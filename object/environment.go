package object

import "fmt"

type Binding struct {
	Value   Object
	Mutable bool
}

type Environment struct {
	store map[string]Binding
	outer *Environment
}

func NewEnvironment() *Environment {
	return &Environment{store: make(map[string]Binding)}
}

func NewEnclosedEnvironment(outer *Environment) *Environment {
	env := NewEnvironment()
	env.outer = outer
	return env
}

func (e *Environment) Get(name string) (Object, bool) {
	binding, ok := e.store[name]
	if !ok && e.outer != nil {
		return e.outer.Get(name)
	}
	return binding.Value, ok
}

func (e *Environment) Define(name string, val Object, mutable bool) Object {
	e.store[name] = Binding{Value: val, Mutable: mutable}
	return val
}

func (e *Environment) Assign(name string, val Object) Object {
	if binding, ok := e.store[name]; ok {
		if !binding.Mutable {
			return &Error{Message: fmt.Sprintf("cannot assign to immutable binding %q", name)}
		}
		e.store[name] = Binding{Value: val, Mutable: true}
		return val
	}
	if e.outer != nil {
		return e.outer.Assign(name, val)
	}
	return &Error{Message: fmt.Sprintf("identifier not found: %s", name)}
}
