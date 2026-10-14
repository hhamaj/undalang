// Package vm runs Unda programs as bytecode. it compiles the analyzed
// program the C backend compiles, with the same frames and slots, and runs
// it on a stack machine. operators, builtins and type checks are the
// interpreter's own Go code, so the three backends agree by construction
package vm

import (
	"fmt"

	"undalang/ast"
	"undalang/evaluator"
	"undalang/object"
	"undalang/semantic"
)

type Op uint8

const (
	opConst     Op = iota // push consts[A]
	opNil                 // push nil
	opPop                 // drop the top
	opDup                 // push the top again
	opLoad                // push slot B of the frame A levels up
	opStore               // set slot B of the frame A levels up to the top, keep it
	opLoadRef             // push what refs[A] resolves to at runtime
	opAssign              // assign the top through refs[A], keep it
	opCheck               // the top has to fit checks[A]
	opBinary              // pop two, push names[A] applied to them, B is the intOps code
	opNot                 // !top
	opNeg                 // -top
	opAndJump             // a falsy top becomes false and jumps to A
	opOrJump              // a truthy top becomes true and jumps to A
	opJump                // jump to A
	opJumpFalse           // pop, jump to A when falsy
	opArray               // pop A values into an array
	opMapKey              // the top has to be usable as a map key
	opMap                 // pop A key value pairs into a map
	opIndex               // pop index and value, push value[index]
	opMember              // pop a value, push its member names[A]
	opCall                // call with A arguments
	opCallNamed           // call with A arguments named by argNames[B]
	opClosure             // push a closure over protos[A]
	opStruct              // push the struct type of structs[A]
	opImport              // push the module imports[A] names
	opExtern              // fail, extern needs the native backend
	opReturn              // return the top from the running proto
	opTry                 // unwrap an ok or return an err, tries[A]
	opPushFrame           // enter a fresh frame with A slots
	opPopFrame            // back to the parent frame
	opLoopEnter           // remember the stack and frame for break/continue
	opLoopExit            // forget them
	opBreak               // unwind to the loop and jump to A
	opContinue            // same, jumping to A for the next round
	opIterInit            // pop an iterable, push an iterator binding A vars
	opIterNext            // push the next A items or jump to B when done
	opMatch               // pop a value, match patterns[A], jump to B on a miss
	opArmEnd              // pop the arm's value and the matched value, push the first, jump to A
	opFieldSet            // pop v and the current value, push a copy with fieldPaths[A] set to v
)

type Instr struct {
	Op        Op
	A, B      int32
	Line, Col int32
}

// Proto is compiled code: a function body, a module's top level or the
// expression inside a match pattern
type Proto struct {
	Code     []Instr
	NumSlots int
	Params   []int
	Lit      *ast.FunctionLiteral
	// Inspect is how a closure over this proto prints, the same text the
	// interpreter shows for a function
	Inspect string
	File    string
	// Heap is set when a closure may capture the frame, otherwise the VM
	// reuses the frame once the call returns
	Heap bool
}

type check struct {
	typ  *ast.TypeAnnotation
	what string
}

type importInfo struct {
	std string
	mod *semantic.Module
}

type tryInfo struct {
	lit *ast.FunctionLiteral
}

type patternInfo struct {
	pattern ast.Pattern
	// thunks are the compiled expressions value and map patterns compare
	// against, run in the matching frame
	thunks   map[ast.Expression]*Proto
	bindings map[*ast.Identifier]*semantic.Symbol
	file     string
}

// Program is a whole compiled program
type Program struct {
	Main       *Proto
	modules    map[*semantic.Module]*Proto
	mainMod    *semantic.Module
	consts     []object.Object
	names      []string
	refs       []*semantic.Ref
	checks     []check
	protos     []*Proto
	structs    []*ast.StructStatement
	imports    []importInfo
	tries      []tryInfo
	patterns   []patternInfo
	argNames   [][]*ast.Identifier
	fieldPaths [][]*ast.Identifier
}

// Compile turns an analyzed program without diagnostics into bytecode
func Compile(prog *semantic.Program) (*Program, error) {
	if prog.HasErrors() || prog.Main == nil {
		return nil, fmt.Errorf("cannot compile a program with errors")
	}
	p := &Program{modules: map[*semantic.Module]*Proto{}, mainMod: prog.Main}
	for _, mod := range prog.Modules {
		p.modules[mod] = p.compileModule(mod)
	}
	p.Main = p.modules[prog.Main]
	return p, nil
}

type compiler struct {
	p     *Program
	mod   *semantic.Module
	info  *semantic.FuncInfo
	proto *Proto
	// loops are the open loops, innermost last, for break and continue
	loops []*loopCtx
}

type loopCtx struct {
	head   int
	breaks []int
}

func (p *Program) compileModule(mod *semantic.Module) *Proto {
	c := &compiler{p: p, mod: mod, info: mod.Top, proto: &Proto{NumSlots: mod.Top.NumSlots, File: mod.File}}
	c.statements(mod.Program.Statements)
	c.emit(opReturn, 0, 0, nil)
	return c.proto
}

func (c *compiler) emit(op Op, a, b int, at ast.Node) int {
	in := Instr{Op: op, A: int32(a), B: int32(b)}
	if at != nil {
		line, col := ast.Position(at)
		in.Line, in.Col = int32(line), int32(col)
	}
	c.proto.Code = append(c.proto.Code, in)
	return len(c.proto.Code) - 1
}

func (c *compiler) here() int { return len(c.proto.Code) }

func (c *compiler) patch(at int) { c.proto.Code[at].A = int32(c.here()) }

func (c *compiler) constant(v object.Object) int {
	c.p.consts = append(c.p.consts, v)
	return len(c.p.consts) - 1
}

func (c *compiler) name(s string) int {
	c.p.names = append(c.p.names, s)
	return len(c.p.names) - 1
}

func (c *compiler) check(v *ast.TypeAnnotation, what string, at ast.Node) {
	if v == nil {
		return
	}
	c.p.checks = append(c.p.checks, check{typ: v, what: what})
	c.emit(opCheck, len(c.p.checks)-1, 0, at)
}

// statements leaves the value of the last statement on the stack, nil for
// none, the way a block's value works
func (c *compiler) statements(stmts []ast.Statement) {
	if len(stmts) == 0 {
		c.emit(opNil, 0, 0, nil)
		return
	}
	for i, s := range stmts {
		c.stmt(s)
		if i < len(stmts)-1 {
			c.emit(opPop, 0, 0, nil)
		}
	}
}

func (c *compiler) stmt(s ast.Statement) {
	switch s := s.(type) {
	case *ast.LetStatement:
		c.expr(s.Value)
		c.check(s.Type, s.Name.Value+" expects", s)
		c.emit(opStore, 0, c.mod.Decls[s].Slot, nil)
	case *ast.AssignStatement:
		c.expr(s.Value)
		r := c.mod.Assigns[s]
		if cand := r.Candidates; len(s.Fields) == 0 && len(cand) > 0 && cand[0].Direct && cand[0].Sym.Annot == nil {
			// the checker proved the binding exists and is mutable, and
			// there's no type to check, so it's a plain store
			c.emit(opStore, cand[0].Depth, cand[0].Sym.Slot, nil)
			return
		}
		ref := c.ref(r)
		if len(s.Fields) > 0 {
			c.emit(opLoadRef, ref, 0, s.Name)
			c.p.fieldPaths = append(c.p.fieldPaths, s.Fields)
			c.emit(opFieldSet, len(c.p.fieldPaths)-1, 0, s)
		}
		c.emit(opAssign, ref, 0, s)
	case *ast.ReturnStatement:
		c.expr(s.ReturnValue)
		if lit := c.info.Lit; lit != nil {
			c.check(lit.ReturnType, returnLabel(lit), s)
		}
		c.emit(opReturn, 0, 0, nil)
		c.emit(opNil, 0, 0, nil)
	case *ast.ImportStatement:
		imp := c.mod.Imports[s]
		c.p.imports = append(c.p.imports, importInfo{std: imp.Std, mod: imp.Module})
		c.emit(opImport, len(c.p.imports)-1, 0, s)
		c.emit(opStore, 0, c.mod.Decls[s].Slot, nil)
	case *ast.StructStatement:
		c.p.structs = append(c.p.structs, s)
		c.emit(opStruct, len(c.p.structs)-1, 0, nil)
		c.emit(opStore, 0, c.mod.Decls[s].Slot, nil)
	case *ast.ExternStatement:
		c.emit(opExtern, 0, 0, s)
		c.emit(opNil, 0, 0, nil)
	case *ast.ExpressionStatement:
		c.expr(s.Expression)
	case *ast.BlockStatement:
		c.statements(s.Statements)
	case *ast.WhileStatement:
		loop := &loopCtx{}
		c.emit(opLoopEnter, 0, 0, nil)
		loop.head = c.here()
		c.expr(s.Condition)
		exit := c.emit(opJumpFalse, 0, 0, nil)
		c.loops = append(c.loops, loop)
		c.loopBody(s, s.Body, nil)
		c.loops = c.loops[:len(c.loops)-1]
		c.emit(opJump, loop.head, 0, nil)
		c.patch(exit)
		c.endLoop(loop)
	case *ast.ForStatement:
		c.expr(s.Iterable)
		c.emit(opIterInit, len(s.Vars), 0, s)
		loop := &loopCtx{}
		c.emit(opLoopEnter, 0, 0, nil)
		loop.head = c.here()
		exit := c.emit(opIterNext, 0, len(s.Vars), nil)
		c.loops = append(c.loops, loop)
		c.loopBody(s, s.Body, func() {
			// IterNext pushed the items in order, store them last first
			for i := len(s.Vars) - 1; i >= 0; i-- {
				c.emit(opStore, 0, c.mod.Bindings[s.Vars[i]].Slot, nil)
				c.emit(opPop, 0, 0, nil)
			}
		})
		c.loops = c.loops[:len(c.loops)-1]
		c.emit(opJump, loop.head, 0, nil)
		c.patch(exit)
		c.endLoop(loop)
		c.emit(opPop, 0, 0, nil) // the iterator
		c.emit(opNil, 0, 0, nil)
		return
	case *ast.BreakStatement:
		loop := c.loops[len(c.loops)-1]
		loop.breaks = append(loop.breaks, c.emit(opBreak, 0, 0, nil))
		c.emit(opNil, 0, 0, nil)
	case *ast.ContinueStatement:
		c.emit(opContinue, c.loops[len(c.loops)-1].head, 0, nil)
		c.emit(opNil, 0, 0, nil)
	default:
		panic(fmt.Sprintf("vm: unsupported statement %T", s))
	}
	if _, ok := s.(*ast.WhileStatement); ok {
		c.emit(opNil, 0, 0, nil)
	}
}

// endLoop closes a loop: breaks land on the LoopExit that drops its marker
func (c *compiler) endLoop(loop *loopCtx) {
	exit := c.here()
	c.emit(opLoopExit, 0, 0, nil)
	for _, b := range loop.breaks {
		c.proto.Code[b].A = int32(exit)
	}
}

// loopBody runs one iteration, in a fresh frame when the body makes
// closures, like the C backend's loopBody
func (c *compiler) loopBody(loop ast.Statement, body *ast.BlockStatement, bind func()) {
	frame := c.mod.LoopFrames[loop]
	if frame != nil {
		c.emit(opPushFrame, frame.NumSlots, 0, nil)
	}
	if bind != nil {
		bind()
	}
	c.statements(body.Statements)
	c.emit(opPop, 0, 0, nil)
	if frame != nil {
		c.emit(opPopFrame, 0, 0, nil)
	}
}

func (c *compiler) ref(r *semantic.Ref) int {
	c.p.refs = append(c.p.refs, r)
	return len(c.p.refs) - 1
}

func (c *compiler) exprs(list []ast.Expression) {
	for _, e := range list {
		c.expr(e)
	}
}

func (c *compiler) expr(e ast.Expression) {
	switch e := e.(type) {
	case *ast.IntegerLiteral:
		c.emit(opConst, c.constant(object.NewInt(e.Value)), 0, nil)
	case *ast.FloatLiteral:
		c.emit(opConst, c.constant(&object.Float{Value: e.Value}), 0, nil)
	case *ast.StringLiteral:
		c.emit(opConst, c.constant(&object.String{Value: e.Value}), 0, nil)
	case *ast.Boolean:
		c.emit(opConst, c.constant(evaluator.Bool(e.Value)), 0, nil)
	case *ast.NilLiteral:
		c.emit(opNil, 0, 0, nil)
	case *ast.Identifier:
		ref := c.mod.Refs[e]
		if cand := ref.Candidates; len(cand) > 0 && cand[0].Direct {
			c.emit(opLoad, cand[0].Depth, cand[0].Sym.Slot, nil)
			return
		}
		c.emit(opLoadRef, c.ref(ref), 0, e)
	case *ast.PrefixExpression:
		c.expr(e.Right)
		if e.Operator == "!" {
			c.emit(opNot, 0, 0, nil)
		} else {
			c.emit(opNeg, 0, 0, e)
		}
	case *ast.InfixExpression:
		c.expr(e.Left)
		var jump int
		switch e.Operator {
		case "&&":
			jump = c.emit(opAndJump, 0, 0, nil)
		case "||":
			jump = c.emit(opOrJump, 0, 0, nil)
		}
		c.expr(e.Right)
		c.emit(opBinary, c.name(e.Operator), intOps[e.Operator], e)
		if e.Operator == "&&" || e.Operator == "||" {
			c.patch(jump)
		}
	case *ast.PipelineExpression:
		c.expr(e.Left)
		if call, ok := e.Right.(*ast.CallExpression); ok {
			c.expr(call.Function)
			c.pipeCall(e, call.Arguments)
			return
		}
		c.expr(e.Right)
		c.pipeCall(e, nil)
	case *ast.IfExpression:
		c.expr(e.Condition)
		elseJump := c.emit(opJumpFalse, 0, 0, nil)
		c.statements(e.Consequence.Statements)
		end := c.emit(opJump, 0, 0, nil)
		c.patch(elseJump)
		if e.Alternative != nil {
			c.statements(e.Alternative.Statements)
		} else {
			c.emit(opNil, 0, 0, nil)
		}
		c.patch(end)
	case *ast.FunctionLiteral:
		c.emit(opClosure, c.function(e), 0, nil)
	case *ast.ArrayLiteral:
		c.exprs(e.Elements)
		c.emit(opArray, len(e.Elements), 0, nil)
	case *ast.MapLiteral:
		for _, pair := range e.Pairs {
			c.expr(pair.Key)
			c.emit(opMapKey, 0, 0, e)
			c.expr(pair.Value)
		}
		c.emit(opMap, len(e.Pairs), 0, nil)
	case *ast.IndexExpression:
		c.expr(e.Left)
		c.expr(e.Index)
		c.emit(opIndex, 0, 0, e)
	case *ast.MemberExpression:
		c.expr(e.Object)
		c.emit(opMember, c.name(e.Property.Value), 0, e)
	case *ast.MatchExpression:
		c.match(e)
	case *ast.CallExpression:
		c.expr(e.Function)
		c.exprs(e.Arguments)
		if e.ArgNames != nil {
			c.p.argNames = append(c.p.argNames, e.ArgNames)
			c.emit(opCallNamed, len(e.Arguments), len(c.p.argNames)-1, e)
			return
		}
		c.emit(opCall, len(e.Arguments), 0, e)
	case *ast.TryExpression:
		c.expr(e.Value)
		c.p.tries = append(c.p.tries, tryInfo{lit: c.info.Lit})
		c.emit(opTry, len(c.p.tries)-1, 0, e)
	default:
		panic(fmt.Sprintf("vm: unsupported expression %T", e))
	}
}

// pipeCall calls with the piped value first. the piped value is evaluated
// before the callee, so the stack holds [left, callee, rest...] and B=1
// tells opCall to swap the first two into [callee, left, rest...]
func (c *compiler) pipeCall(e *ast.PipelineExpression, rest []ast.Expression) {
	c.exprs(rest)
	c.emit(opCall, len(rest)+1, 1, e)
}

func (c *compiler) function(lit *ast.FunctionLiteral) int {
	info := c.mod.FuncOf[lit]
	fn := &object.Function{Parameters: lit.Parameters, ParamTypes: lit.ParamTypes, ReturnType: lit.ReturnType, Body: lit.Body, Name: lit.Name}
	proto := &Proto{NumSlots: info.NumSlots, Params: info.ParamSlots, Lit: lit, Inspect: fn.Inspect(), File: c.mod.File, Heap: info.HeapFrame}
	c.p.protos = append(c.p.protos, proto)
	idx := len(c.p.protos) - 1
	sub := &compiler{p: c.p, mod: c.mod, info: info, proto: proto}
	sub.statements(lit.Body.Statements)
	if lit.ReturnType != nil {
		var at ast.Node = lit.Body
		if n := len(lit.Body.Statements); n > 0 {
			at = lit.Body.Statements[n-1]
		}
		sub.check(lit.ReturnType, returnLabel(lit), at)
	}
	sub.emit(opReturn, 0, 0, nil)
	return idx
}

func (c *compiler) match(e *ast.MatchExpression) {
	c.expr(e.Value)
	var ends []int
	for _, arm := range e.Arms {
		c.emit(opDup, 0, 0, nil)
		info := patternInfo{pattern: arm.Pattern, thunks: map[ast.Expression]*Proto{}, bindings: c.mod.Bindings, file: c.mod.File}
		c.thunks(arm.Pattern, info.thunks)
		c.p.patterns = append(c.p.patterns, info)
		miss := c.emit(opMatch, len(c.p.patterns)-1, 0, nil)
		var guardMiss int = -1
		if arm.Guard != nil {
			c.expr(arm.Guard)
			guardMiss = c.emit(opJumpFalse, 0, 0, nil)
		}
		c.expr(arm.Value)
		ends = append(ends, c.emit(opArmEnd, 0, 0, nil))
		c.proto.Code[miss].B = int32(c.here())
		if guardMiss >= 0 {
			c.patch(guardMiss)
		}
	}
	c.emit(opPop, 0, 0, nil)
	c.emit(opNil, 0, 0, nil)
	for _, end := range ends {
		c.patch(end)
	}
}

// thunks compiles the expressions inside a pattern into little protos that
// run in the frame doing the matching
func (c *compiler) thunks(p ast.Pattern, out map[ast.Expression]*Proto) {
	compile := func(e ast.Expression) {
		proto := &Proto{File: c.mod.File}
		sub := &compiler{p: c.p, mod: c.mod, info: c.info, proto: proto}
		sub.expr(e)
		sub.emit(opReturn, 0, 0, nil)
		out[e] = proto
	}
	switch p := p.(type) {
	case *ast.ValuePattern:
		compile(p.Value)
	case *ast.ArrayPattern:
		for _, el := range p.Elements {
			c.thunks(el, out)
		}
	case *ast.MapPattern:
		for i, k := range p.Keys {
			compile(k)
			c.thunks(p.Values[i], out)
		}
	case *ast.ResultPattern:
		c.thunks(p.Inner, out)
	}
}

// intOps numbers the operators the VM does itself when both sides are ints
var intOps = map[string]int{"+": 1, "-": 2, "*": 3, "/": 4, "%": 5, "<": 6, ">": 7, "<=": 8, ">=": 9, "==": 10, "!=": 11}

func returnLabel(lit *ast.FunctionLiteral) string {
	if lit.Name != "" {
		return lit.Name + " returns"
	}
	return "function returns"
}
