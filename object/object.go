package object

import (
	"bytes"
	"fmt"
	"strings"

	"ranalang/ast"
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
)

type Object interface {
	Type() Type
	Inspect() string
}

type Integer struct {
	Value int64
}

func (i *Integer) Type() Type      { return IntegerObj }
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

type Error struct {
	Message string
}

func (e *Error) Type() Type      { return ErrorObj }
func (e *Error) Inspect() string { return "runtime error: " + e.Message }

type Function struct {
	Parameters []*ast.Identifier
	Body       *ast.BlockStatement
	Env        *Environment
	Name       string
}

func (f *Function) Type() Type { return FunctionObj }
func (f *Function) Inspect() string {
	var out bytes.Buffer
	params := []string{}
	for _, p := range f.Parameters {
		params = append(params, p.String())
	}
	out.WriteString("fn")
	if f.Name != "" {
		out.WriteString(" " + f.Name)
	}
	out.WriteString("(")
	out.WriteString(strings.Join(params, ", "))
	out.WriteString(") {\n")
	out.WriteString(f.Body.String())
	out.WriteString("\n}")
	return out.String()
}

type BuiltinFunction func(args ...Object) Object

type Builtin struct {
	Fn BuiltinFunction
}

func (b *Builtin) Type() Type      { return BuiltinObj }
func (b *Builtin) Inspect() string { return "builtin function" }
