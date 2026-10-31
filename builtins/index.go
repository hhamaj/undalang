package builtins

import (
	"fmt"

	"undalang/object"
)

// Index is xs[i], s[i] and m[k]. reading past the end of an array or a
// string is a bug and stops the program, a missing map key gives nil.
// try_get is the try_ twin: the same lookup, with a miss as err(message).
// the C runtime's u_index is the native copy, keep the messages in step

// Index evaluates left[index], a failure is a runtime error
func Index(left, index object.Object) object.Object {
	v, fail := lookup("", left, index)
	if fail != "" {
		return errorf("%s", fail)
	}
	return v
}

// lookup returns the element, or the failure message for a miss. a missing
// map key only counts as a miss for try_get (name != "")
func lookup(name string, left, index object.Object) (object.Object, string) {
	prefix := ""
	if name != "" {
		prefix = name + ": "
	}
	switch l := left.(type) {
	case *object.Array:
		i, ok := index.(*object.Integer)
		if !ok {
			break
		}
		if i.Value < 0 || i.Value >= int64(len(l.Elements)) {
			return nil, fmt.Sprintf("%sindex %d out of range for an array of length %d", prefix, i.Value, len(l.Elements))
		}
		return l.Elements[i.Value], ""
	case *object.String:
		i, ok := index.(*object.Integer)
		if !ok {
			break
		}
		// strings index by character (code point) not byte
		n := int64(0)
		for _, r := range l.Value {
			if n == i.Value {
				return &object.String{Value: string(r)}, ""
			}
			n++
		}
		return nil, fmt.Sprintf("%sindex %d out of range for a string of length %d", prefix, i.Value, n)
	case *object.Map:
		key, ok := index.(object.Hashable)
		if !ok {
			return errorf("unusable as map key: %s", index.Type()), ""
		}
		if pair, ok := l.Pairs[key.HashKey()]; ok {
			return pair.Value, ""
		}
		if name != "" {
			return nil, prefix + "key not found: " + index.Inspect()
		}
		return &object.Nil{}, ""
	}
	return errorf("index operator not supported: %s[%s]", left.Type(), index.Type()), ""
}

func tryGet(name string, args []object.Object) (object.Object, string) {
	if len(args) != 2 {
		return errorf("%s expects 2 arguments, got %d", name, len(args)), ""
	}
	switch args[0].(type) {
	case *object.Array, *object.String, *object.Map:
		return lookup(name, args[0], args[1])
	}
	return errorf("%s expects ARRAY, STRING or MAP, got %s", name, args[0].Type()), ""
}
