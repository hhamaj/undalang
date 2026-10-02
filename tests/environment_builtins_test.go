package tests

import (
	"bytes"
	"testing"

	"undalang/builtins"
	"undalang/object"
)

func TestEnvironmentScopesAndMutability(t *testing.T) {
	outer := object.NewEnvironment()
	outer.Define("x", &object.Integer{Value: 1}, true)
	outer.Define("y", &object.Integer{Value: 2}, false)

	inner := object.NewEnclosedEnvironment(outer)
	inner.Define("z", &object.Integer{Value: 3}, false)

	if got, ok := inner.Get("x"); !ok || got.(*object.Integer).Value != 1 {
		t.Fatalf("expected inner to read outer x")
	}
	if got := inner.Assign("x", &object.Integer{Value: 4}); got.Type() == object.ErrorObj {
		t.Fatalf("expected mutable outer assignment to work: %s", got.Inspect())
	}
	if got, _ := outer.Get("x"); got.(*object.Integer).Value != 4 {
		t.Fatalf("outer x not updated")
	}
	if got := inner.Assign("y", &object.Integer{Value: 5}); got.Type() != object.ErrorObj {
		t.Fatalf("expected immutable assignment error")
	}
	if _, ok := outer.Get("z"); ok {
		t.Fatalf("inner binding leaked to outer")
	}
}

func TestBuiltins(t *testing.T) {
	out := &bytes.Buffer{}
	b := builtins.New(out)

	b["print"].Fn(&object.String{Value: "hi"}, &object.Integer{Value: 3})
	if out.String() != "hi 3" {
		t.Fatalf("print output wrong: %q", out.String())
	}

	lenObj := b["len"].Fn(&object.String{Value: "unda"})
	if lenObj.(*object.Integer).Value != 4 {
		t.Fatalf("len result wrong: %s", lenObj.Inspect())
	}

	typeObj := b["type"].Fn(&object.Float{Value: 1.2})
	if typeObj.(*object.String).Value != "float" {
		t.Fatalf("type result wrong: %s", typeObj.Inspect())
	}
}
