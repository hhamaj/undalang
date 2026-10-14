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
