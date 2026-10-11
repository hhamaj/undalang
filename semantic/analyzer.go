// Package semantic resolves names, validates scopes, and runs a conservative
// type check over Unda programs. Its output drives both `unda check` and the
// C backend, which relies on the slot layout computed here.
//
// Name resolution follows the interpreter's dynamic rules: function bodies
// are resolved only after the enclosing module has been fully declared,
// because the interpreter looks names up at call time. Statements at the
// same function level are resolved in order, so a direct use before a `let`
// is reported.
package semantic

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"undalang/ast"
	"undalang/diag"
	"undalang/lexer"
	"undalang/parser"
)

type Options struct {
	// Native enables checks for features the C backend does not support.
	Native bool
}

// Symbol is one binding slot in a function frame. Redeclaring a name in the
// same scope reuses the slot, matching the interpreter's Define.
type Symbol struct {
	Name    string
	Slot    int
	Level   int
	Mutable bool
	Type    Type
	decls   int
}

// Candidate is a binding an identifier may refer to, Depth frames up.
// Direct candidates are resolved in execution order within the same
// function, so they are always initialized when read.
type Candidate struct {
	Sym    *Symbol
	Depth  int
	Direct bool
}

// Ref is a resolved identifier. Candidates are ordered nearest first. A
// reference from a nested function may see a binding that is declared later
// in an outer scope; at runtime the first initialized candidate wins, then
// the global builtin (if any), exactly like the interpreter's env chain.
type Ref struct {
	Name       string
	Candidates []Candidate
	Builtin    string
}

// FuncInfo describes one frame: a function literal, a module's top level,
// or a loop body that creates closures (each iteration then gets its own
// frame so closures capture that iteration's bindings).
type FuncInfo struct {
	ID         int
	Lit        *ast.FunctionLiteral
	Parent     *FuncInfo
	Level      int
	NumSlots   int
	ParamSlots []int
	// HeapFrame is set when a closure may capture this frame.
	HeapFrame bool
}

func (fn *FuncInfo) markHeap() {
	for f := fn; f != nil && !f.HeapFrame; f = f.Parent {
		f.HeapFrame = true
	}
}

type Import struct {
	Std    string
	Module *Module
}

type Module struct {
	ID      int
	Path    string
	File    string
	Dir     string
	Name    string
	Source  string
	Program *ast.Program
	Top     *FuncInfo
	Funcs   []*FuncInfo
	FuncOf  map[*ast.FunctionLiteral]*FuncInfo
	Refs    map[*ast.Identifier]*Ref
	Assigns map[*ast.AssignStatement]*Ref
	Decls   map[ast.Statement]*Symbol
	// Bindings maps identifiers introduced by for loops and match patterns.
	Bindings map[*ast.Identifier]*Symbol
	// LoopFrames holds the per-iteration frame of loops whose body creates
	// closures.
	LoopFrames map[ast.Statement]*FuncInfo
	Imports    map[*ast.ImportStatement]*Import
	Exports    []*Symbol
	Type       *ModuleType
}

type Program struct {
	Main *Module
	// Modules lists every loaded module with dependencies before dependents.
	Modules     []*Module
	Diagnostics []diag.Diagnostic
	sources     map[string]string
}

// Source returns the text of the file a diagnostic refers to.
func (p *Program) Source(file string) string { return p.sources[file] }

func (p *Program) HasErrors() bool { return len(p.Diagnostics) > 0 }

func AnalyzeFile(path string, opts Options) *Program {
	a := newAnalyzer(opts)
	abs, err := filepath.Abs(path)
	if err != nil {
		a.prog.Diagnostics = append(a.prog.Diagnostics, diag.Diagnostic{File: path, Message: err.Error()})
		return a.prog
	}
	input, err := os.ReadFile(abs)
	if err != nil {
		a.prog.Diagnostics = append(a.prog.Diagnostics, diag.Diagnostic{File: path, Message: fmt.Sprintf("could not read %s: %v", path, err)})
		return a.prog
	}
	a.prog.Main = a.load(abs, diag.DisplayPath(abs), string(input))
	return a.prog
}

// AnalyzeSource analyzes source text whose imports resolve against dir.
func AnalyzeSource(source, file, dir string, opts Options) *Program {
	a := newAnalyzer(opts)
	if dir == "" {
		dir, _ = os.Getwd()
	}
	path := ""
	if file != "" {
		path, _ = filepath.Abs(file)
	}
	a.prog.Main = a.loadSource(path, file, dir, source)
	return a.prog
}

type analyzer struct {
	opts    Options
	prog    *Program
	loading map[string]bool
	loaded  map[string]*Module
	nextFn  int
}

func newAnalyzer(opts Options) *analyzer {
	return &analyzer{
		opts:    opts,
		prog:    &Program{sources: map[string]string{}},
		loading: map[string]bool{},
		loaded:  map[string]*Module{},
	}
}

func (a *analyzer) load(path, file, source string) *Module {
	return a.loadSource(path, file, filepath.Dir(path), source)
}

func (a *analyzer) loadSource(path, file, dir, source string) *Module {
	a.prog.sources[file] = source
	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		for _, msg := range p.Errors() {
			a.prog.Diagnostics = append(a.prog.Diagnostics, diag.FromParserError(file, msg))
		}
		return nil
	}
	mod := &Module{
		ID:      len(a.prog.Modules),
		Path:    path,
		File:    file,
		Dir:     dir,
		Name:    ModuleName(path),
		Source:  source,
		Program: program,
		FuncOf:  map[*ast.FunctionLiteral]*FuncInfo{},
		Refs:    map[*ast.Identifier]*Ref{},
		Assigns: map[*ast.AssignStatement]*Ref{},
		Decls:   map[ast.Statement]*Symbol{},
		Imports: map[*ast.ImportStatement]*Import{},

		Bindings:   map[*ast.Identifier]*Symbol{},
		LoopFrames: map[ast.Statement]*FuncInfo{},
	}
	if path != "" {
		a.loading[path] = true
		defer delete(a.loading, path)
	}
	c := &checker{a: a, mod: mod}
	c.run()
	mod.ID = len(a.prog.Modules)
	a.prog.Modules = append(a.prog.Modules, mod)
	if path != "" {
		a.loaded[path] = mod
	}
	return mod
}

// ModuleName mirrors the interpreter's default module naming.
func ModuleName(path string) string {
	path = strings.TrimSuffix(path, ".unda")
	path = filepath.Base(path)
	path = strings.Trim(path, `"`)
	if path == "" || path == "." {
		return "module"
	}
	return path
}

type scope struct {
	parent *scope
	names  map[string]*Symbol
	fn     *FuncInfo
	// boundary marks a function's parameter scope; names found beyond it are
	// read when the function runs, not in statement order.
	boundary bool
}

func newScope(parent *scope, fn *FuncInfo) *scope {
	return &scope{parent: parent, names: map[string]*Symbol{}, fn: fn}
}

type pending struct {
	fn    *FuncInfo
	scope *scope
}

type checker struct {
	a     *analyzer
	mod   *Module
	queue []pending
	diags []diag.Diagnostic
	loops int
}

func (c *checker) run() {
	top := c.newFunc(nil, nil)
	c.mod.Top = top
	sc := newScope(nil, top)
	for _, stmt := range c.mod.Program.Statements {
		c.stmt(stmt, sc)
	}
	for len(c.queue) > 0 {
		p := c.queue[0]
		c.queue = c.queue[1:]
		c.function(p)
	}
	members := map[string]Type{}
	for _, name := range sortedKeys(sc.names) {
		sym := sc.names[name]
		c.mod.Exports = append(c.mod.Exports, sym)
		members[name] = sym.Type
	}
	c.mod.Type = &ModuleType{Name: c.mod.Name, Members: members}

	sort.SliceStable(c.diags, func(i, j int) bool {
		if c.diags[i].Line != c.diags[j].Line {
			return c.diags[i].Line < c.diags[j].Line
		}
		return c.diags[i].Column < c.diags[j].Column
	})
	c.a.prog.Diagnostics = append(c.a.prog.Diagnostics, c.diags...)
}

func (c *checker) newFunc(lit *ast.FunctionLiteral, parent *FuncInfo) *FuncInfo {
	level := 0
	if parent != nil {
		level = parent.Level + 1
	}
	fn := &FuncInfo{ID: c.a.nextFn, Lit: lit, Parent: parent, Level: level}
	c.a.nextFn++
	if lit != nil {
		c.mod.Funcs = append(c.mod.Funcs, fn)
		c.mod.FuncOf[lit] = fn
	}
	return fn
}

func (c *checker) errorf(node ast.Node, format string, args ...interface{}) {
	line, col := ast.Position(node)
	c.diags = append(c.diags, diag.Diagnostic{File: c.mod.File, Line: line, Column: col, Message: fmt.Sprintf(format, args...)})
}

func (c *checker) function(p pending) {
	lit := p.fn.Lit
	params := newScope(p.scope, p.fn)
	params.boundary = true
	for _, param := range lit.Parameters {
		sym := c.declare(params, param.Value, false, unknown())
		p.fn.ParamSlots = append(p.fn.ParamSlots, sym.Slot)
	}
	outerLoops := c.loops
	c.loops = 0
	c.block(lit.Body, newScope(params, p.fn))
	c.loops = outerLoops
}

func (c *checker) declare(sc *scope, name string, mutable bool, t Type) *Symbol {
	if sym, ok := sc.names[name]; ok {
		sym.decls++
		sym.Mutable = mutable
		sym.Type = unknown()
		return sym
	}
	if mutable {
		t = unknown()
	}
	sym := &Symbol{Name: name, Slot: sc.fn.NumSlots, Level: sc.fn.Level, Mutable: mutable, Type: t, decls: 1}
	sc.fn.NumSlots++
	sc.names[name] = sym
	return sym
}

// lookup collects the bindings name may refer to from sc outward. Bindings
// found before crossing a function boundary were declared earlier in
// execution order, so they are initialized and the search stops there.
func lookup(sc *scope, name string) []Candidate {
	var out []Candidate
	level := sc.fn.Level
	crossed := false
	for s := sc; s != nil; s = s.parent {
		if sym, ok := s.names[name]; ok {
			out = append(out, Candidate{Sym: sym, Depth: level - sym.Level, Direct: !crossed})
			if !crossed {
				break
			}
		}
		if s.boundary {
			crossed = true
		}
	}
	return out
}

func (c *checker) block(b *ast.BlockStatement, sc *scope) Type {
	result := kindType(KNil)
	for _, stmt := range b.Statements {
		result = c.stmt(stmt, sc)
	}
	return result
}

func (c *checker) stmt(s ast.Statement, sc *scope) Type {
	switch s := s.(type) {
	case *ast.LetStatement:
		t := c.expr(s.Value, sc)
		c.mod.Decls[s] = c.declare(sc, s.Name.Value, s.Mutable, t)
		return t
	case *ast.AssignStatement:
		t := c.expr(s.Value, sc)
		c.assign(s, sc)
		return t
	case *ast.ReturnStatement:
		c.expr(s.ReturnValue, sc)
		return kindType(KNever)
	case *ast.ImportStatement:
		t := c.importStmt(s)
		alias := ModuleName(s.Path.Value)
		if s.Alias != nil {
			alias = s.Alias.Value
		}
		c.mod.Decls[s] = c.declare(sc, alias, false, t)
		return t
	case *ast.ExpressionStatement:
		return c.expr(s.Expression, sc)
	case *ast.WhileStatement:
		c.expr(s.Condition, sc)
		body := c.loopScope(s, s.Body, sc)
		c.loops++
		c.block(s.Body, body)
		c.loops--
		return kindType(KNil)
	case *ast.ForStatement:
		t := c.expr(s.Iterable, sc)
		if t.Kind.Known() && t.Kind&(KArray|KMap|KString) == 0 {
			c.errorf(s, "cannot iterate over %s", t.Kind)
		}
		vars := c.loopScope(s, s.Body, sc)
		for _, v := range s.Vars {
			if _, dup := vars.names[v.Value]; dup {
				c.errorf(v, "duplicate loop variable %s", v.Value)
			}
			c.mod.Bindings[v] = c.declare(vars, v.Value, false, unknown())
		}
		c.loops++
		c.block(s.Body, newScope(vars, vars.fn))
		c.loops--
		return kindType(KNil)
	case *ast.BreakStatement:
		if c.loops == 0 {
			c.errorf(s, "break outside loop")
		}
		return kindType(KNever)
	case *ast.ContinueStatement:
		if c.loops == 0 {
			c.errorf(s, "continue outside loop")
		}
		return kindType(KNever)
	case *ast.BlockStatement:
		return c.block(s, newScope(sc, sc.fn))
	}
	return unknown()
}

// loopScope returns the scope a loop body's bindings live in. When the body
// creates closures, it is a fresh frame per iteration.
func (c *checker) loopScope(loop ast.Statement, body *ast.BlockStatement, sc *scope) *scope {
	if !containsFunction(body) {
		return newScope(sc, sc.fn)
	}
	frame := c.newFunc(nil, sc.fn)
	frame.HeapFrame = true
	c.mod.LoopFrames[loop] = frame
	return newScope(sc, frame)
}

func (c *checker) assign(s *ast.AssignStatement, sc *scope) {
	name := s.Name.Value
	cands := lookup(sc, name)
	if len(cands) == 0 {
		c.errorf(s, "identifier not found: %s", name)
		return
	}
	anyMutable := false
	for _, cand := range cands {
		anyMutable = anyMutable || cand.Sym.Mutable
	}
	if !anyMutable || (cands[0].Direct && !cands[0].Sym.Mutable) {
		c.errorf(s, "cannot assign to immutable binding %q", name)
	}
	c.mod.Assigns[s] = &Ref{Name: name, Candidates: cands}
}

func (c *checker) importStmt(s *ast.ImportStatement) Type {
	path := s.Path.Value
	if _, ok := StdModules[path]; ok {
		c.mod.Imports[s] = &Import{Std: path}
		return Type{Kind: KModule, Arity: -1, Module: stdModuleType(path)}
	}
	full := path
	if !strings.HasSuffix(full, ".unda") {
		full += ".unda"
	}
	if !filepath.IsAbs(full) {
		full = filepath.Join(c.mod.Dir, full)
	}
	abs, err := filepath.Abs(full)
	if err != nil {
		c.errorf(s, "import %q failed: %v", path, err)
		return unknown()
	}
	if c.a.loading[abs] {
		c.errorf(s, "import cycle detected at %q", path)
		return unknown()
	}
	mod, ok := c.a.loaded[abs]
	if !ok {
		input, err := os.ReadFile(abs)
		if err != nil {
			c.errorf(s, "import %q failed: %v", path, err)
			return unknown()
		}
		mod = c.a.load(abs, diag.DisplayPath(abs), string(input))
		if mod == nil {
			return unknown()
		}
	}
	c.mod.Imports[s] = &Import{Module: mod}
	return Type{Kind: KModule, Arity: -1, Module: mod.Type}
}

func (c *checker) expr(e ast.Expression, sc *scope) Type {
	switch e := e.(type) {
	case *ast.IntegerLiteral:
		return kindType(KInt)
	case *ast.FloatLiteral:
		return kindType(KFloat)
	case *ast.StringLiteral:
		return kindType(KString)
	case *ast.Boolean:
		return kindType(KBool)
	case *ast.NilLiteral:
		return kindType(KNil)
	case *ast.Identifier:
		return c.identifier(e, sc)
	case *ast.PrefixExpression:
		t := c.expr(e.Right, sc)
		if e.Operator == "!" {
			return kindType(KBool)
		}
		if t.Kind.Known() && t.Kind&KNumber == 0 {
			c.errorf(e, "unknown operator: -%s", t.Kind)
			return unknown()
		}
		if t.Kind.Known() {
			return kindType(t.Kind)
		}
		return unknown()
	case *ast.InfixExpression:
		l := c.expr(e.Left, sc)
		r := c.expr(e.Right, sc)
		if l.Kind.Known() && r.Kind.Known() {
			k, msg := infixResult(e.Operator, l.Kind, r.Kind)
			if msg != "" {
				c.errorf(e, "%s", msg)
				return unknown()
			}
			return kindType(k)
		}
		switch e.Operator {
		case "==", "!=", "&&", "||":
			return kindType(KBool)
		}
		return unknown()
	case *ast.PipelineExpression:
		left := c.expr(e.Left, sc)
		if call, ok := e.Right.(*ast.CallExpression); ok {
			callee := c.expr(call.Function, sc)
			args := append([]Type{left}, c.exprs(call.Arguments, sc)...)
			return c.call(e, callee, args)
		}
		callee := c.expr(e.Right, sc)
		return c.call(e, callee, []Type{left})
	case *ast.IfExpression:
		c.expr(e.Condition, sc)
		t := c.block(e.Consequence, newScope(sc, sc.fn))
		if e.Alternative != nil {
			return join(t, c.block(e.Alternative, newScope(sc, sc.fn)))
		}
		return join(t, kindType(KNil))
	case *ast.FunctionLiteral:
		fn := c.newFunc(e, sc.fn)
		sc.fn.markHeap()
		c.queue = append(c.queue, pending{fn: fn, scope: sc})
		return Type{Kind: KFunction, Arity: len(e.Parameters)}
	case *ast.ArrayLiteral:
		c.exprs(e.Elements, sc)
		return kindType(KArray)
	case *ast.MapLiteral:
		for _, pair := range e.Pairs {
			k := c.expr(pair.Key, sc)
			if k.Kind.Known() && k.Kind&KHash == 0 {
				c.errorf(e, "unusable as map key: %s", k.Kind)
			}
			c.expr(pair.Value, sc)
		}
		return kindType(KMap)
	case *ast.IndexExpression:
		return c.index(e, c.expr(e.Left, sc), c.expr(e.Index, sc))
	case *ast.MemberExpression:
		return c.member(e, c.expr(e.Object, sc))
	case *ast.MatchExpression:
		c.expr(e.Value, sc)
		result := kindType(KNever)
		exhaustive := false
		for _, arm := range e.Arms {
			armScope := newScope(sc, sc.fn)
			c.pattern(arm.Pattern, armScope)
			if arm.Guard != nil {
				c.expr(arm.Guard, armScope)
			} else if irrefutable(arm.Pattern) {
				exhaustive = true
			}
			result = join(result, c.expr(arm.Value, armScope))
		}
		if !exhaustive {
			result = join(result, kindType(KNil))
		}
		return result
	case *ast.CallExpression:
		callee := c.expr(e.Function, sc)
		return c.call(e, callee, c.exprs(e.Arguments, sc))
	}
	return unknown()
}

// pattern resolves a match pattern, declaring its bindings in sc.
func (c *checker) pattern(p ast.Pattern, sc *scope) {
	bind := func(id *ast.Identifier) {
		if _, dup := sc.names[id.Value]; dup {
			c.errorf(id, "duplicate binding %s in pattern", id.Value)
		}
		c.mod.Bindings[id] = c.declare(sc, id.Value, false, unknown())
	}
	switch p := p.(type) {
	case *ast.BindingPattern:
		bind(p.Name)
	case *ast.ValuePattern:
		c.expr(p.Value, sc)
	case *ast.ArrayPattern:
		for _, el := range p.Elements {
			c.pattern(el, sc)
		}
		if p.Rest != nil {
			bind(p.Rest)
		}
	case *ast.MapPattern:
		for i, key := range p.Keys {
			k := c.expr(key, sc)
			if k.Kind.Known() && k.Kind&KHash == 0 {
				c.errorf(p, "unusable as map key: %s", k.Kind)
			}
			c.pattern(p.Values[i], sc)
		}
	}
}

func irrefutable(p ast.Pattern) bool {
	switch p.(type) {
	case *ast.WildcardPattern, *ast.BindingPattern:
		return true
	}
	return false
}

func (c *checker) exprs(list []ast.Expression, sc *scope) []Type {
	out := make([]Type, 0, len(list))
	for _, e := range list {
		out = append(out, c.expr(e, sc))
	}
	return out
}

func (c *checker) identifier(id *ast.Identifier, sc *scope) Type {
	ref := &Ref{Name: id.Value, Candidates: lookup(sc, id.Value)}
	direct := len(ref.Candidates) > 0 && ref.Candidates[len(ref.Candidates)-1].Direct
	if _, ok := GlobalBuiltins[id.Value]; ok && !direct {
		ref.Builtin = id.Value
	}
	if len(ref.Candidates) == 0 && ref.Builtin == "" {
		c.errorf(id, "identifier not found: %s", id.Value)
		return unknown()
	}
	c.mod.Refs[id] = ref
	t := kindType(KNever)
	for _, cand := range ref.Candidates {
		t = join(t, cand.Sym.Type)
	}
	if ref.Builtin != "" {
		t = join(t, Type{Kind: KBuiltin, Arity: -1, Builtin: GlobalBuiltins[ref.Builtin]})
	}
	return t
}

func (c *checker) call(node ast.Node, callee Type, args []Type) Type {
	if !callee.Kind.Known() {
		return unknown()
	}
	switch callee.Kind {
	case KFunction:
		if callee.Arity >= 0 && callee.Arity != len(args) {
			c.errorf(node, "wrong number of arguments: expected %d, got %d", callee.Arity, len(args))
		}
		return unknown()
	case KBuiltin:
		s := callee.Builtin
		if s == nil {
			return unknown()
		}
		if len(args) < s.MinArgs || (s.MaxArgs >= 0 && len(args) > s.MaxArgs) {
			c.errorf(node, "%s", s.arityMessage(len(args)))
			return kindType(s.Returns)
		}
		for i, arg := range args {
			if i >= len(s.Params) {
				break
			}
			if arg.Kind.Known() && arg.Kind&s.Params[i] == 0 {
				c.errorf(node, "%s argument %d must be %s, got %s", s.Name, i+1, s.Params[i], arg.Kind)
			}
		}
		if s.Returns == KUnknown {
			return unknown()
		}
		return kindType(s.Returns)
	default:
		c.errorf(node, "not a function: %s", callee.Kind)
		return unknown()
	}
}

func (c *checker) index(e *ast.IndexExpression, left, idx Type) Type {
	if !left.Kind.Known() {
		return unknown()
	}
	switch left.Kind {
	case KArray:
		if idx.Kind.Known() && idx.Kind != KInt {
			c.errorf(e, "index operator not supported: ARRAY[%s]", idx.Kind)
		}
	case KMap:
		if idx.Kind.Known() && idx.Kind&KHash == 0 {
			c.errorf(e, "unusable as map key: %s", idx.Kind)
		}
	case KString:
		if idx.Kind.Known() && idx.Kind != KInt {
			c.errorf(e, "index operator not supported: STRING[%s]", idx.Kind)
		}
		return kindType(KString | KNil)
	default:
		if idx.Kind.Known() {
			c.errorf(e, "index operator not supported: %s[%s]", left.Kind, idx.Kind)
		} else {
			c.errorf(e, "index operator not supported on %s", left.Kind)
		}
	}
	return unknown()
}

func (c *checker) member(e *ast.MemberExpression, obj Type) Type {
	if !obj.Kind.Known() {
		return unknown()
	}
	if obj.Kind != KModule {
		c.errorf(e, "member access expects module, got %s", obj.Kind)
		return unknown()
	}
	if obj.Module == nil {
		return unknown()
	}
	t, ok := obj.Module.Members[e.Property.Value]
	if !ok {
		c.errorf(e, "module %q has no member %q", obj.Module.Name, e.Property.Value)
		return unknown()
	}
	if c.a.opts.Native && t.Builtin != nil && t.Builtin.NativeUnsupported {
		c.errorf(e, "%s is not supported by the C backend yet", t.Builtin.Name)
	}
	return t
}
