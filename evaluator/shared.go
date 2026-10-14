package evaluator

import (
	"undalang/ast"
	"undalang/object"
)

// these are the interpreter's semantics for the other Go backend, the VM.
// sharing the code instead of copying it keeps the two from drifting

func Infix(op string, left, right object.Object) object.Object {
	return evalInfixExpression(op, left, right)
}

func Prefix(op string, right object.Object) object.Object { return evalPrefixExpression(op, right) }

func Index(left, index object.Object) object.Object { return evalIndexExpression(left, index) }

func Member(obj object.Object, name string) object.Object { return evalMemberExpression(obj, name) }

func Truthy(obj object.Object) bool { return isTruthy(obj) }

func Equal(left, right object.Object) bool { return objectsEqual(left, right) }

func WithFields(obj object.Object, fields []*ast.Identifier, v object.Object) object.Object {
	return withFields(obj, fields, v)
}

func ApplyNamed(fn object.Object, args []object.Object, names []*ast.Identifier) object.Object {
	return applyNamed(fn, args, names)
}

func Bool(b bool) *object.Boolean { return nativeBool(b) }

// Nil is the interpreter's nil, shared so identity checks keep working
var Nil object.Object = nilObj

func Errorf(format string, a ...interface{}) *object.Error { return newError(format, a...) }

// IsAbrupt reports values that end evaluation early
func IsAbrupt(obj object.Object) bool { return isAbrupt(obj) }
