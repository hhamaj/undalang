package tests

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"undalang/evaluator"
	"undalang/lexer"
	"undalang/object"
	"undalang/parser"
)

func TestEvalExpressions(t *testing.T) {
	tests := []struct {
		input    string
		expected interface{}
	}{
		{"5", int64(5)},
		{"5.5", 5.5},
		{`"hello" + " " + "unda"`, "hello unda"},
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

func TestEvalArraysMapsMatchAndPipeline(t *testing.T) {
	input := `import "strings" as strings
import "arrays" as arrays

fn bang(s) { return s + "!" }

let names = "unda,go" |> strings.split(",") |> arrays.map(bang)
let data = {"first": names[0], "count": len(names)}
match data["count"] {
    2 -> data["first"],
    _ -> "bad",
}`

	evaluated, _ := testEval(t, input)
	testExpectedObject(t, evaluated, "unda!")
}

func TestEvalMapUtilitiesAndJSON(t *testing.T) {
	input := `import "json" as json
import "maps" as maps

let parsed = json.parse("{\"name\":\"unda\",\"items\":[1,2]}")
let updated = maps.set(parsed, "ok", true)
json.stringify({"name": updated["name"], "ok": maps.has(updated, "ok"), "second": updated["items"][1]})`

	evaluated, _ := testEval(t, input)
	testExpectedObject(t, evaluated, `{"name":"unda","ok":true,"second":2}`)
}

func TestEvalLocalImportAndFileIO(t *testing.T) {
	dir := t.TempDir()
	modulePath := filepath.Join(dir, "util.unda")
	if err := os.WriteFile(modulePath, []byte(`fn greet(name) { return "hi " + name }
let version = 1`), 0644); err != nil {
		t.Fatal(err)
	}
	dataPath := filepath.Join(dir, "out.txt")

	input := `import "util" as util
write_file(` + strconv.Quote(dataPath) + `, util.greet("unda"))
read_file(` + strconv.Quote(dataPath) + `) + ":" + type(util.version)`

	evaluated, _ := testEvalInDir(t, input, dir)
	testExpectedObject(t, evaluated, "hi unda:integer")
}

func TestEvalPhase1RuntimeErrors(t *testing.T) {
	tests := []string{
		`[1]["bad"]`,
		`{"x": 1}[fn() { 1 }]`,
		`import "strings" as s
s.missing`,
	}
	for _, input := range tests {
		evaluated, _ := testEval(t, input)
		if evaluated.Type() != object.ErrorObj {
			t.Fatalf("expected error for %q, got=%s", input, evaluated.Type())
		}
	}
}

func TestEvalStringsAreCharacterBased(t *testing.T) {
	evaluated, _ := testEval(t, `len("çay")`)
	testExpectedObject(t, evaluated, int64(3))
	evaluated, _ = testEval(t, `"ğüş"[1]`)
	testExpectedObject(t, evaluated, "ü")
	evaluated, _ = testEval(t, `"ab"[2]`)
	testExpectedObject(t, evaluated, nil)
}

func testEval(t *testing.T, input string) (object.Object, *bytes.Buffer) {
	return testEvalInDir(t, input, "")
}

func testEvalInDir(t *testing.T, input string, baseDir string) (object.Object, *bytes.Buffer) {
	t.Helper()
	l := lexer.New(input)
	p := parser.New(l)
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parser errors: %v", p.Errors())
	}

	env := object.NewEnvironment()
	out := &bytes.Buffer{}
	return evaluator.EvalWithContext(program, env, evaluator.NewContext(baseDir, out)), out
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
