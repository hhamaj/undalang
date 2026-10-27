package object

import (
	"fmt"

	"undalang/ast"
)

type Binding struct {
	Value   Object
	Mutable bool
	// Type is the annotation the binding was declared with, assignments have
	// to keep fitting it
	Type *ast.TypeAnnotation
}

// Environment is one scope. most scopes hold a handful of names, so they live
// in a slice that gets searched linearly. a map index only shows up once a
// scope gets big, like the top level of a long script
type Environment struct {
	entries []entry
	index   map[string]int
	outer   *Environment
}

type entry struct {
	name string
	Binding
}

const indexAfter = 8

func NewEnvironment() *Environment {
	return &Environment{}
}

func (e *Environment) find(name string) int {
	if e.index != nil {
		if i, ok := e.index[name]; ok {
			return i
		}
		return -1
	}
	for i := range e.entries {
		if e.entries[i].name == name {
			return i
		}
	}
	return -1
}

// NewFunctionEnvironment makes the scope for a call, sized for its
// parameters so defining them doesn't grow the slice
func NewFunctionEnvironment(outer *Environment, params int) *Environment {
	return &Environment{entries: make([]entry, 0, params), outer: outer}
}

func NewEnclosedEnvironment(outer *Environment) *Environment {
	env := NewEnvironment()
	env.outer = outer
	return env
}

func (e *Environment) Get(name string) (Object, bool) {
	for env := e; env != nil; env = env.outer {
		if i := env.find(name); i >= 0 {
			return env.entries[i].Value, true
		}
	}
	return nil, false
}

func (e *Environment) Define(name string, val Object, mutable bool) Object {
	return e.DefineTyped(name, val, mutable, nil)
}

func (e *Environment) DefineTyped(name string, val Object, mutable bool, typ *ast.TypeAnnotation) Object {
	b := Binding{Value: val, Mutable: mutable, Type: typ}
	if i := e.find(name); i >= 0 {
		e.entries[i].Binding = b
		return val
	}
	e.entries = append(e.entries, entry{name: name, Binding: b})
	switch {
	case e.index != nil:
		e.index[name] = len(e.entries) - 1
	case len(e.entries) > indexAfter:
		e.index = make(map[string]int, len(e.entries)*2)
		for i := range e.entries {
			e.index[e.entries[i].name] = i
		}
	}
	return val
}

// TypeMismatch is the error for a value that doesn't fit an annotation. what
// says who wanted the type, like "x expects" or "add returns"
func TypeMismatch(what string, typ *ast.TypeAnnotation, val Object) *Error {
	_, detail := misfit(typ, val)
	return &Error{Message: fmt.Sprintf("type mismatch: %s %s, got %s%s", what, typ, val.Type(), detail)}
}

func (e *Environment) Assign(name string, val Object) Object {
	for env := e; env != nil; env = env.outer {
		i := env.find(name)
		if i < 0 {
			continue
		}
		binding := &env.entries[i].Binding
		if !binding.Mutable {
			return &Error{Message: fmt.Sprintf("cannot assign to immutable binding %q", name)}
		}
		if binding.Type != nil && !Fits(binding.Type, val) {
			return TypeMismatch(name+" expects", binding.Type, val)
		}
		binding.Value = val
		return val
	}
	return &Error{Message: fmt.Sprintf("identifier not found: %s", name)}
}

// AssignWith is Assign for a value that only changed in part. cheap says
// whether the binding's type still holds without walking the whole value,
// when it can't tell the full check runs and names what doesn't fit
func (e *Environment) AssignWith(name string, val Object, cheap func(*ast.TypeAnnotation) bool) Object {
	for env := e; env != nil; env = env.outer {
		i := env.find(name)
		if i < 0 {
			continue
		}
		binding := &env.entries[i].Binding
		if !binding.Mutable {
			return &Error{Message: fmt.Sprintf("cannot assign to immutable binding %q", name)}
		}
		if binding.Type != nil && !cheap(binding.Type) && !Fits(binding.Type, val) {
			return TypeMismatch(name+" expects", binding.Type, val)
		}
		binding.Value = val
		return val
	}
	return &Error{Message: fmt.Sprintf("identifier not found: %s", name)}
}

func (e *Environment) ExportedBindings() map[string]Object {
	out := map[string]Object{}
	for _, en := range e.entries {
		out[en.name] = Escape(en.Value)
	}
	return out
}
