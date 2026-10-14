package object

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"

	"undalang/ast"
	"undalang/diag"
)

type Type string

const (
	IntegerObj     = "INTEGER"
	FloatObj       = "FLOAT"
	BooleanObj     = "BOOLEAN"
	StringObj      = "STRING"
	NilObj         = "NIL"
	ReturnValueObj = "RETURN_VALUE"
	ErrorObj       = "ERROR"
	FunctionObj    = "FUNCTION"
	BuiltinObj     = "BUILTIN"
	ArrayObj       = "ARRAY"
	MapObj         = "MAP"
	ModuleObj      = "MODULE"
	BreakObj       = "BREAK"
	ContinueObj    = "CONTINUE"
	ResultObj      = "RESULT"
)

type Object interface {
	Type() Type
	Inspect() string
}

type Integer struct {
	Value int64
}

func (i *Integer) Type() Type { return IntegerObj }

// small ints are shared so counters and indexes don't allocate. that's only
// fine because nothing ever changes an Integer after making it
var smallInts = func() (out [1280]Integer) {
	for i := range out {
		out[i].Value = int64(i) - 256
	}
	return out
}()

func NewInt(v int64) *Integer {
	if v >= -256 && v < 1024 {
		return &smallInts[v+256]
	}
	return &Integer{Value: v}
}
func (i *Integer) Inspect() string { return fmt.Sprintf("%d", i.Value) }

type Float struct {
	Value float64
}

func (f *Float) Type() Type      { return FloatObj }
func (f *Float) Inspect() string { return fmt.Sprintf("%g", f.Value) }

type Boolean struct {
	Value bool
}

func (b *Boolean) Type() Type      { return BooleanObj }
func (b *Boolean) Inspect() string { return fmt.Sprintf("%t", b.Value) }

type String struct {
	Value string
}

func (s *String) Type() Type      { return StringObj }
func (s *String) Inspect() string { return s.Value }

type Nil struct{}

func (n *Nil) Type() Type      { return NilObj }
func (n *Nil) Inspect() string { return "nil" }

type ReturnValue struct {
	Value Object
}

func (rv *ReturnValue) Type() Type      { return ReturnValueObj }
func (rv *ReturnValue) Inspect() string { return rv.Value.Inspect() }

// BreakSignal and ContinueSignal propagate out of a loop body the same way
// ReturnValue propagates out of a function
type BreakSignal struct{}

func (b *BreakSignal) Type() Type      { return BreakObj }
func (b *BreakSignal) Inspect() string { return "break" }

type ContinueSignal struct{}

func (c *ContinueSignal) Type() Type      { return ContinueObj }
func (c *ContinueSignal) Inspect() string { return "continue" }

type Error struct {
	Message string
	File    string
	Line    int
	Column  int
}

func (e *Error) Type() Type { return ErrorObj }
func (e *Error) Inspect() string {
	return "runtime error: " + diag.Location(e.File, e.Line, e.Column) + e.Message
}

type Function struct {
	Parameters []*ast.Identifier
	ParamTypes []*ast.TypeAnnotation
	ReturnType *ast.TypeAnnotation
	Body       *ast.BlockStatement
	Env        *Environment
	Name       string
	// File and BaseDir remember where the function was defined so errors and
	// imports in its body resolve against that module
	File    string
	BaseDir string
}

func (f *Function) Type() Type { return FunctionObj }

// ReturnLabel names the function in return type errors
func (f *Function) ReturnLabel() string {
	if f.Name != "" {
		return f.Name + " returns"
	}
	return "function returns"
}
func (f *Function) Inspect() string {
	var out bytes.Buffer
	out.WriteString("fn")
	if f.Name != "" {
		out.WriteString(" " + f.Name)
	}
	out.WriteString(ast.Signature(f.Parameters, f.ParamTypes, f.ReturnType))
	out.WriteString(" {\n")
	out.WriteString(f.Body.String())
	out.WriteString("\n}")
	return out.String()
}

// Result is what ok(v) and err(e) make
type Result struct {
	Ok    bool
	Value Object
}

func (r *Result) Type() Type { return ResultObj }
func (r *Result) Inspect() string {
	if r.Ok {
		return "ok(" + r.Value.Inspect() + ")"
	}
	return "err(" + r.Value.Inspect() + ")"
}

type BuiltinFunction func(args ...Object) Object

type Builtin struct {
	Fn BuiltinFunction
}

func (b *Builtin) Type() Type      { return BuiltinObj }
func (b *Builtin) Inspect() string { return "builtin function" }

type Array struct {
	Elements []Object
}

func (a *Array) Type() Type { return ArrayObj }
func (a *Array) Inspect() string {
	parts := make([]string, 0, len(a.Elements))
	for _, e := range a.Elements {
		parts = append(parts, e.Inspect())
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

type HashKey struct {
	Type  Type
	Value uint64
}

type Hashable interface {
	HashKey() HashKey
}

func (b *Boolean) HashKey() HashKey {
	if b.Value {
		return HashKey{Type: b.Type(), Value: 1}
	}
	return HashKey{Type: b.Type(), Value: 0}
}

func (i *Integer) HashKey() HashKey {
	return HashKey{Type: i.Type(), Value: uint64(i.Value)}
}

func (s *String) HashKey() HashKey {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s.Value))
	return HashKey{Type: s.Type(), Value: h.Sum64()}
}

func (n *Nil) HashKey() HashKey {
	return HashKey{Type: n.Type(), Value: 0}
}

type MapPair struct {
	Key   Object
	Value Object
}

type Map struct {
	Pairs map[HashKey]MapPair
}

func (m *Map) Type() Type { return MapObj }
func (m *Map) Inspect() string {
	parts := make([]string, 0, len(m.Pairs))
	for _, pair := range m.Pairs {
		parts = append(parts, fmt.Sprintf("%s: %s", pair.Key.Inspect(), pair.Value.Inspect()))
	}
	sort.Strings(parts)
	return "{" + strings.Join(parts, ", ") + "}"
}

type Module struct {
	Name    string
	Members map[string]Object
}

func (m *Module) Type() Type { return ModuleObj }
func (m *Module) Inspect() string {
	keys := make([]string, 0, len(m.Members))
	for k := range m.Members {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return fmt.Sprintf("module %s {%s}", m.Name, strings.Join(keys, ", "))
}

const StructDefObj = "STRUCT"

// StructDef is a struct type. calling it builds a value
type StructDef struct {
	Name   string
	Fields []string
	// Types lines up with Fields, nil where a field takes anything
	Types []*ast.TypeAnnotation
}

func (d *StructDef) Type() Type      { return StructDefObj }
func (d *StructDef) Inspect() string { return "struct " + d.Name }

func (d *StructDef) FieldIndex(name string) int {
	for i, f := range d.Fields {
		if f == name {
			return i
		}
	}
	return -1
}

func (d *StructDef) NoField(name string) *Error {
	return &Error{Message: fmt.Sprintf("%s has no field %q", d.Name, name)}
}

// CheckField returns an error when v doesn't fit field i's type
func (d *StructDef) CheckField(i int, v Object) *Error {
	if t := d.Types[i]; t != nil && !Fits(t, v) {
		return TypeMismatch("field "+d.Fields[i]+" of "+d.Name+" expects", t, v)
	}
	return nil
}

// New builds a value from positional arguments, or named ones when names is
// set. the native runtime's u_struct_new makes the same checks in the same
// order
func (d *StructDef) New(args []Object, names []string) Object {
	values := make([]Object, len(d.Fields))
	if names == nil {
		if len(args) != len(d.Fields) {
			return &Error{Message: fmt.Sprintf("wrong number of arguments: expected %d, got %d", len(d.Fields), len(args))}
		}
		copy(values, args)
	} else {
		for i, name := range names {
			idx := d.FieldIndex(name)
			if idx < 0 {
				return d.NoField(name)
			}
			if values[idx] != nil {
				return &Error{Message: fmt.Sprintf("field %s given twice", name)}
			}
			values[idx] = args[i]
		}
		for i, v := range values {
			if v == nil {
				return &Error{Message: fmt.Sprintf("missing field %s for %s", d.Fields[i], d.Name)}
			}
		}
	}
	for i, v := range values {
		if err := d.CheckField(i, v); err != nil {
			return err
		}
	}
	return &Struct{Def: d, Fields: values}
}

// Struct is a struct value. its runtime type name is the struct's name
type Struct struct {
	Def    *StructDef
	Fields []Object
}

func (s *Struct) Type() Type { return Type(s.Def.Name) }
func (s *Struct) Inspect() string {
	parts := make([]string, len(s.Fields))
	for i, v := range s.Fields {
		parts[i] = s.Def.Fields[i] + ": " + v.Inspect()
	}
	return s.Def.Name + "(" + strings.Join(parts, ", ") + ")"
}

// Fits reports whether v fits t, down to the elements of typed arrays and
// maps. ast.TypeAnnotation.Accepts only looks at the outer type
func Fits(t *ast.TypeAnnotation, v Object) bool {
	bad, _ := misfit(t, v)
	return !bad
}

// misfit finds why v doesn't fit t. the detail names the first element that
// doesn't, map entries are walked in key order like maps.keys so both
// backends name the same one
func misfit(t *ast.TypeAnnotation, v Object) (bool, string) {
	if !t.Accepts(string(v.Type())) {
		return true, ""
	}
	switch {
	case t.Name == "array" && len(t.Params) == 1:
		for i, el := range v.(*Array).Elements {
			if bad, d := misfit(t.Params[0], el); bad {
				return true, fmt.Sprintf(" (element %d is %s%s)", i, el.Type(), d)
			}
		}
	case t.Name == "map" && len(t.Params) == 2:
		m := v.(*Map)
		pairs := make([]MapPair, 0, len(m.Pairs))
		for _, p := range m.Pairs {
			pairs = append(pairs, p)
		}
		sort.Slice(pairs, func(i, j int) bool { return pairs[i].Key.Inspect() < pairs[j].Key.Inspect() })
		for _, p := range pairs {
			if bad, d := misfit(t.Params[0], p.Key); bad {
				return true, fmt.Sprintf(" (key %s is %s%s)", keyText(p.Key), p.Key.Type(), d)
			}
			if bad, d := misfit(t.Params[1], p.Value); bad {
				return true, fmt.Sprintf(" (value at key %s is %s%s)", keyText(p.Key), p.Value.Type(), d)
			}
		}
	}
	return false, ""
}

func keyText(k Object) string {
	if s, ok := k.(*String); ok {
		return `"` + s.Value + `"`
	}
	return k.Inspect()
}

const BufferObj = "BUFFER"

// Buffer is memory from mem.alloc. it's the one value that changes in place:
// it stands for memory C reads and writes, so copying it would defeat it
type Buffer struct {
	Data  []byte
	Freed bool
}

func (b *Buffer) Type() Type { return BufferObj }
func (b *Buffer) Inspect() string {
	if b.Freed {
		return "buffer(freed)"
	}
	return fmt.Sprintf("buffer(%d bytes)", len(b.Data))
}

// ExitSignal is os.exit on its way out. it unwinds like an error, the runner
// turns it into the process exit code
type ExitSignal struct {
	Code int
}

func (e *ExitSignal) Type() Type      { return "EXIT" }
func (e *ExitSignal) Inspect() string { return fmt.Sprintf("exit(%d)", e.Code) }
