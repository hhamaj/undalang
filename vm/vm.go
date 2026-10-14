package vm

import (
	"io"

	"undalang/ast"
	"undalang/builtins"
	"undalang/evaluator"
	"undalang/object"
)

// Frame is one function's slots. closures keep their defining frame alive
// through parent
type Frame struct {
	slots  []object.Object
	parent *Frame
	// inline holds the slots of small frames, so a call allocates once
	inline [4]object.Object
}

func newFrame(n int, parent *Frame) *Frame {
	f := &Frame{parent: parent}
	if n <= len(f.inline) {
		f.slots = f.inline[:n]
	} else {
		f.slots = make([]object.Object, n)
	}
	return f
}

func (f *Frame) up(depth int32) *Frame {
	for ; depth > 0; depth-- {
		f = f.parent
	}
	return f
}

// Closure is a function value. it prints and compares like the
// interpreter's object.Function
type Closure struct {
	proto *Proto
	env   *Frame
}

func (c *Closure) Type() object.Type { return object.FunctionObj }
func (c *Closure) Inspect() string   { return c.proto.Inspect }

// iterator walks what a for loop binds, see evaluator.IterationItems
type iterator struct {
	first, second []object.Object
	i             int
}

func (it *iterator) Type() object.Type { return "ITERATOR" }
func (it *iterator) Inspect() string   { return "iterator" }

type callRec struct {
	proto *Proto
	pc    int
	frame *Frame
	// base is where the callee sat on the stack, the result goes there
	base     int
	loopBase int
	// counted says whether this call counts toward the depth limit, module
	// bodies and pattern thunks don't
	counted bool
	// own is the frame the call made, recycled on return unless closures
	// may hold it
	own *Frame
}

type loopMark struct {
	sp    int
	frame *Frame
}

type VM struct {
	prog     *Program
	stack    []object.Object
	calls    []callRec
	loops    []loopMark
	depth    int
	builtins map[string]*object.Builtin
	std      map[string]*object.Module
	modules  map[*Proto]*object.Module
	structs  map[*ast.StructStatement]*object.StructDef
	free     []*Frame
}

// release puts a returning call's frame back for reuse
func (v *VM) release(rec *callRec) {
	if rec.own != nil && !rec.proto.Heap && len(v.free) < 256 {
		clear(rec.own.slots)
		rec.own.parent = nil
		v.free = append(v.free, rec.own)
	}
}

func (v *VM) frame(n int, parent *Frame) *Frame {
	if k := len(v.free); k > 0 && cap(v.free[k-1].slots) >= n {
		f := v.free[k-1]
		v.free = v.free[:k-1]
		f.slots = f.slots[:n]
		f.parent = parent
		return f
	}
	return newFrame(n, parent)
}

func New(prog *Program, out io.Writer) *VM {
	v := &VM{
		prog:     prog,
		builtins: builtins.New(out),
		modules:  map[*Proto]*object.Module{},
		stack:    make([]object.Object, 0, 1024),
		structs:  map[*ast.StructStatement]*object.StructDef{},
	}
	v.std = builtins.StdModules(v.apply)
	return v
}

// Run runs the main module. the result is the program's last value, or an
// *object.Error or *object.ExitSignal that ended it
func (v *VM) Run() object.Object {
	return v.runProto(v.prog.Main, newFrame(v.prog.Main.NumSlots, nil), false)
}

// runProto runs proto in frame to its return, re-entrantly
func (v *VM) runProto(p *Proto, frame *Frame, counted bool) object.Object {
	entry := len(v.calls)
	v.calls = append(v.calls, callRec{proto: p, frame: frame, base: len(v.stack), loopBase: len(v.loops), counted: counted})
	return v.loop(entry)
}

func (v *VM) push(o object.Object) { v.stack = append(v.stack, o) }

func (v *VM) pop() object.Object {
	o := v.stack[len(v.stack)-1]
	v.stack = v.stack[:len(v.stack)-1]
	return o
}

// abrupt values end the run: errors and os.exit. an error without a place
// yet gets the one of the instruction that hit it
func abrupt(o object.Object) bool {
	switch o.(type) {
	case *object.Error, *object.ExitSignal:
		return true
	}
	return false
}

func (v *VM) stamp(o object.Object, p *Proto, in *Instr) object.Object {
	if e, ok := o.(*object.Error); ok && e.Line == 0 && in.Line > 0 {
		e.File, e.Line, e.Column = p.File, int(in.Line), int(in.Col)
	}
	return o
}

// unwind drops everything the run started with entry pushed and hands the
// abrupt value back
func (v *VM) unwind(entry int, o object.Object) object.Object {
	for i := len(v.calls) - 1; i >= entry; i-- {
		if v.calls[i].counted {
			v.depth--
		}
	}
	rec := v.calls[entry]
	v.calls = v.calls[:entry]
	v.stack = v.stack[:rec.base]
	v.loops = v.loops[:rec.loopBase]
	return o
}

func (v *VM) loop(entry int) object.Object {
	// ri indexes the running call. nested runs from builtins can grow
	// v.calls, so no pointers into it survive an instruction
	ri := len(v.calls) - 1
	p, frame, pc := v.calls[ri].proto, v.calls[ri].frame, 0
	for {
		in := &p.Code[pc]
		pc++
		var fail object.Object
		switch in.Op {
		case opConst:
			v.push(v.prog.consts[in.A])
		case opNil:
			v.push(evaluator.Nil)
		case opPop:
			v.stack = v.stack[:len(v.stack)-1]
		case opDup:
			v.push(v.stack[len(v.stack)-1])
		case opLoad:
			v.push(frame.up(in.A).slots[in.B])
		case opStore:
			frame.up(in.A).slots[in.B] = v.stack[len(v.stack)-1]
		case opLoadRef:
			val, err := v.loadRef(frame, int(in.A))
			if err != nil {
				fail = err
				break
			}
			v.push(val)
		case opAssign:
			fail = v.assign(frame, int(in.A), v.stack[len(v.stack)-1])
		case opCheck:
			ck := v.prog.checks[in.A]
			if top := v.stack[len(v.stack)-1]; !object.Fits(ck.typ, top) {
				fail = object.TypeMismatch(ck.what, ck.typ, top)
			}
		case opBinary:
			n := len(v.stack)
			if li, ok := v.stack[n-2].(*object.Integer); ok && in.B != 0 {
				if ri, ok := v.stack[n-1].(*object.Integer); ok {
					if res := intOp(in.B, li.Value, ri.Value); res != nil {
						v.stack[n-2] = res
						v.stack = v.stack[:n-1]
						break
					}
				}
			}
			r := v.pop()
			l := v.pop()
			res := evaluator.Infix(v.prog.names[in.A], l, r)
			if abrupt(res) {
				fail = res
				break
			}
			v.push(res)
		case opNot:
			v.push(evaluator.Bool(!evaluator.Truthy(v.pop())))
		case opNeg:
			res := evaluator.Prefix("-", v.pop())
			if abrupt(res) {
				fail = res
				break
			}
			v.push(res)
		case opAndJump:
			if top := len(v.stack) - 1; !evaluator.Truthy(v.stack[top]) {
				v.stack[top] = evaluator.Bool(false)
				pc = int(in.A)
			}
		case opOrJump:
			if top := len(v.stack) - 1; evaluator.Truthy(v.stack[top]) {
				v.stack[top] = evaluator.Bool(true)
				pc = int(in.A)
			}
		case opJump:
			pc = int(in.A)
		case opJumpFalse:
			if !evaluator.Truthy(v.pop()) {
				pc = int(in.A)
			}
		case opArray:
			n := int(in.A)
			elems := make([]object.Object, n)
			copy(elems, v.stack[len(v.stack)-n:])
			v.stack = v.stack[:len(v.stack)-n]
			v.push(&object.Array{Elements: elems})
		case opMapKey:
			if k := v.stack[len(v.stack)-1]; !hashable(k) {
				fail = evaluator.Errorf("unusable as map key: %s", k.Type())
			}
		case opMap:
			n := int(in.A)
			pairs := make(map[object.HashKey]object.MapPair, n)
			items := v.stack[len(v.stack)-2*n:]
			for i := 0; i < n; i++ {
				k := items[2*i]
				pairs[k.(object.Hashable).HashKey()] = object.MapPair{Key: k, Value: items[2*i+1]}
			}
			v.stack = v.stack[:len(v.stack)-2*n]
			v.push(&object.Map{Pairs: pairs})
		case opIndex:
			i := v.pop()
			l := v.pop()
			res := evaluator.Index(l, i)
			if abrupt(res) {
				fail = res
				break
			}
			v.push(res)
		case opMember:
			res := evaluator.Member(v.pop(), v.prog.names[in.A])
			if abrupt(res) {
				fail = res
				break
			}
			v.push(res)
		case opCall, opCallNamed:
			argc := int(in.A)
			calleeAt := len(v.stack) - argc - 1
			if in.Op == opCall && in.B == 1 {
				v.stack[calleeAt], v.stack[calleeAt+1] = v.stack[calleeAt+1], v.stack[calleeAt]
			}
			callee := v.stack[calleeAt]
			if in.Op == opCallNamed {
				args := append([]object.Object{}, v.stack[calleeAt+1:]...)
				v.stack = v.stack[:calleeAt]
				res := evaluator.ApplyNamed(callee, args, v.prog.argNames[in.B])
				if abrupt(res) {
					fail = res
					break
				}
				v.push(res)
				break
			}
			if cl, ok := callee.(*Closure); ok {
				newFrame, err := v.enter(cl, v.stack[calleeAt+1:])
				if err != nil {
					fail = err
					break
				}
				v.calls[ri].pc = pc
				v.calls = append(v.calls, callRec{proto: cl.proto, frame: newFrame, base: calleeAt, loopBase: len(v.loops), counted: true, own: newFrame})
				v.stack = v.stack[:calleeAt]
				ri = len(v.calls) - 1
				p, frame, pc = cl.proto, newFrame, 0
				break
			}
			args := append([]object.Object{}, v.stack[calleeAt+1:]...)
			v.stack = v.stack[:calleeAt]
			res := v.callOther(callee, args)
			if abrupt(res) {
				fail = res
				break
			}
			v.push(res)
		case opClosure:
			v.push(&Closure{proto: v.prog.protos[in.A], env: frame})
		case opStruct:
			v.push(v.structDef(v.prog.structs[in.A]))
		case opImport:
			res := v.importModule(v.prog.imports[in.A])
			if abrupt(res) {
				fail = res
				break
			}
			v.push(res)
		case opExtern:
			fail = evaluator.Errorf("extern functions need the native backend, run with --native or use unda build")
		case opReturn:
			result := v.pop()
			done := v.calls[len(v.calls)-1]
			if done.counted {
				v.depth--
			}
			v.release(&done)
			v.calls = v.calls[:len(v.calls)-1]
			v.stack = v.stack[:done.base]
			v.loops = v.loops[:done.loopBase]
			if len(v.calls) == entry {
				return result
			}
			v.push(result)
			ri = len(v.calls) - 1
			p, frame, pc = v.calls[ri].proto, v.calls[ri].frame, v.calls[ri].pc
		case opTry:
			val := v.pop()
			r, ok := val.(*object.Result)
			switch {
			case !ok:
				fail = evaluator.Errorf("? expects RESULT, got %s", val.Type())
			case r.Ok:
				v.push(r.Value)
			default:
				lit := v.prog.tries[in.A].lit
				if lit == nil {
					fail = evaluator.Errorf("unhandled %s", r.Inspect())
					break
				}
				if lit.ReturnType != nil && !lit.ReturnType.Accepts(object.ResultObj) {
					fail = object.TypeMismatch(returnLabel(lit), lit.ReturnType, r)
					break
				}
				// return the err from this function, like opReturn
				v.push(r)
				pc = len(p.Code) - 1
				for p.Code[pc].Op != opReturn {
					pc--
				}
			}
		case opPushFrame:
			frame = newFrame(int(in.A), frame)
			v.calls[ri].frame = frame
		case opPopFrame:
			frame = frame.parent
			v.calls[ri].frame = frame
		case opLoopEnter:
			v.loops = append(v.loops, loopMark{sp: len(v.stack), frame: frame})
		case opLoopExit:
			v.loops = v.loops[:len(v.loops)-1]
		case opBreak, opContinue:
			m := v.loops[len(v.loops)-1]
			v.stack = v.stack[:m.sp]
			frame = m.frame
			v.calls[ri].frame = frame
			pc = int(in.A)
		case opIterInit:
			first, second, err := evaluator.IterationItems(v.pop(), int(in.A))
			if err != nil {
				fail = err
				break
			}
			v.push(&iterator{first: first, second: second})
		case opIterNext:
			it := v.stack[len(v.stack)-1].(*iterator)
			if it.i >= len(it.first) {
				pc = int(in.A)
				break
			}
			v.push(it.first[it.i])
			if in.B == 2 {
				v.push(it.second[it.i])
			}
			it.i++
		case opMatch:
			info := &v.prog.patterns[in.A]
			ok, res := v.match(info, info.pattern, v.pop(), frame)
			if res != nil {
				fail = res
				break
			}
			if !ok {
				pc = int(in.B)
			}
		case opArmEnd:
			result := v.pop()
			v.stack[len(v.stack)-1] = result
			pc = int(in.A)
		case opFieldSet:
			cur := v.pop()
			val := v.pop()
			res := evaluator.WithFields(cur, v.prog.fieldPaths[in.A], val)
			if abrupt(res) {
				fail = res
				break
			}
			v.push(res)
		}
		if fail != nil {
			return v.unwind(entry, v.stamp(fail, p, in))
		}
	}
}

// enter checks a call to a closure the way the interpreter's
// applyFunctionWithContext does and builds the callee's frame
func (v *VM) enter(cl *Closure, args []object.Object) (*Frame, object.Object) {
	lit := cl.proto.Lit
	if len(args) != len(lit.Parameters) {
		return nil, evaluator.Errorf("wrong number of arguments: expected %d, got %d", len(lit.Parameters), len(args))
	}
	if v.depth >= evaluator.MaxCallDepth {
		return nil, evaluator.Errorf("maximum call depth exceeded")
	}
	for i, param := range lit.Parameters {
		if i < len(lit.ParamTypes) && lit.ParamTypes[i] != nil && !object.Fits(lit.ParamTypes[i], args[i]) {
			return nil, object.TypeMismatch("parameter "+param.Value+" expects", lit.ParamTypes[i], args[i])
		}
	}
	frame := v.frame(cl.proto.NumSlots, cl.env)
	for i, slot := range cl.proto.Params {
		frame.slots[slot] = args[i]
	}
	v.depth++
	return frame, nil
}

func (v *VM) callOther(callee object.Object, args []object.Object) object.Object {
	switch fn := callee.(type) {
	case *object.Builtin:
		return fn.Fn(args...)
	case *object.StructDef:
		return fn.New(args, nil)
	}
	return evaluator.Errorf("not a function: %s", callee.Type())
}

// apply is how builtins call back into Unda code, arrays.map and friends
func (v *VM) apply(fn object.Object, args []object.Object) object.Object {
	cl, ok := fn.(*Closure)
	if !ok {
		return v.callOther(fn, args)
	}
	frame, err := v.enter(cl, args)
	if err != nil {
		return err
	}
	entry := len(v.calls)
	v.calls = append(v.calls, callRec{proto: cl.proto, frame: frame, base: len(v.stack), loopBase: len(v.loops), counted: true, own: frame})
	return v.loop(entry)
}

func (v *VM) loadRef(frame *Frame, i int) (object.Object, object.Object) {
	ref := v.prog.refs[i]
	for _, cand := range ref.Candidates {
		if val := frame.up(int32(cand.Depth)).slots[cand.Sym.Slot]; val != nil {
			return val, nil
		}
	}
	if ref.Builtin != "" {
		return v.builtins[ref.Builtin], nil
	}
	return nil, evaluator.Errorf("identifier not found: %s", ref.Name)
}

// assign stores through the first binding that exists, the way the
// interpreter's Environment.Assign walks outward
func (v *VM) assign(frame *Frame, i int, val object.Object) object.Object {
	ref := v.prog.refs[i]
	for _, cand := range ref.Candidates {
		f := frame.up(int32(cand.Depth))
		if !cand.Direct && f.slots[cand.Sym.Slot] == nil {
			continue
		}
		if !cand.Direct && !cand.Sym.Mutable {
			return evaluator.Errorf("cannot assign to immutable binding %q", ref.Name)
		}
		if a := cand.Sym.Annot; a != nil && !object.Fits(a, val) {
			return object.TypeMismatch(ref.Name+" expects", a, val)
		}
		f.slots[cand.Sym.Slot] = val
		return nil
	}
	return evaluator.Errorf("identifier not found: %s", ref.Name)
}

func (v *VM) structDef(s *ast.StructStatement) *object.StructDef {
	if def, ok := v.structs[s]; ok {
		return def
	}
	def := &object.StructDef{Name: s.Name.Value, Types: s.Types}
	for _, f := range s.Fields {
		def.Fields = append(def.Fields, f.Value)
	}
	v.structs[s] = def
	return def
}

// importModule runs a local module's top level once and exposes the
// bindings that ran, like the interpreter
func (v *VM) importModule(imp importInfo) object.Object {
	if imp.std != "" {
		return v.std[imp.std]
	}
	proto := v.prog.modules[imp.mod]
	if mod, ok := v.modules[proto]; ok {
		return mod
	}
	frame := newFrame(proto.NumSlots, nil)
	if res := v.runProto(proto, frame, false); abrupt(res) {
		return res
	}
	members := map[string]object.Object{}
	for _, sym := range imp.mod.Exports {
		if val := frame.slots[sym.Slot]; val != nil {
			members[sym.Name] = val
		}
	}
	mod := &object.Module{Name: imp.mod.Name, Members: members}
	v.modules[proto] = mod
	return mod
}

// match is the interpreter's matchPattern over slots. the second result is
// an error to propagate
func (v *VM) match(info *patternInfo, pattern ast.Pattern, value object.Object, frame *Frame) (bool, object.Object) {
	switch p := pattern.(type) {
	case *ast.WildcardPattern:
		return true, nil
	case *ast.BindingPattern:
		frame.slots[info.bindings[p.Name].Slot] = value
		return true, nil
	case *ast.ValuePattern:
		expected := v.runProto(info.thunks[p.Value], frame, false)
		if abrupt(expected) {
			return false, expected
		}
		return evaluator.Equal(value, expected), nil
	case *ast.ArrayPattern:
		arr, ok := value.(*object.Array)
		if !ok || len(arr.Elements) < len(p.Elements) || (!p.HasRest && len(arr.Elements) != len(p.Elements)) {
			return false, nil
		}
		for i, el := range p.Elements {
			if ok, res := v.match(info, el, arr.Elements[i], frame); !ok || res != nil {
				return false, res
			}
		}
		if p.Rest != nil {
			rest := append([]object.Object{}, arr.Elements[len(p.Elements):]...)
			frame.slots[info.bindings[p.Rest].Slot] = &object.Array{Elements: rest}
		}
		return true, nil
	case *ast.MapPattern:
		m, ok := value.(*object.Map)
		if !ok {
			return false, nil
		}
		for i, keyExpr := range p.Keys {
			key := v.runProto(info.thunks[keyExpr], frame, false)
			if abrupt(key) {
				return false, key
			}
			hashable, ok := key.(object.Hashable)
			if !ok {
				err := evaluator.Errorf("unusable as map key: %s", key.Type())
				err.File, err.Line, err.Column = info.file, p.Token.Line, p.Token.Column
				return false, err
			}
			pair, ok := m.Pairs[hashable.HashKey()]
			if !ok {
				return false, nil
			}
			if ok, res := v.match(info, p.Values[i], pair.Value, frame); !ok || res != nil {
				return false, res
			}
		}
		return true, nil
	case *ast.ResultPattern:
		r, ok := value.(*object.Result)
		if !ok || r.Ok != p.Ok {
			return false, nil
		}
		return v.match(info, p.Inner, r.Value, frame)
	}
	return false, evaluator.Errorf("unsupported pattern %T", pattern)
}

// intOp is the int-int case of the interpreter's evalNumberInfixExpression.
// nil means "not here": division and modulo by zero or a negative number go
// the long way so the errors and edge cases stay the interpreter's
func intOp(op int32, a, b int64) object.Object {
	switch op {
	case 1:
		return object.NewInt(a + b)
	case 2:
		return object.NewInt(a - b)
	case 3:
		return object.NewInt(a * b)
	case 4:
		if b > 0 {
			return object.NewInt(a / b)
		}
	case 5:
		if b > 0 {
			return object.NewInt(a % b)
		}
	case 6:
		return evaluator.Bool(a < b)
	case 7:
		return evaluator.Bool(a > b)
	case 8:
		return evaluator.Bool(a <= b)
	case 9:
		return evaluator.Bool(a >= b)
	case 10:
		return evaluator.Bool(a == b)
	case 11:
		return evaluator.Bool(a != b)
	}
	return nil
}

func hashable(o object.Object) bool {
	_, ok := o.(object.Hashable)
	return ok
}
