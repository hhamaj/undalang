package tests

import (
	"bytes"
	"testing"

	"ranalang/evaluator"
	"ranalang/lexer"
	"ranalang/object"
	"ranalang/parser"
)

func TestEvalExpressions(t *testing.T) {
	tests := []struct {
		input    string
		expected interface{}
	}{
		{"5", int64(5)},
		{"5.5", 5.5},
		{`"hello" + " " + "rana"`, "hello rana"},
		{"true == !false", true},
		{"1 + 2 * 3 == 7", true},
		{"10 / 2 + 3", int64(8)},
		{"5 % 2", int64(1)},
		{"if 1 < 2 { 10 } else { 20 }", int64(10)},
		{"nil == nil", true},
	}

	for _, tt := range tests {
		evaluated, _ := testEval(t, tt.input)
		testExpectedObject(t, evaluated, tt.expected)
	}
}

func TestEvalLetMutAndAssignment(t *testing.T) {
	evaluated, _ := testEval(t, `let mut x = 5
x = x + 2
x`)
	testExpectedObject(t, evaluated, int64(7))

	evaluated, _ = testEval(t, `let x = 5
x = 6`)
	errObj, ok := evaluated.(*object.Error)
	if !ok {
		t.Fatalf("expected error, got=%T (%s)", evaluated, evaluated.Inspect())
	}
	if errObj.Message != `cannot assign to immutable binding "x"` {
		t.Fatalf("wrong error msg: %q", errObj.Message)
	}
}

func TestEvalFunctionsAndClosures(t *testing.T) {
	input := `let base = 10
fn add(a, b) {
    return a + b + base
}
add(2, 3)`

	evaluated, _ := testEval(t, input)
	testExpectedObject(t, evaluated, int64(15))
}

func TestEvalRuntimeErrorsDoNotPanic(t *testing.T) {
	evaluated, _ := testEval(t, `5 + true`)
	if evaluated.Type() != object.ErrorObj {
		t.Fatalf("expected error object, got=%s", evaluated.Type())
	}
}

func TestEvalPrintlnBuiltin(t *testing.T) {
	evaluated, out := testEval(t, `println("Hello", "Hamza")`)
	testExpectedObject(t, evaluated, nil)
	if out.String() != "Hello Hamza\n" {
		t.Fatalf("wrong output: %q", out.String())
	}
}

func testEval(t *testing.T, input string) (object.Object, *bytes.Buffer) {
	t.Helper()
	l := lexer.New(input)
	p := parser.New(l)
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parser errors: %v", p.Errors())
	}

	env := object.NewEnvironment()
	out := &bytes.Buffer{}
	return evaluator.Eval(program, env, out), out
}

func testExpectedObject(t *testing.T, obj object.Object, expected interface{}) {
	t.Helper()
	switch expected := expected.(type) {
	case int64:
		result, ok := obj.(*object.Integer)
		if !ok {
			t.Fatalf("object is not Integer. got=%T (%+v)", obj, obj)
		}
		if result.Value != expected {
			t.Fatalf("object has wrong value. got=%d, want=%d", result.Value, expected)
		}
	case float64:
		result, ok := obj.(*object.Float)
		if !ok {
			t.Fatalf("object is not Float. got=%T (%+v)", obj, obj)
		}
		if result.Value != expected {
			t.Fatalf("object has wrong value. got=%g, want=%g", result.Value, expected)
		}
	case bool:
		result, ok := obj.(*object.Boolean)
		if !ok {
			t.Fatalf("object is not Boolean. got=%T (%+v)", obj, obj)
		}
		if result.Value != expected {
			t.Fatalf("object has wrong value. got=%t, want=%t", result.Value, expected)
		}
	case string:
		result, ok := obj.(*object.String)
		if !ok {
			t.Fatalf("object is not String. got=%T (%+v)", obj, obj)
		}
		if result.Value != expected {
			t.Fatalf("object has wrong value. got=%q, want=%q", result.Value, expected)
		}
	case nil:
		if obj.Type() != object.NilObj {
			t.Fatalf("object is not nil. got=%s", obj.Type())
		}
	}
}
