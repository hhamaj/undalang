package compiler

import (
	_ "embed"
	"fmt"
	"strconv"
	"strings"

	"undalang/ast"
	"undalang/semantic"
)

//go:embed runtime/unda_runtime.c
var runtimeSource string

var binops = map[string]string{
	"+": "OP_ADD", "-": "OP_SUB", "*": "OP_MUL", "/": "OP_DIV", "%": "OP_MOD",
	"<": "OP_LT", ">": "OP_GT", "<=": "OP_LE", ">=": "OP_GE",
	"==": "OP_EQ", "!=": "OP_NE", "&&": "OP_AND", "||": "OP_OR",
}

// GenerateC lowers an analyzed program to a single C translation unit. The
// program must be free of diagnostics.
//
// Every Unda frame becomes a slot array; identifiers were resolved to
// (depth, slot) pairs by the semantic pass. Subexpressions are evaluated into
// temporaries so Unda's left-to-right evaluation order survives C's
// unspecified argument order.
func GenerateC(prog *semantic.Program) (string, error) {
	if prog.HasErrors() || prog.Main == nil {
		return "", fmt.Errorf("cannot generate C for a program with errors")
	}
	g := &gen{prog: prog, builtins: map[string]bool{}}
	for _, mod := range prog.Modules {
		g.module(mod)
	}

	var out strings.Builder
	out.WriteString(runtimeSource)
	out.WriteString("\n/* ---------- generated program ---------- */\n\n")
	for _, mod := range prog.Modules {
		fmt.Fprintf(&out, "static const char u_file_%d[] = %s;\n", mod.ID, cString(mod.File))
	}
	for _, name := range sortedSet(g.builtins) {
		fmt.Fprintf(&out, "static UValue g_builtin_%s;\n", name)
	}
	out.WriteString(g.decls.String())
	out.WriteString("\n")
	out.WriteString(g.defs.String())
	fmt.Fprintf(&out, "static UValue u_program_main(void) {\n    return mod_%d_body();\n}\n", prog.Main.ID)
	return out.String(), nil
}

type gen struct {
	prog     *semantic.Program
	decls    strings.Builder
	defs     strings.Builder
	builtins map[string]bool
}

func (g *gen) module(mod *semantic.Module) {
	top := mod.Top
	fmt.Fprintf(&g.decls, "static UValue mod_%d_slots[%d];\n", mod.ID, max(top.NumSlots, 1))
	fmt.Fprintf(&g.decls, "static UFrame mod_%d_frame = {NULL, mod_%d_slots};\n", mod.ID, mod.ID)
	fmt.Fprintf(&g.decls, "static UValue mod_%d_body(void);\n", mod.ID)
	for _, fn := range mod.Funcs {
		fmt.Fprintf(&g.decls, "static UValue fn_%d(UFrame *up, int argc, UValue *argv);\n", fn.ID)
	}

	// Module body.
	f := &fgen{g: g, mod: mod, info: top, indent: 1}
	f.line("UFrame *fr = &mod_%d_frame;", mod.ID)
	f.line("UValue *S = fr->slots;")
	f.line("(void)S;")
	result := f.statements(mod.Program.Statements)
	f.line("return %s;", result)
	fmt.Fprintf(&g.defs, "static UValue mod_%d_body(void) {\n%s}\n\n", mod.ID, f.body.String())

	// Module value constructor, used by importers.
	if mod != g.prog.Main {
		names := make([]string, 0, len(mod.Exports))
		slots := make([]string, 0, len(mod.Exports))
		for _, sym := range mod.Exports {
			names = append(names, cString(sym.Name))
			slots = append(slots, strconv.Itoa(sym.Slot))
		}
		namesExpr, slotsExpr := "NULL", "NULL"
		if len(names) > 0 {
			fmt.Fprintf(&g.defs, "static const char *mod_%d_names[] = {%s};\n", mod.ID, strings.Join(names, ", "))
			fmt.Fprintf(&g.defs, "static const int mod_%d_slotidx[] = {%s};\n", mod.ID, strings.Join(slots, ", "))
			namesExpr = fmt.Sprintf("mod_%d_names", mod.ID)
			slotsExpr = fmt.Sprintf("mod_%d_slotidx", mod.ID)
		}
		fmt.Fprintf(&g.decls, "static UValue mod_%d_init(void);\n", mod.ID)
		fmt.Fprintf(&g.defs, `static UValue mod_%[1]d_init(void) {
    static bool done = false;
    static UValue value;
    if (done) return value;
    done = true;
    mod_%[1]d_body();
    value = u_module_from_frame(%[2]s, %[3]d, %[4]s, %[5]s, &mod_%[1]d_frame);
    return value;
}

`, mod.ID, cString(mod.Name), len(names), namesExpr, slotsExpr)
	}

	for _, fn := range mod.Funcs {
		g.function(mod, fn)
	}
}

func (g *gen) function(mod *semantic.Module, info *semantic.FuncInfo) {
	f := &fgen{g: g, mod: mod, info: info, indent: 1}
	f.line("(void)argc; (void)argv;")
	if info.HeapFrame {
		f.line("UFrame *fr = u_frame_new(up, %d);", info.NumSlots)
	} else {
		f.line("UValue S_[%d];", max(info.NumSlots, 1))
		f.line("memset(S_, 0, sizeof S_);")
		f.line("UFrame fr_ = {up, S_};")
		f.line("UFrame *fr = &fr_;")
	}
	f.line("UValue *S = fr->slots;")
	f.line("(void)S;")
	for i, slot := range info.ParamSlots {
		f.line("S[%d] = argv[%d];", slot, i)
	}
	result := f.statements(info.Lit.Body.Statements)
	f.line("return %s;", result)
	fmt.Fprintf(&g.defs, "static UValue fn_%d(UFrame *up, int argc, UValue *argv) {\n%s}\n\n", info.ID, f.body.String())
}

type fgen struct {
	g      *gen
	mod    *semantic.Module
	info   *semantic.FuncInfo
	body   strings.Builder
	tmp    int
	indent int
}

func (f *fgen) line(format string, args ...interface{}) {
	f.body.WriteString(strings.Repeat("    ", f.indent))
	fmt.Fprintf(&f.body, format, args...)
	f.body.WriteString("\n")
}

func (f *fgen) temp() string {
	f.tmp++
	return fmt.Sprintf("t%d", f.tmp)
}

func (f *fgen) loc(node ast.Node) {
	line, col := ast.Position(node)
	f.line("U_LOC(u_file_%d, %d, %d);", f.mod.ID, line, col)
}

func (f *fgen) slot(c semantic.Candidate) string {
	if c.Depth == 0 {
		return fmt.Sprintf("S[%d]", c.Sym.Slot)
	}
	return fmt.Sprintf("u_up(fr, %d)->slots[%d]", c.Depth, c.Sym.Slot)
}

// statements emits a statement list and returns the C expression holding the
// value of the last one (a block's value in Unda).
func (f *fgen) statements(stmts []ast.Statement) string {
	result := "uv_nil()"
	for _, stmt := range stmts {
		result = f.stmt(stmt)
	}
	return result
}

func (f *fgen) block(b *ast.BlockStatement) string {
	return f.statements(b.Statements)
}

func (f *fgen) stmt(s ast.Statement) string {
	switch s := s.(type) {
	case *ast.LetStatement:
		v := f.expr(s.Value)
		sym := f.mod.Decls[s]
		f.line("S[%d] = %s;", sym.Slot, v)
		return v
	case *ast.AssignStatement:
		v := f.expr(s.Value)
		f.assign(s, v)
		return v
	case *ast.ReturnStatement:
		v := f.expr(s.ReturnValue)
		f.line("return %s;", v)
		return "uv_nil()"
	case *ast.ImportStatement:
		t := f.temp()
		imp := f.mod.Imports[s]
		if imp.Std != "" {
			f.line("UValue %s = u_std_module(%s);", t, cString(imp.Std))
		} else {
			f.line("UValue %s = mod_%d_init();", t, imp.Module.ID)
		}
		f.line("S[%d] = %s;", f.mod.Decls[s].Slot, t)
		return t
	case *ast.ExpressionStatement:
		return f.expr(s.Expression)
	case *ast.BlockStatement:
		return f.block(s)
	}
	panic(fmt.Sprintf("cgen: unsupported statement %T", s))
}

func (f *fgen) assign(s *ast.AssignStatement, v string) {
	ref := f.mod.Assigns[s]
	if len(ref.Candidates) == 1 && ref.Candidates[0].Depth == 0 {
		f.line("S[%d] = %s;", ref.Candidates[0].Sym.Slot, v)
		return
	}
	opened := 0
	for i, cand := range ref.Candidates {
		lv := f.slot(cand)
		if cand.Depth == 0 {
			f.line("%s = %s;", lv, v)
			break
		}
		f.line("if (%s.tag != U_UNDEF) {", lv)
		f.indent++
		if !cand.Sym.Mutable {
			f.loc(s)
			f.line("u_panic(\"cannot assign to immutable binding \\\"%s\\\"\");", ref.Name)
		} else {
			f.line("%s = %s;", lv, v)
		}
		f.indent--
		f.line("} else {")
		f.indent++
		opened++
		if i == len(ref.Candidates)-1 {
			f.loc(s)
			f.line("u_panic(\"identifier not found: %s\");", ref.Name)
		}
	}
	for ; opened > 0; opened-- {
		f.indent--
		f.line("}")
	}
}

func (f *fgen) identifier(id *ast.Identifier) string {
	ref := f.mod.Refs[id]
	t := f.temp()
	if len(ref.Candidates) == 0 {
		f.g.builtins[ref.Builtin] = true
		f.line("UValue %s = g_builtin_%s.tag ? g_builtin_%s : (g_builtin_%s = u_global_builtin(%s));",
			t, ref.Builtin, ref.Builtin, ref.Builtin, cString(ref.Builtin))
		return t
	}
	f.line("UValue %s = %s;", t, f.slot(ref.Candidates[0]))
	if ref.Candidates[0].Depth == 0 {
		return t
	}
	// The binding lives in an enclosing frame and may not have been
	// initialized yet; fall back outward like the interpreter's env chain.
	opened := 0
	for _, cand := range ref.Candidates[1:] {
		f.line("if (%s.tag == U_UNDEF) {", t)
		f.indent++
		opened++
		f.line("%s = %s;", t, f.slot(cand))
	}
	f.line("if (%s.tag == U_UNDEF) {", t)
	f.indent++
	opened++
	if ref.Builtin != "" {
		f.line("%s = u_global_builtin(%s);", t, cString(ref.Builtin))
	} else {
		f.loc(id)
		f.line("u_panic(\"identifier not found: %s\");", ref.Name)
	}
	for ; opened > 0; opened-- {
		f.indent--
		f.line("}")
	}
	return t
}

// expr emits the statements computing e and returns a side-effect-free C
// expression for its value.
func (f *fgen) expr(e ast.Expression) string {
	switch e := e.(type) {
	case *ast.IntegerLiteral:
		return fmt.Sprintf("uv_int(INT64_C(%d))", e.Value)
	case *ast.FloatLiteral:
		return fmt.Sprintf("uv_float(%s)", strconv.FormatFloat(e.Value, 'g', -1, 64)+cFloatSuffix(e.Value))
	case *ast.StringLiteral:
		return fmt.Sprintf("uv_str_n(%s, %d)", cString(e.Value), len(e.Value))
	case *ast.Boolean:
		if e.Value {
			return "uv_bool(true)"
		}
		return "uv_bool(false)"
	case *ast.NilLiteral:
		return "uv_nil()"
	case *ast.Identifier:
		return f.identifier(e)
	case *ast.PrefixExpression:
		r := f.expr(e.Right)
		t := f.temp()
		if e.Operator == "!" {
			f.line("UValue %s = u_not(%s);", t, r)
			return t
		}
		f.loc(e)
		f.line("UValue %s = u_neg(%s);", t, r)
		return t
	case *ast.InfixExpression:
		return f.infix(e)
	case *ast.PipelineExpression:
		left := f.expr(e.Left)
		var callee string
		args := []string{left}
		if call, ok := e.Right.(*ast.CallExpression); ok {
			callee = f.expr(call.Function)
			for _, a := range call.Arguments {
				args = append(args, f.expr(a))
			}
		} else {
			callee = f.expr(e.Right)
		}
		return f.call(e, callee, args)
	case *ast.IfExpression:
		cond := f.expr(e.Condition)
		t := f.temp()
		f.line("UValue %s;", t)
		f.line("if (u_truthy(%s)) {", cond)
		f.indent++
		f.line("%s = %s;", t, f.block(e.Consequence))
		f.indent--
		f.line("} else {")
		f.indent++
		if e.Alternative != nil {
			f.line("%s = %s;", t, f.block(e.Alternative))
		} else {
			f.line("%s = uv_nil();", t)
		}
		f.indent--
		f.line("}")
		return t
	case *ast.FunctionLiteral:
		info := f.mod.FuncOf[e]
		t := f.temp()
		name := "NULL"
		if e.Name != "" {
			name = cString(e.Name)
		}
		f.line("UValue %s = u_closure(fn_%d, fr, %d, %s, %s);", t, info.ID, len(e.Parameters), name, cString(functionInspect(e)))
		return t
	case *ast.ArrayLiteral:
		elems := make([]string, 0, len(e.Elements))
		for _, el := range e.Elements {
			elems = append(elems, f.expr(el))
		}
		t := f.temp()
		f.line("UValue %s = uv_arrv(u_arr_new(%d));", t, len(elems))
		for _, el := range elems {
			f.line("u_arr_push(%s.as.a, %s);", t, el)
		}
		return t
	case *ast.MapLiteral:
		t := f.temp()
		f.line("UValue %s = uv_mapv(u_map_new());", t)
		for _, pair := range e.Pairs {
			k := f.expr(pair.Key)
			f.loc(e)
			f.line("u_map_literal_key(%s);", k)
			v := f.expr(pair.Value)
			f.line("u_map_set(%s.as.m, %s, %s);", t, k, v)
		}
		return t
	case *ast.IndexExpression:
		l := f.expr(e.Left)
		i := f.expr(e.Index)
		t := f.temp()
		f.loc(e)
		f.line("UValue %s = u_index(%s, %s);", t, l, i)
		return t
	case *ast.MemberExpression:
		o := f.expr(e.Object)
		t := f.temp()
		f.loc(e)
		f.line("UValue %s = u_member(%s, %s);", t, o, cString(e.Property.Value))
		return t
	case *ast.MatchExpression:
		v := f.expr(e.Value)
		t := f.temp()
		f.line("UValue %s = uv_nil();", t)
		f.matchArms(e.Arms, v, t)
		return t
	case *ast.CallExpression:
		callee := f.expr(e.Function)
		args := make([]string, 0, len(e.Arguments))
		for _, a := range e.Arguments {
			args = append(args, f.expr(a))
		}
		return f.call(e, callee, args)
	}
	panic(fmt.Sprintf("cgen: unsupported expression %T", e))
}

func (f *fgen) infix(e *ast.InfixExpression) string {
	op := binops[e.Operator]
	l := f.expr(e.Left)
	t := f.temp()
	if e.Operator == "&&" || e.Operator == "||" {
		f.line("UValue %s;", t)
		if e.Operator == "&&" {
			f.line("if (!u_truthy(%s)) {", l)
			f.line("    %s = uv_bool(false);", t)
		} else {
			f.line("if (u_truthy(%s)) {", l)
			f.line("    %s = uv_bool(true);", t)
		}
		f.line("} else {")
		f.indent++
		r := f.expr(e.Right)
		f.loc(e)
		f.line("%s = u_binop(%s, %s, %s);", t, op, l, r)
		f.indent--
		f.line("}")
		return t
	}
	r := f.expr(e.Right)
	f.loc(e)
	f.line("UValue %s = u_binop(%s, %s, %s);", t, op, l, r)
	return t
}

func (f *fgen) call(node ast.Node, callee string, args []string) string {
	t := f.temp()
	f.loc(node)
	if len(args) == 0 {
		f.line("UValue %s = u_call(%s, 0, NULL);", t, callee)
	} else {
		f.line("UValue %s = u_call(%s, %d, (UValue[]){%s});", t, callee, len(args), strings.Join(args, ", "))
	}
	return t
}

func (f *fgen) matchArms(arms []ast.MatchArm, value, result string) {
	if len(arms) == 0 {
		return
	}
	arm := arms[0]
	if arm.Wildcard {
		f.line("%s = %s;", result, f.expr(arm.Value))
		return
	}
	p := f.expr(arm.Pattern)
	f.line("if (u_equal(%s, %s)) {", value, p)
	f.indent++
	f.line("%s = %s;", result, f.expr(arm.Value))
	f.indent--
	f.line("} else {")
	f.indent++
	f.matchArms(arms[1:], value, result)
	f.indent--
	f.line("}")
}

// functionInspect reproduces object.Function.Inspect for a literal.
func functionInspect(lit *ast.FunctionLiteral) string {
	params := make([]string, 0, len(lit.Parameters))
	for _, p := range lit.Parameters {
		params = append(params, p.String())
	}
	var out strings.Builder
	out.WriteString("fn")
	if lit.Name != "" {
		out.WriteString(" " + lit.Name)
	}
	out.WriteString("(" + strings.Join(params, ", ") + ") {\n")
	out.WriteString(lit.Body.String())
	out.WriteString("\n}")
	return out.String()
}

// cString renders s as a C string literal using octal escapes, which cannot
// swallow following digits the way \x escapes do.
func cString(s string) string {
	var out strings.Builder
	out.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"' || c == '\\':
			out.WriteByte('\\')
			out.WriteByte(c)
		case c == '?':
			out.WriteString(`\?`)
		case c < 0x20 || c >= 0x7f:
			fmt.Fprintf(&out, "\\%03o", c)
		default:
			out.WriteByte(c)
		}
	}
	out.WriteByte('"')
	return out.String()
}

func cFloatSuffix(v float64) string {
	s := strconv.FormatFloat(v, 'g', -1, 64)
	if strings.ContainsAny(s, ".eE") {
		return ""
	}
	return ".0"
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}
