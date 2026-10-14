package compiler

import (
	_ "embed"
	"fmt"
	"math"
	"strconv"
	"strings"

	"undalang/ast"
	"undalang/semantic"
)

//go:embed runtime/unda_runtime.c
var runtimeSource string

var tagNames = map[string]string{
	"INTEGER": "U_INT", "FLOAT": "U_FLOAT", "STRING": "U_STR", "BOOLEAN": "U_BOOL", "NIL": "U_NIL",
	"ARRAY": "U_ARR", "MAP": "U_MAP", "FUNCTION": "U_FN", "BUILTIN": "U_BUILTIN", "MODULE": "U_MOD",
	"RESULT": "U_RES", "POINTER": "U_PTR", "BUFFER": "U_BUF",
}

var binops = map[string]string{
	"+": "OP_ADD", "-": "OP_SUB", "*": "OP_MUL", "/": "OP_DIV", "%": "OP_MOD",
	"<": "OP_LT", ">": "OP_GT", "<=": "OP_LE", ">=": "OP_GE",
	"==": "OP_EQ", "!=": "OP_NE", "&&": "OP_AND", "||": "OP_OR",
}

// GenerateC lowers an analyzed program to a single C translation unit. the
// program must not have any diagnostics
//
// every Unda frame becomes a slot array and the semantic pass already
// resolved identifiers to (depth, slot) pairs. subexpressions go into
// temporaries so Unda's left to right evaluation order survives C's
// unspecified argument order
func GenerateC(prog *semantic.Program) (string, error) {
	if prog.HasErrors() || prog.Main == nil {
		return "", fmt.Errorf("cannot generate C for a program with errors")
	}
	g := &gen{prog: prog, builtins: map[string]bool{}, types: map[string]string{},
		sdefs: map[*ast.StructStatement]string{}, cstructs: map[string]string{}}
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
	// static storage that can hold heap values is a GC root
	out.WriteString("static void u_register_roots(void) {\n")
	for _, mod := range prog.Modules {
		fmt.Fprintf(&out, "    u_gc_add_root(mod_%d_slots, sizeof mod_%d_slots);\n", mod.ID, mod.ID)
		if mod != prog.Main {
			fmt.Fprintf(&out, "    u_gc_add_root(&mod_%d_value, sizeof mod_%d_value);\n", mod.ID, mod.ID)
		}
	}
	for _, name := range sortedSet(g.builtins) {
		fmt.Fprintf(&out, "    u_gc_add_root(&g_builtin_%s, sizeof g_builtin_%s);\n", name, name)
	}
	out.WriteString("}\n")
	fmt.Fprintf(&out, "static UValue u_program_main(void) {\n    return mod_%d_body();\n}\n", prog.Main.ID)
	return out.String(), nil
}

type gen struct {
	prog     *semantic.Program
	decls    strings.Builder
	defs     strings.Builder
	builtins map[string]bool
	structs  int
	externs  int
	// types holds the UType each annotation was emitted as, by its text
	types map[string]string
	// sdefs and cstructs remember what each struct was emitted as: its
	// UStructDef, and for extern structs the C typedef
	sdefs    map[*ast.StructStatement]string
	cstructs map[string]string
}

var cTypeNames = map[string]string{
	"i8": "int8_t", "i16": "int16_t", "i32": "int32_t", "i64": "int64_t",
	"u8": "uint8_t", "u16": "uint16_t", "u32": "uint32_t", "u64": "uint64_t",
	"f32": "float", "f64": "double", "bool": "bool", "cstring": "const char *", "ptr": "void *",
}

// cType is the C spelling of an extern type
func (g *gen) cType(t *ast.TypeAnnotation) string {
	if t.IsStruct() {
		return g.cstructs[t.Name]
	}
	return cTypeNames[t.Name]
}

// cArg is the conversion from an Unda value to a C value of type t. what
// and field are C expressions that name the value in errors, like
// "area argument 1" and "" or what and "x" for a struct field
func (g *gen) cArg(t *ast.TypeAnnotation, v, what, field string) string {
	switch t.Name {
	case "f32", "f64":
		return fmt.Sprintf("(%s)u_ffi_float(%s, %s, %s)", cTypeNames[t.Name], v, what, field)
	case "bool":
		return fmt.Sprintf("u_ffi_bool(%s, %s, %s)", v, what, field)
	case "cstring":
		return fmt.Sprintf("u_ffi_cstring(%s, %s, %s)", v, what, field)
	case "ptr":
		return fmt.Sprintf("u_ffi_ptr(%s, %s, %s)", v, what, field)
	case "u64":
		return fmt.Sprintf("u_ffi_uint(%s, %s, %s, UINT64_MAX, \"u64\")", v, what, field)
	case "u8", "u16", "u32":
		return fmt.Sprintf("(%s)u_ffi_uint(%s, %s, %s, UINT%s_MAX, %q)", cTypeNames[t.Name], v, what, field, t.Name[1:], t.Name)
	}
	if t.IsStruct() {
		return fmt.Sprintf("%s_in(%s, %s)", g.cstructs[t.Name], v, what)
	}
	return fmt.Sprintf("(%s)u_ffi_int(%s, %s, %s, INT%s_MIN, INT%s_MAX, %q)", cTypeNames[t.Name], v, what, field, t.Name[1:], t.Name[1:], t.Name)
}

// cResult turns the C value e of type t into an Unda value. fn names the
// function in errors
func (g *gen) cResult(t *ast.TypeAnnotation, e, fn string) string {
	switch {
	case t.Name == "f32" || t.Name == "f64":
		return "uv_float((double)" + e + ")"
	case t.Name == "bool":
		return "uv_bool(" + e + ")"
	case t.Name == "cstring":
		return "u_ffi_cstr_out(" + e + ")"
	case t.Name == "ptr":
		return "u_ffi_ptr_out(" + e + ")"
	case t.Name == "u64":
		return fmt.Sprintf("u_ffi_u64_out(%s, %s)", e, fn)
	case t.IsStruct():
		return fmt.Sprintf("%s_out(%s, %s)", g.cstructs[t.Name], e, fn)
	}
	return "uv_int((int64_t)" + e + ")"
}

// externStruct emits the C typedef for a struct with a C layout and the
// functions that convert it to and from the Unda struct. C needs a struct's
// field types first, so fields naming structs of the same block go first
func (g *gen) externStruct(es *ast.ExternStruct, block []*ast.ExternStruct) {
	if _, done := g.cstructs[es.Decl.Name.Value]; done {
		return
	}
	g.cstructs[es.Decl.Name.Value] = ""
	for _, t := range es.CTypes {
		for _, other := range block {
			if t.IsStruct() && other.Decl.Name.Value == t.Name {
				g.externStruct(other, block)
			}
		}
	}
	sdef := g.structDef(es.Decl)
	name := "ucs_" + strings.TrimPrefix(sdef, "sdef_")
	g.cstructs[es.Decl.Name.Value] = name
	fields := make([]string, len(es.CTypes))
	in := make([]string, len(es.CTypes))
	out := make([]string, len(es.CTypes))
	for i, t := range es.CTypes {
		f := es.Decl.Fields[i].Value
		fields[i] = fmt.Sprintf("%s %s;", g.cType(t), f)
		in[i] = fmt.Sprintf("    c.%s = %s;\n", f, g.cArg(t, fmt.Sprintf("v.as.st->fields[%d]", i), "what", cString(f)))
		out[i] = fmt.Sprintf("    st->fields[%d] = %s;\n", i, g.cResult(t, "c."+f, "fn"))
	}
	fmt.Fprintf(&g.decls, "typedef struct { %s } %s;\n", strings.Join(fields, " "), name)
	fmt.Fprintf(&g.decls, `static %[1]s %[1]s_in(UValue v, const char *what) {
    %[1]s c;
    if (v.tag != U_STRUCT || v.as.st->def != &%[2]s) u_panic("%%s must be %[3]s, got %%s", what, u_type_name(v));
%[4]s    return c;
}
static UValue %[1]s_out(%[1]s c, const char *fn) {
    (void)fn;
    UStruct *st = u_alloc(sizeof(UStruct) + %[5]d * sizeof(UValue));
    st->def = &%[2]s;
%[6]s    UValue v; v.tag = U_STRUCT; v.as.st = st;
    return v;
}
`, name, sdef, es.Decl.Name.Value, strings.Join(in, ""), max(len(es.CTypes), 1), strings.Join(out, ""))
}

// externFunc emits the C declaration of an extern function and a builtin
// wrapper that converts the arguments and the result. the declaration uses
// an asm label so it can't clash with whatever the runtime's headers declare
// under the same name
func (g *gen) externFunc(fn *ast.ExternFunc) string {
	g.externs++
	id := fmt.Sprintf("ufx_%d", g.externs)
	ret := "void"
	if fn.Return != nil {
		ret = g.cType(fn.Return)
	}
	params := make([]string, len(fn.Types))
	args := make([]string, len(fn.Types))
	for i, t := range fn.Types {
		params[i] = g.cType(t)
		args[i] = g.cArg(t, fmt.Sprintf("argv[%d]", i), cString(fmt.Sprintf("%s argument %d", fn.Name.Value, i+1)), `""`)
	}
	if len(params) == 0 {
		params = []string{"void"}
	}
	fmt.Fprintf(&g.decls, "extern %s %s(%s) __asm__(%s);\n", ret, id, strings.Join(params, ", "), cString(fn.Name.Value))
	call := fmt.Sprintf("%s(%s)", id, strings.Join(args, ", "))
	result := call + ";\n    return uv_nil();"
	if fn.Return != nil {
		result = "return " + g.cResult(fn.Return, call, cString(fn.Name.Value)) + ";"
	}
	fmt.Fprintf(&g.defs, "static UValue %s_call(UFrame *env_, int argc, UValue *argv) {\n    (void)env_; (void)argv;\n    U_ARGC(%s, %d);\n    %s\n}\n\n",
		id, cString(fn.Name.Value), len(fn.Types), result)
	return id + "_call"
}

func (g *gen) module(mod *semantic.Module) {
	top := mod.Top
	fmt.Fprintf(&g.decls, "static UValue mod_%d_slots[%d];\n", mod.ID, max(top.NumSlots, 1))
	fmt.Fprintf(&g.decls, "static UFrame mod_%d_frame = {NULL, mod_%d_slots};\n", mod.ID, mod.ID)
	fmt.Fprintf(&g.decls, "static UValue mod_%d_body(void);\n", mod.ID)
	for _, fn := range mod.Funcs {
		fmt.Fprintf(&g.decls, "static UValue fn_%d(UFrame *up, int argc, UValue *argv);\n", fn.ID)
	}

	f := &fgen{g: g, mod: mod, info: top, indent: 1}
	f.line("UFrame *fr = &mod_%d_frame;", mod.ID)
	f.line("UValue *S = fr->slots;")
	f.line("(void)S;")
	result := f.statements(mod.Program.Statements)
	f.line("return %s;", result)
	fmt.Fprintf(&g.defs, "static UValue mod_%d_body(void) {\n%s}\n\n", mod.ID, f.body.String())

	// module value constructor, importers use this
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
		fmt.Fprintf(&g.decls, "static bool mod_%d_done;\nstatic UValue mod_%d_value;\n", mod.ID, mod.ID)
		fmt.Fprintf(&g.defs, `static UValue mod_%[1]d_init(void) {
    if (mod_%[1]d_done) return mod_%[1]d_value;
    mod_%[1]d_done = true;
    mod_%[1]d_body();
    mod_%[1]d_value = u_module_from_frame(%[2]s, %[3]d, %[4]s, %[5]s, &mod_%[1]d_frame);
    return mod_%[1]d_value;
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
	lit := info.Lit
	for i, slot := range info.ParamSlots {
		if i < len(lit.ParamTypes) && lit.ParamTypes[i] != nil {
			f.checkType(fmt.Sprintf("argv[%d]", i), lit.ParamTypes[i], "parameter "+lit.Parameters[i].Value+" expects")
		}
		f.line("S[%d] = argv[%d];", slot, i)
	}
	result := f.statements(lit.Body.Statements)
	if lit.ReturnType != nil {
		var at ast.Node = lit.Body
		if n := len(lit.Body.Statements); n > 0 {
			at = lit.Body.Statements[n-1]
		}
		f.loc(at)
		f.checkType(result, lit.ReturnType, returnLabel(lit))
	}
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

// checkType emits the runtime check for an annotation. it uses whatever
// location is current, callers set it first when they need a different one
func (f *fgen) checkType(v string, t *ast.TypeAnnotation, what string) {
	if t.Name == "any" {
		return
	}
	f.line("u_check(%s, &%s, %s);", v, f.g.typeDesc(t), cString(what+" "+t.String()))
}

// typeDesc emits the static UType for an annotation once and returns its
// name. element types come first since the outer one points at them
func (g *gen) typeDesc(t *ast.TypeAnnotation) string {
	key := t.String()
	if name, ok := g.types[key]; ok {
		return name
	}
	elem, k := "NULL", "NULL"
	switch {
	case t.Name == "array" && len(t.Params) == 1:
		elem = "&" + g.typeDesc(t.Params[0])
	case t.Name == "map" && len(t.Params) == 2:
		k = "&" + g.typeDesc(t.Params[0])
		elem = "&" + g.typeDesc(t.Params[1])
	}
	name := fmt.Sprintf("ut_%d", len(g.types)+1)
	g.types[key] = name
	mask, sname := "~0u", "NULL"
	switch {
	case t.IsStruct():
		mask, sname = "0", cString(t.Name)
	case t.Name != "any":
		tags := make([]string, 0, len(ast.TypeNames[t.Name]))
		for _, n := range ast.TypeNames[t.Name] {
			tags = append(tags, "(1u << "+tagNames[n]+")")
		}
		mask = strings.Join(tags, " | ")
	}
	fmt.Fprintf(&g.decls, "static const UType %s = {%s, %s, %s, %s};\n", name, mask, sname, elem, k)
	return name
}

// structDef emits the static definition for a struct statement and returns
// its C name
func (g *gen) structDef(s *ast.StructStatement) string {
	if name, ok := g.sdefs[s]; ok {
		return name
	}
	g.structs++
	name := fmt.Sprintf("sdef_%d", g.structs)
	fields := make([]string, len(s.Fields))
	types := make([]string, len(s.Fields))
	tnames := make([]string, len(s.Fields))
	for i, f := range s.Fields {
		fields[i] = cString(f.Value)
		types[i], tnames[i] = "NULL", "NULL"
		if t := s.Types[i]; t != nil && t.Name != "any" {
			types[i], tnames[i] = "&"+g.typeDesc(t), cString(t.String())
		}
	}
	if len(fields) == 0 {
		fields, types, tnames = []string{"NULL"}, []string{"NULL"}, []string{"NULL"}
	}
	fmt.Fprintf(&g.decls, "static const char *%s_fields[] = {%s};\n", name, strings.Join(fields, ", "))
	fmt.Fprintf(&g.decls, "static const UType *const %s_types[] = {%s};\n", name, strings.Join(types, ", "))
	fmt.Fprintf(&g.decls, "static const char *%s_tnames[] = {%s};\n", name, strings.Join(tnames, ", "))
	fmt.Fprintf(&g.decls, "static UStructDef %s = {%s, %d, %s_fields, %s_types, %s_tnames};\n", name, cString(s.Name.Value), len(s.Fields), name, name, name)
	g.sdefs[s] = name
	return name
}

func returnLabel(lit *ast.FunctionLiteral) string {
	if lit.Name != "" {
		return lit.Name + " returns"
	}
	return "function returns"
}

func (f *fgen) slot(c semantic.Candidate) string {
	if c.Depth == 0 {
		return fmt.Sprintf("S[%d]", c.Sym.Slot)
	}
	return fmt.Sprintf("u_up(fr, %d)->slots[%d]", c.Depth, c.Sym.Slot)
}

// statements emits a statement list and returns the C expression holding the
// last one's value, which is the block's value in Unda
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
		if s.Type != nil {
			f.loc(s)
			f.checkType(v, s.Type, s.Name.Value+" expects")
		}
		sym := f.mod.Decls[s]
		f.line("S[%d] = %s;", sym.Slot, v)
		return v
	case *ast.AssignStatement:
		v := f.expr(s.Value)
		if len(s.Fields) > 0 {
			// read the current value, build the changed copy, assign that
			cur := f.readRef(f.mod.Assigns[s], s.Name)
			names := make([]string, len(s.Fields))
			for i, field := range s.Fields {
				names[i] = cString(field.Value)
			}
			t := f.temp()
			f.loc(s)
			f.line("UValue %s = u_with_fields(%s, %d, (const char *[]){%s}, %s);", t, cur, len(names), strings.Join(names, ", "), v)
			v = t
		}
		f.assign(s, v)
		return v
	case *ast.ReturnStatement:
		v := f.expr(s.ReturnValue)
		if lit := f.info.Lit; lit != nil && lit.ReturnType != nil {
			f.loc(s)
			f.checkType(v, lit.ReturnType, returnLabel(lit))
		}
		f.line("return %s;", v)
		return "uv_nil()"
	case *ast.ExternStatement:
		for _, es := range s.Structs {
			f.g.externStruct(es, s.Structs)
			f.line("S[%d] = uv_sdef(&%s);", f.mod.Decls[es.Decl].Slot, f.g.structDef(es.Decl))
		}
		for _, fn := range s.Funcs {
			f.line("S[%d] = u_builtin(%s, %s);", f.mod.Bindings[fn.Name].Slot, f.g.externFunc(fn), cString(fn.Name.Value))
		}
		return "uv_nil()"
	case *ast.StructStatement:
		t := f.temp()
		f.line("UValue %s = uv_sdef(&%s);", t, f.g.structDef(s))
		f.line("S[%d] = %s;", f.mod.Decls[s].Slot, t)
		return t
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
	case *ast.WhileStatement:
		f.line("for (;;) {")
		f.indent++
		cond := f.expr(s.Condition)
		f.line("if (!u_truthy(%s)) break;", cond)
		f.loopBody(s, s.Body, nil)
		f.indent--
		f.line("}")
		return "uv_nil()"
	case *ast.ForStatement:
		it := f.expr(s.Iterable)
		n := f.tmp + 1
		f.tmp++
		f.loc(s)
		f.line("UArr *k%d = NULL, *v%d = NULL;", n, n)
		f.line("u_iter(%s, %d, &k%d, &v%d);", it, len(s.Vars), n, n)
		f.line("for (size_t i%d = 0; i%d < k%d->len; i%d++) {", n, n, n, n)
		f.indent++
		f.loopBody(s, s.Body, func() {
			f.line("S[%d] = k%d->items[i%d];", f.mod.Bindings[s.Vars[0]].Slot, n, n)
			if len(s.Vars) == 2 {
				f.line("S[%d] = v%d->items[i%d];", f.mod.Bindings[s.Vars[1]].Slot, n, n)
			}
		})
		f.indent--
		f.line("}")
		return "uv_nil()"
	case *ast.BreakStatement:
		f.line("break;")
		return "uv_nil()"
	case *ast.ContinueStatement:
		f.line("continue;")
		return "uv_nil()"
	}
	panic(fmt.Sprintf("cgen: unsupported statement %T", s))
}

// loopBody emits one iteration. if the body creates closures every iteration
// gets a fresh heap frame, the C block shadows fr and S so the body code
// stays the same. break and continue map straight to C's since the generator
// emits no other C loops or switches
func (f *fgen) loopBody(loop ast.Statement, body *ast.BlockStatement, bind func()) {
	frame := f.mod.LoopFrames[loop]
	if frame != nil {
		parent := f.temp()
		f.line("UFrame *%s = fr;", parent)
		f.line("{")
		f.indent++
		f.line("UFrame *fr = u_frame_new(%s, %d);", parent, frame.NumSlots)
		f.line("UValue *S = fr->slots;")
		f.line("(void)S;")
	}
	if bind != nil {
		bind()
	}
	f.block(body)
	if frame != nil {
		f.indent--
		f.line("}")
	}
}

func (f *fgen) assign(s *ast.AssignStatement, v string) {
	ref := f.mod.Assigns[s]
	opened := 0
	for i, cand := range ref.Candidates {
		lv := f.slot(cand)
		if cand.Direct {
			f.assignChecked(s, cand.Sym, lv, v)
			break
		}
		f.line("if (%s.tag != U_UNDEF) {", lv)
		f.indent++
		if !cand.Sym.Mutable {
			f.loc(s)
			f.line("u_panic(\"cannot assign to immutable binding \\\"%s\\\"\");", ref.Name)
		} else {
			f.assignChecked(s, cand.Sym, lv, v)
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

func (f *fgen) assignChecked(s *ast.AssignStatement, sym *semantic.Symbol, lv, v string) {
	if sym.Annot != nil {
		f.loc(s)
		f.checkType(v, sym.Annot, sym.Name+" expects")
	}
	f.line("%s = %s;", lv, v)
}

func (f *fgen) identifier(id *ast.Identifier) string {
	return f.readRef(f.mod.Refs[id], id)
}

// readRef loads whatever ref resolves to at runtime. at is where an
// "identifier not found" error points
func (f *fgen) readRef(ref *semantic.Ref, at ast.Node) string {
	t := f.temp()
	if len(ref.Candidates) == 0 {
		f.g.builtins[ref.Builtin] = true
		f.line("UValue %s = g_builtin_%s.tag ? g_builtin_%s : (g_builtin_%s = u_global_builtin(%s));",
			t, ref.Builtin, ref.Builtin, ref.Builtin, cString(ref.Builtin))
		return t
	}
	f.line("UValue %s = %s;", t, f.slot(ref.Candidates[0]))
	if ref.Candidates[0].Direct {
		return t
	}
	// the binding lives in an outer frame and might not be initialized yet,
	// so fall back outward like the interpreter's env chain
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
		f.loc(at)
		f.line("u_panic(\"identifier not found: %s\");", ref.Name)
	}
	for ; opened > 0; opened-- {
		f.indent--
		f.line("}")
	}
	return t
}

// expr emits the statements that compute e and returns a side-effect free C
// expression for its value
func (f *fgen) expr(e ast.Expression) string {
	switch e := e.(type) {
	case *ast.IntegerLiteral:
		return intLiteral(e.Value)
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
		if v, ok := foldInt(e); ok {
			return intLiteral(v)
		}
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
		calleeExpr := e.Right
		if call, ok := e.Right.(*ast.CallExpression); ok {
			calleeExpr = call.Function
		}
		return f.call(e, calleeExpr, callee, args)
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
		if e.ArgNames != nil {
			names := make([]string, len(e.ArgNames))
			for i, n := range e.ArgNames {
				names[i] = cString(n.Value)
			}
			t := f.temp()
			f.loc(e)
			f.line("UValue %s = u_call_named(%s, %d, (UValue[]){%s}, (const char *[]){%s});",
				t, callee, len(args), strings.Join(args, ", "), strings.Join(names, ", "))
			return t
		}
		return f.call(e, e.Function, callee, args)
	case *ast.TryExpression:
		return f.try(e)
	}
	panic(fmt.Sprintf("cgen: unsupported expression %T", e))
}

// try mirrors evaluator.evalTry. inside a function an err result is returned
// as is, at the top level there's nothing to return from
func (f *fgen) try(e *ast.TryExpression) string {
	v := f.expr(e.Value)
	f.loc(e)
	f.line("u_want_result(%s);", v)
	f.line("if (!%s.as.r->ok) {", v)
	f.indent++
	if lit := f.info.Lit; lit == nil {
		f.line("u_unhandled(%s);", v)
	} else {
		if lit.ReturnType != nil {
			f.checkType(v, lit.ReturnType, returnLabel(lit))
		}
		f.line("return %s;", v)
	}
	f.indent--
	f.line("}")
	t := f.temp()
	f.line("UValue %s = %s.as.r->v;", t, v)
	return t
}

// fastInt is the C for an operator when both sides are ints. it has to give
// exactly what u_binop gives, so + - * wrap around instead of overflowing
var fastInt = map[string]string{
	"+":  "uv_int((int64_t)((uint64_t)%[1]s.as.i + (uint64_t)%[2]s.as.i))",
	"-":  "uv_int((int64_t)((uint64_t)%[1]s.as.i - (uint64_t)%[2]s.as.i))",
	"*":  "uv_int((int64_t)((uint64_t)%[1]s.as.i * (uint64_t)%[2]s.as.i))",
	"<":  "uv_bool(%[1]s.as.i < %[2]s.as.i)",
	">":  "uv_bool(%[1]s.as.i > %[2]s.as.i)",
	"<=": "uv_bool(%[1]s.as.i <= %[2]s.as.i)",
	">=": "uv_bool(%[1]s.as.i >= %[2]s.as.i)",
	"==": "uv_bool(%[1]s.as.i == %[2]s.as.i)",
	"!=": "uv_bool(%[1]s.as.i != %[2]s.as.i)",
	// a positive divisor can't hit division by zero or INT64_MIN / -1
	"/": "uv_int(%[1]s.as.i / %[2]s.as.i)",
	"%": "uv_int(%[1]s.as.i %% %[2]s.as.i)",
}

func (f *fgen) infix(e *ast.InfixExpression) string {
	op := binops[e.Operator]
	if v, ok := foldInt(e); ok {
		return intLiteral(v)
	}
	if b, ok := foldCompare(e); ok {
		return fmt.Sprintf("uv_bool(%t)", b)
	}
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
	if fast, ok := fastInt[e.Operator]; ok {
		cond := fmt.Sprintf("%s.tag == U_INT && %s.tag == U_INT", l, r)
		if e.Operator == "/" || e.Operator == "%" {
			cond += fmt.Sprintf(" && %s.as.i > 0", r)
		}
		// the location only matters when u_binop might fail, so only the
		// slow path sets it
		line, col := ast.Position(e)
		f.line("UValue %s = (%s) ? %s : (U_LOC(u_file_%d, %d, %d), u_binop(%s, %s, %s));",
			t, cond, fmt.Sprintf(fast, l, r), f.mod.ID, line, col, op, l, r)
		return t
	}
	f.loc(e)
	f.line("UValue %s = u_binop(%s, %s, %s);", t, op, l, r)
	return t
}

// foldInt works out integer arithmetic on literals at compile time, with the
// same wrap-around as the runtime. division is left alone so division by zero
// still fails where it's written
func foldInt(e ast.Expression) (int64, bool) {
	switch e := e.(type) {
	case *ast.IntegerLiteral:
		return e.Value, true
	case *ast.PrefixExpression:
		if v, ok := foldInt(e.Right); ok && e.Operator == "-" {
			return -v, true
		}
	case *ast.InfixExpression:
		l, lok := foldInt(e.Left)
		r, rok := foldInt(e.Right)
		if !lok || !rok {
			return 0, false
		}
		switch e.Operator {
		case "+":
			return l + r, true
		case "-":
			return l - r, true
		case "*":
			return l * r, true
		}
	}
	return 0, false
}

func foldCompare(e *ast.InfixExpression) (bool, bool) {
	l, lok := foldInt(e.Left)
	r, rok := foldInt(e.Right)
	if !lok || !rok {
		return false, false
	}
	switch e.Operator {
	case "<":
		return l < r, true
	case ">":
		return l > r, true
	case "<=":
		return l <= r, true
	case ">=":
		return l >= r, true
	case "==":
		return l == r, true
	case "!=":
		return l != r, true
	}
	return false, false
}

func intLiteral(v int64) string {
	if v == math.MinInt64 {
		return "uv_int(INT64_MIN)"
	}
	return fmt.Sprintf("uv_int(INT64_C(%d))", v)
}

func (f *fgen) call(node ast.Node, calleeExpr ast.Expression, callee string, args []string) string {
	t := f.temp()
	f.loc(node)
	argv := "NULL"
	if len(args) > 0 {
		argv = "(UValue[]){" + strings.Join(args, ", ") + "}"
	}
	if info := f.directCallee(calleeExpr, len(args)); info != nil {
		// same checks u_call would make, minus the ones the checker already
		// proved: it's a function and the argument count fits
		f.line("if (u_depth >= U_MAX_DEPTH) u_panic(\"maximum call depth exceeded\");")
		f.line("u_depth++;")
		f.line("UValue %s = fn_%d(%s.as.fn->env, %d, %s);", t, info.ID, callee, len(args), argv)
		f.line("u_depth--;")
		return t
	}
	f.line("UValue %s = u_call(%s, %d, %s);", t, callee, len(args), argv)
	return t
}

// directCallee returns the function a call is known to reach, if any. that's
// a plain name bound once to a function literal with nothing else it could
// resolve to
func (f *fgen) directCallee(e ast.Expression, argc int) *semantic.FuncInfo {
	id, ok := e.(*ast.Identifier)
	if !ok {
		return nil
	}
	ref := f.mod.Refs[id]
	if ref == nil || len(ref.Candidates) != 1 || ref.Builtin != "" {
		return nil
	}
	lit := ref.Candidates[0].Sym.Func
	if lit == nil || len(lit.Parameters) != argc {
		return nil
	}
	return f.mod.FuncOf[lit]
}

func (f *fgen) matchArms(arms []ast.MatchArm, value, result string) {
	if len(arms) == 0 {
		return
	}
	arm := arms[0]
	_, wildcard := arm.Pattern.(*ast.WildcardPattern)
	if wildcard && arm.Guard == nil {
		f.line("%s = %s;", result, f.expr(arm.Value))
		return
	}
	ok := f.temp()
	f.line("bool %s = true;", ok)
	f.pattern(arm.Pattern, value, ok)
	if arm.Guard != nil {
		f.line("if (%s) {", ok)
		f.indent++
		g := f.expr(arm.Guard)
		f.line("%s = u_truthy(%s);", ok, g)
		f.indent--
		f.line("}")
	}
	f.line("if (%s) {", ok)
	f.indent++
	f.line("%s = %s;", result, f.expr(arm.Value))
	f.indent--
	f.line("} else {")
	f.indent++
	f.matchArms(arms[1:], value, result)
	f.indent--
	f.line("}")
}

// pattern emits code that clears ok when value doesn't match p. it checks
// parts left to right and stops at the first mismatch like the interpreter,
// bindings are written to their slots as they match
func (f *fgen) pattern(p ast.Pattern, value, ok string) {
	switch p := p.(type) {
	case *ast.WildcardPattern:
	case *ast.BindingPattern:
		f.line("if (%s) S[%d] = %s;", ok, f.mod.Bindings[p.Name].Slot, value)
	case *ast.ValuePattern:
		f.line("if (%s) {", ok)
		f.indent++
		expected := f.expr(p.Value)
		f.line("%s = u_equal(%s, %s);", ok, value, expected)
		f.indent--
		f.line("}")
	case *ast.ArrayPattern:
		cmp := "=="
		if p.HasRest {
			cmp = ">="
		}
		f.line("if (%s) %s = %s.tag == U_ARR && %s.as.a->len %s %d;", ok, ok, value, value, cmp, len(p.Elements))
		for i, el := range p.Elements {
			f.line("if (%s) {", ok)
			f.indent++
			item := f.temp()
			f.line("UValue %s = %s.as.a->items[%d];", item, value, i)
			f.pattern(el, item, ok)
			f.indent--
			f.line("}")
		}
		if p.Rest != nil {
			f.line("if (%s) S[%d] = uv_arrv(u_arr_tail(%s.as.a, %d));", ok, f.mod.Bindings[p.Rest].Slot, value, len(p.Elements))
		}
	case *ast.ResultPattern:
		f.line("if (%s) %s = %s.tag == U_RES && %s.as.r->ok == %t;", ok, ok, value, value, p.Ok)
		f.line("if (%s) {", ok)
		f.indent++
		item := f.temp()
		f.line("UValue %s = %s.as.r->v;", item, value)
		f.pattern(p.Inner, item, ok)
		f.indent--
		f.line("}")
	case *ast.MapPattern:
		f.line("if (%s) %s = %s.tag == U_MAP;", ok, ok, value)
		for i, keyExpr := range p.Keys {
			f.line("if (%s) {", ok)
			f.indent++
			key := f.expr(keyExpr)
			f.loc(p)
			f.line("u_map_literal_key(%s);", key)
			idx := f.temp()
			f.line("int64_t %s = u_map_find(%s.as.m, %s);", idx, value, key)
			f.line("%s = %s >= 0;", ok, idx)
			f.line("if (%s) {", ok)
			f.indent++
			item := f.temp()
			f.line("UValue %s = %s.as.m->entries[%s].value;", item, value, idx)
			f.pattern(p.Values[i], item, ok)
			f.indent--
			f.line("}")
			f.indent--
			f.line("}")
		}
	default:
		panic(fmt.Sprintf("cgen: unsupported pattern %T", p))
	}
}

// functionInspect does the same thing as object.Function.Inspect for a
// literal
func functionInspect(lit *ast.FunctionLiteral) string {
	var out strings.Builder
	out.WriteString("fn")
	if lit.Name != "" {
		out.WriteString(" " + lit.Name)
	}
	out.WriteString(ast.Signature(lit.Parameters, lit.ParamTypes, lit.ReturnType) + " {\n")
	out.WriteString(lit.Body.String())
	out.WriteString("\n}")
	return out.String()
}

// cString renders s as a C string literal with octal escapes, \x escapes
// would eat the digits that follow
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
