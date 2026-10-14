// Package semantic resolves names, checks scopes and runs a conservative type
// check over Unda programs. `unda check` and the C backend both use its
// output, and the backend depends on the slot layout computed here
//
// name resolution follows the interpreter's dynamic rules. function bodies
// get resolved only after the whole enclosing module is declared because the
// interpreter looks names up at call time. statements at the same function
// level are resolved in order, so using a name directly before its `let` gets
// reported
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
	// Native turns on checks for features the C backend doesn't support
	Native bool
}

// Symbol is one binding slot in a function frame. redeclaring a name in the
// same scope reuses the slot, same as the interpreter's Define
type Symbol struct {
	Name    string
	Slot    int
	Level   int
	Mutable bool
	Type    Type
	// Annot is the declared type, assignments get checked against it
	Annot *ast.TypeAnnotation
	// Func is set when the binding can only ever hold this one function
	// literal: declared once, immutable, initialized with the literal. the C
	// backend calls such functions directly
	Func  *ast.FunctionLiteral
	decls int
}

// Candidate is a binding an identifier might refer to, Depth frames up.
// direct candidates are resolved in execution order within the same function
// so they're always initialized when read
type Candidate struct {
	Sym    *Symbol
	Depth  int
	Direct bool
}

// Ref is a resolved identifier with candidates ordered nearest first. a
// reference from a nested function can see a binding declared later in an
// outer scope. at runtime the first initialized candidate wins, then the
// global builtin if there is one, exactly like the interpreter's env chain
type Ref struct {
	Name       string
	Candidates []Candidate
	Builtin    string
}

// FuncInfo describes one frame, which is a function literal, a module's top
// level or a loop body that creates closures (then each iteration gets its
// own frame so closures capture that iteration's bindings)
type FuncInfo struct {
	ID         int
	Lit        *ast.FunctionLiteral
	Parent     *FuncInfo
	Level      int
	NumSlots   int
	ParamSlots []int
	// HeapFrame is set when a closure might capture this frame
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
	// Bindings maps identifiers introduced by for loops and match patterns
	Bindings map[*ast.Identifier]*Symbol
	// LoopFrames holds the per-iteration frame for loops whose body creates
	// closures
	LoopFrames map[ast.Statement]*FuncInfo
	Imports    map[*ast.ImportStatement]*Import
	Exports    []*Symbol
	Type       *ModuleType
}

type Program struct {
	Main *Module
	// Modules lists every loaded module, dependencies before dependents
	Modules     []*Module
	Diagnostics []diag.Diagnostic
	// Libraries are the C compiler arguments that link what extern blocks
	// use, like "-lm" or the absolute path of a shared library
	Libraries []string
	sources   map[string]string
}

// Source returns the text of the file a diagnostic points at
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

// AnalyzeSource analyzes source text, imports resolve against dir
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
	// structs lists every struct declared in the modules loaded so far, by
	// name. types refer to structs by name, so two with the same name make it
	// ambiguous and the checker stops guessing
	structs map[string][]*ast.StructStatement
	opts    Options
	prog    *Program
	loading map[string]bool
	loaded  map[string]*Module
	nextFn  int
}

func newAnalyzer(opts Options) *analyzer {
	return &analyzer{
		opts:    opts,
		structs: map[string][]*ast.StructStatement{},
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

// ModuleName does the same default module naming as the interpreter
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
	// boundary marks a function's parameter scope. names found past it are
	// read when the function runs, not in statement order
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
	// fn is the function literal whose body is being checked, nil at the top
	// level
	fn *ast.FunctionLiteral
}

func (c *checker) run() {
	top := c.newFunc(nil, nil)
	c.mod.Top = top
	sc := newScope(nil, top)
	walk(c.mod.Program, func(n ast.Node) bool {
		if s, ok := n.(*ast.StructStatement); ok {
			c.a.structs[s.Name.Value] = append(c.a.structs[s.Name.Value], s)
		}
		return true
	})
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
	for i, param := range lit.Parameters {
		c.checkAnnot(paramAnnot(lit, i))
		sym := c.declare(params, param.Value, false, unknown(), paramAnnot(lit, i))
		p.fn.ParamSlots = append(p.fn.ParamSlots, sym.Slot)
	}
	outerLoops := c.loops
	c.loops = 0
	c.fn = lit
	c.checkAnnot(lit.ReturnType)
	t := c.block(lit.Body, newScope(params, p.fn))
	if !fits(t, lit.ReturnType) {
		var at ast.Node = lit.Body
		if n := len(lit.Body.Statements); n > 0 {
			at = lit.Body.Statements[n-1]
		}
		c.errorf(at, "type mismatch: %s %s, got %s", returnLabel(lit), lit.ReturnType.Name, t.Name())
	}
	c.fn = nil
	c.loops = outerLoops
}

func paramAnnot(lit *ast.FunctionLiteral, i int) *ast.TypeAnnotation {
	if i < len(lit.ParamTypes) {
		return lit.ParamTypes[i]
	}
	return nil
}

// returnLabel names a function in return type errors, the same way the
// interpreter does
func returnLabel(lit *ast.FunctionLiteral) string {
	if lit.Name != "" {
		return lit.Name + " returns"
	}
	return "function returns"
}

// annotType is what a value of an annotated type is known to be
func (c *checker) annotType(a *ast.TypeAnnotation) Type {
	t := kindType(AnnotationKind(a))
	if a != nil && a.IsStruct() {
		t.Struct = c.lookupStruct(a.Name)
	}
	return t
}

// narrow keeps what we know about t but drops the kinds a rules out, the
// runtime check guarantees the rest
func (c *checker) narrow(t Type, a *ast.TypeAnnotation) Type {
	k := AnnotationKind(a)
	if t.Kind&k == t.Kind {
		return t
	}
	out := kindType(t.Kind & k)
	if out.Kind == KStruct {
		out.Struct = c.lookupStruct(a.Name)
	}
	return out
}

func (c *checker) lookupStruct(name string) *ast.StructStatement {
	if decls := c.a.structs[name]; len(decls) == 1 {
		return decls[0]
	}
	return nil
}

// checkAnnot reports struct types nobody declared
func (c *checker) checkAnnot(a *ast.TypeAnnotation) {
	if a == nil || !a.IsStruct() || len(c.a.structs[a.Name]) > 0 {
		return
	}
	c.errorf(a, "unknown type %s%s", a.Name, diag.DidYouMean(a.Name, sortedKeys(c.a.structs)))
}

func (c *checker) declare(sc *scope, name string, mutable bool, t Type, annot *ast.TypeAnnotation) *Symbol {
	if sym, ok := sc.names[name]; ok {
		sym.decls++
		sym.Func = nil
		sym.Mutable = mutable
		sym.Type = c.annotType(annot)
		sym.Annot = annot
		return sym
	}
	switch {
	case annot != nil && mutable:
		t = c.annotType(annot)
	case annot != nil:
		t = c.narrow(t, annot)
	case mutable:
		t = unknown()
	}
	sym := &Symbol{Name: name, Slot: sc.fn.NumSlots, Level: sc.fn.Level, Mutable: mutable, Type: t, Annot: annot, decls: 1}
	sc.fn.NumSlots++
	sc.names[name] = sym
	return sym
}

// visibleNames lists every name that could be meant at sc, for suggestions.
// builtins only count where they can be read, not assigned to
func visibleNames(sc *scope, withBuiltins bool) []string {
	var names []string
	for s := sc; s != nil; s = s.parent {
		names = append(names, sortedKeys(s.names)...)
	}
	if withBuiltins {
		names = append(names, sortedKeys(GlobalBuiltins)...)
	}
	return names
}

// lookup collects the bindings name might refer to, from sc outward. bindings
// found before crossing a function boundary were declared earlier in
// execution order so they're initialized and the search stops there
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
		c.checkAnnot(s.Type)
		if !fits(t, s.Type) {
			c.errorf(s, "type mismatch: %s expects %s, got %s", s.Name.Value, s.Type.Name, t.Name())
		}
		// a redeclared name shares its slot with the old one, and the native
		// backend checks assignments per slot, so both have to agree on the type
		if old, ok := sc.names[s.Name.Value]; ok && annotName(old.Annot) != annotName(s.Type) {
			c.errorf(s, "cannot redeclare %s with a different type", s.Name.Value)
		}
		sym := c.declare(sc, s.Name.Value, s.Mutable, t, s.Type)
		if lit, ok := s.Value.(*ast.FunctionLiteral); ok && sym.decls == 1 && !s.Mutable {
			sym.Func = lit
		}
		c.mod.Decls[s] = sym
		return t
	case *ast.AssignStatement:
		t := c.expr(s.Value, sc)
		c.assign(s, sc, t)
		return t
	case *ast.ReturnStatement:
		t := c.expr(s.ReturnValue, sc)
		if c.fn != nil && !fits(t, c.fn.ReturnType) {
			c.errorf(s, "type mismatch: %s %s, got %s", returnLabel(c.fn), c.fn.ReturnType.Name, t.Name())
		}
		return kindType(KNever)
	case *ast.ExternStatement:
		c.library(s.Library.Value)
		for _, fn := range s.Funcs {
			params := make([]Kind, len(fn.Types))
			for i, t := range fn.Types {
				params[i] = cTypeKind(t, false)
			}
			sg := sig(fn.Name.Value, len(params), len(params), cTypeKind(fn.Return, true), params...)
			c.mod.Bindings[fn.Name] = c.declare(sc, fn.Name.Value, false, Type{Kind: KBuiltin, Arity: -1, Builtin: sg}, nil)
		}
		return kindType(KNil)
	case *ast.StructStatement:
		for _, typ := range s.Types {
			c.checkAnnot(typ)
		}
		t := Type{Kind: KStructDef, Arity: -1, Struct: s}
		c.mod.Decls[s] = c.declare(sc, s.Name.Value, false, t, nil)
		return t
	case *ast.ImportStatement:
		t := c.importStmt(s)
		alias := ModuleName(s.Path.Value)
		if s.Alias != nil {
			alias = s.Alias.Value
		}
		c.mod.Decls[s] = c.declare(sc, alias, false, t, nil)
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
			c.mod.Bindings[v] = c.declare(vars, v.Value, false, unknown(), nil)
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

// loopScope returns the scope a loop body's bindings live in. if the body
// creates closures it's a fresh frame per iteration
func (c *checker) loopScope(loop ast.Statement, body *ast.BlockStatement, sc *scope) *scope {
	if !containsFunction(body) {
		return newScope(sc, sc.fn)
	}
	frame := c.newFunc(nil, sc.fn)
	frame.HeapFrame = true
	c.mod.LoopFrames[loop] = frame
	return newScope(sc, frame)
}

func annotName(a *ast.TypeAnnotation) string {
	if a == nil {
		return ""
	}
	return a.Name
}

func (c *checker) assign(s *ast.AssignStatement, sc *scope, t Type) {
	name := s.Name.Value
	cands := lookup(sc, name)
	if len(cands) == 0 {
		c.errorf(s, "identifier not found: %s%s", name, diag.DidYouMean(name, visibleNames(sc, false)))
		return
	}
	anyMutable := false
	for _, cand := range cands {
		anyMutable = anyMutable || cand.Sym.Mutable
	}
	if !anyMutable || (cands[0].Direct && !cands[0].Sym.Mutable) {
		c.errorf(s, "cannot assign to immutable binding %q", name)
	} else if len(s.Fields) > 0 {
		if cands[0].Direct {
			c.fieldAssign(s, cands[0].Sym.Type, t)
		}
	} else if cands[0].Direct && !fits(t, cands[0].Sym.Annot) {
		c.errorf(s, "type mismatch: %s expects %s, got %s", name, cands[0].Sym.Annot.Name, t.Name())
	}
	c.mod.Assigns[s] = &Ref{Name: name, Candidates: cands}
}

// fieldAssign checks `x.a.b = v` as far as the types along the path are
// known
func (c *checker) fieldAssign(s *ast.AssignStatement, cur, v Type) {
	for i, f := range s.Fields {
		if cur.Kind != KStruct || cur.Struct == nil {
			if cur.Kind.Known() && cur.Kind != KMap {
				c.errorf(s, "cannot set field %s on %s", f.Value, cur.Name())
			}
			return
		}
		decl := cur.Struct
		idx := fieldIndex(decl, f.Value)
		if idx < 0 {
			c.errorf(s, "%s has no field %q", decl.Name.Value, f.Value)
			return
		}
		if i == len(s.Fields)-1 && !fits(v, decl.Types[idx]) {
			c.errorf(s, "type mismatch: field %s of %s expects %s, got %s", f.Value, decl.Name.Value, decl.Types[idx].Name, v.Name())
		}
		cur = c.annotType(decl.Types[idx])
	}
}

// cTypeKind is the Unda kind a C type takes as an argument or gives back
func cTypeKind(t *ast.TypeAnnotation, result bool) Kind {
	switch {
	case t == nil:
		return KNil
	case t.Name == "f32" || t.Name == "f64":
		if result {
			return KFloat
		}
		return KNumber
	case t.Name == "bool":
		return KBool
	case t.Name == "cstring" && result:
		return KString | KNil
	case t.Name == "cstring":
		return KString
	}
	return KInt
}

// library records what the C compiler needs to link an extern block's
// library. a bare name like "m" becomes -lm, "c" is libc and needs nothing,
// anything that looks like a path is linked as that file
func (c *checker) library(lib string) {
	var arg string
	switch {
	case lib == "c":
		return
	case strings.Contains(lib, "/") || strings.HasSuffix(lib, ".so") || strings.HasSuffix(lib, ".a"):
		arg = lib
		if !filepath.IsAbs(arg) {
			arg = filepath.Join(c.mod.Dir, arg)
		}
	default:
		arg = "-l" + lib
	}
	for _, have := range c.a.prog.Libraries {
		if have == arg {
			return
		}
	}
	c.a.prog.Libraries = append(c.a.prog.Libraries, arg)
}

func fieldIndex(decl *ast.StructStatement, name string) int {
	for i, f := range decl.Fields {
		if f.Value == name {
			return i
		}
	}
	return -1
}

// construct checks a call to a struct constructor, the same checks
// object.StructDef.New makes at runtime
func (c *checker) construct(node ast.Node, decl *ast.StructStatement, args []Type, names []*ast.Identifier) Type {
	out := Type{Kind: KStruct, Arity: -1, Struct: decl}
	if decl == nil {
		return out
	}
	given := make([]*Type, len(decl.Fields))
	if names == nil {
		if len(args) != len(decl.Fields) {
			c.errorf(node, "wrong number of arguments: expected %d, got %d", len(decl.Fields), len(args))
			return out
		}
		for i := range args {
			given[i] = &args[i]
		}
	} else {
		for i, name := range names {
			idx := fieldIndex(decl, name.Value)
			if idx < 0 {
				c.errorf(node, "%s has no field %q%s", decl.Name.Value, name.Value, diag.DidYouMean(name.Value, fieldNames(decl)))
				return out
			}
			if given[idx] != nil {
				c.errorf(node, "field %s given twice", name.Value)
				return out
			}
			given[idx] = &args[i]
		}
		for i, g := range given {
			if g == nil {
				c.errorf(node, "missing field %s for %s", decl.Fields[i].Value, decl.Name.Value)
				return out
			}
		}
	}
	for i, g := range given {
		if !fits(*g, decl.Types[i]) {
			c.errorf(node, "type mismatch: field %s of %s expects %s, got %s", decl.Fields[i].Value, decl.Name.Value, decl.Types[i].Name, g.Name())
			break
		}
	}
	return out
}

func fieldNames(decl *ast.StructStatement) []string {
	names := make([]string, len(decl.Fields))
	for i, f := range decl.Fields {
		names[i] = f.Value
	}
	return names
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
			hint := ""
			if !strings.ContainsAny(path, "/.") {
				if hint = diag.DidYouMean(path, sortedKeys(StdModules)); hint != "" {
					hint = strings.Replace(hint, "did you mean", "did you mean the standard module", 1)
				}
			}
			c.errorf(s, "import %q failed: %v%s", path, err, hint)
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
		return Type{Kind: KFunction, Arity: len(e.Parameters), Func: e}
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
	case *ast.TryExpression:
		t := c.expr(e.Value, sc)
		if t.Kind != KNever && t.Kind&KResult == 0 {
			c.errorf(e, "? expects RESULT, got %s", t.Name())
		}
		if c.fn != nil && c.fn.ReturnType != nil && AnnotationKind(c.fn.ReturnType)&KResult == 0 {
			c.errorf(e, "type mismatch: %s %s, got RESULT", returnLabel(c.fn), c.fn.ReturnType.Name)
		}
		return unknown()
	}
	return unknown()
}

// pattern resolves a match pattern and declares its bindings in sc
func (c *checker) pattern(p ast.Pattern, sc *scope) {
	bind := func(id *ast.Identifier) {
		if _, dup := sc.names[id.Value]; dup {
			c.errorf(id, "duplicate binding %s in pattern", id.Value)
		}
		c.mod.Bindings[id] = c.declare(sc, id.Value, false, unknown(), nil)
	}
	switch p := p.(type) {
	case *ast.BindingPattern:
		bind(p.Name)
	case *ast.ValuePattern:
		c.expr(p.Value, sc)
	case *ast.ResultPattern:
		c.pattern(p.Inner, sc)
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
		c.errorf(id, "identifier not found: %s%s", id.Value, diag.DidYouMean(id.Value, visibleNames(sc, true)))
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
	var names []*ast.Identifier
	if ce, ok := node.(*ast.CallExpression); ok {
		names = ce.ArgNames
	}
	if !callee.Kind.Known() {
		return unknown()
	}
	if names != nil && callee.Kind != KStructDef {
		c.errorf(node, "named arguments only work with struct constructors, got %s", callee.Name())
		return unknown()
	}
	switch callee.Kind {
	case KStructDef:
		return c.construct(node, callee.Struct, args, names)
	case KFunction:
		if callee.Arity >= 0 && callee.Arity != len(args) {
			c.errorf(node, "wrong number of arguments: expected %d, got %d", callee.Arity, len(args))
			return unknown()
		}
		lit := callee.Func
		if lit == nil {
			return unknown()
		}
		for i, arg := range args {
			if a := paramAnnot(lit, i); !fits(arg, a) {
				c.errorf(node, "type mismatch: parameter %s expects %s, got %s", lit.Parameters[i].Value, a.Name, arg.Name())
				break
			}
		}
		if lit.ReturnType == nil {
			return unknown()
		}
		return c.annotType(lit.ReturnType)
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
	if obj.Kind == KStruct {
		decl := obj.Struct
		if decl == nil {
			return unknown()
		}
		idx := fieldIndex(decl, e.Property.Value)
		if idx < 0 {
			c.errorf(e, "%s has no field %q%s", decl.Name.Value, e.Property.Value, diag.DidYouMean(e.Property.Value, fieldNames(decl)))
			return unknown()
		}
		return c.annotType(decl.Types[idx])
	}
	if !obj.Kind.Known() {
		return unknown()
	}
	if obj.Kind == KMap {
		return unknown()
	}
	if obj.Kind != KModule {
		c.errorf(e, "member access expects module, map or struct, got %s", obj.Kind)
		return unknown()
	}
	if obj.Module == nil {
		return unknown()
	}
	t, ok := obj.Module.Members[e.Property.Value]
	if !ok {
		hint := diag.DidYouMean(e.Property.Value, sortedKeys(obj.Module.Members))
		c.errorf(e, "module %q has no member %q%s", obj.Module.Name, e.Property.Value, hint)
		return unknown()
	}
	if c.a.opts.Native && t.Builtin != nil && t.Builtin.NativeUnsupported {
		c.errorf(e, "%s is not supported by the C backend yet", t.Builtin.Name)
	}
	return t
}
