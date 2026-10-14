package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"undalang/builtins"
	"undalang/diag"
	"undalang/object"
	"undalang/semantic"
)

func analyze(t *testing.T, src string, opts semantic.Options) []string {
	t.Helper()
	prog := semantic.AnalyzeSource(src, "", t.TempDir(), opts)
	out := make([]string, 0, len(prog.Diagnostics))
	for _, d := range prog.Diagnostics {
		out = append(out, fmt.Sprintf("%d:%d %s", d.Line, d.Column, d.Message))
	}
	return out
}

func TestSemanticReportsErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{"undefined", "let a = 1\nprintln(b)", []string{"2:9 identifier not found: b"}},
		{"use before let", "println(x)\nlet x = 1", []string{"1:9 identifier not found: x"}},
		{"immutable assign", "let x = 1\nx = 2", []string{`2:1 cannot assign to immutable binding "x"`}},
		{"assign undefined", "y = 2", []string{"1:1 identifier not found: y"}},
		{"type mismatch", `let n = 1 + "a"`, []string{"1:11 type mismatch: INTEGER + STRING"}},
		{"string op", `"a" - "b"`, []string{"1:5 unknown operator: STRING - STRING"}},
		{"float modulo", "1.5 % 2", []string{"1:5 operator % only supports integers"}},
		{"negate string", `-"a"`, []string{"1:1 unknown operator: -STRING"}},
		{"not a function", "let x = 5\nx()", []string{"2:1 not a function: INTEGER"}},
		{"user arity", "fn add(a, b) { a + b }\nadd(1)", []string{"2:1 wrong number of arguments: expected 2, got 1"}},
		{"builtin arity", "len(1, 2)", []string{"1:1 len expects 1 argument, got 2"}},
		{"builtin arg type", `import "strings" as s
s.upper(5)`, []string{"2:3 strings.upper argument 1 must be STRING, got INTEGER"}},
		{"pipeline arity", `import "strings" as s
"a" |> s.split`, []string{"2:5 strings.split expects 2 arguments, got 1"}},
		{"unknown member", `import "strings" as s
s.missing`, []string{`2:2 module "strings" has no member "missing"`}},
		{"member on int", "let n = 1\nn.x", []string{"2:2 member access expects module, map or struct, got INTEGER"}},
		{"bad map key", "{[1]: 2}", []string{"1:1 unusable as map key: ARRAY"}},
		{"bad index", `[1]["a"]`, []string{"1:4 index operator not supported: ARRAY[STRING]"}},
		{"return type flows", "fn f() { return 1 }\nlet s = len(\"ab\") + \"x\"", []string{"2:19 type mismatch: INTEGER + STRING"}},
		{"inside function", "fn f() { return nope }", []string{"1:17 identifier not found: nope"}},
		{"several", "a\nb", []string{"1:1 identifier not found: a", "2:1 identifier not found: b"}},
		{"break outside loop", "break", []string{"1:1 break outside loop"}},
		{"continue in nested fn", "while true { let f = fn() { continue } }", []string{"1:29 continue outside loop"}},
		{"iterate int", "for x in 5 { x }", []string{"1:10 cannot iterate over INTEGER"}},
		{"loop var immutable", "for x in [1] { x = 2 }", []string{`1:16 cannot assign to immutable binding "x"`}},
		{"loop var scope", "for x in [1] { x }\nx", []string{"2:1 identifier not found: x"}},
		{"duplicate binding", "match [1, 2] { [a, a] -> a }", []string{"1:20 duplicate binding a in pattern"}},
		{"binding scope", "match 1 { n -> n }\nn", []string{"2:1 identifier not found: n"}},
		{"bad pattern key", "match {} { {[1]: v} -> v }", []string{"1:12 unusable as map key: ARRAY"}},
		{"duplicate loop var", "for x, x in [1] { x }", []string{"1:8 duplicate loop variable x"}},
		{"typed let", `let x: int = "a"`, []string{"1:1 type mismatch: x expects int, got STRING"}},
		{"typed assign", "let mut n: int = 1\nn = \"s\"", []string{"2:1 type mismatch: n expects int, got STRING"}},
		{"typed mut keeps type", "let mut n: int = 1\nn + \"s\"", []string{"2:3 type mismatch: INTEGER + STRING"}},
		{"typed param in body", `fn f(a: int) { a + "x" }`, []string{"1:18 type mismatch: INTEGER + STRING"}},
		{"typed param call", "fn f(a: int) { a }\nf(\"s\")", []string{"2:1 type mismatch: parameter a expects int, got STRING"}},
		{"typed param pipeline", "fn f(a: int) { a }\n\"s\" |> f", []string{"2:5 type mismatch: parameter a expects int, got STRING"}},
		{"typed return", `fn f() -> int { return "s" }`, []string{"1:17 type mismatch: f returns int, got STRING"}},
		{"typed implicit return", "fn f() -> string { 1 }", []string{"1:20 type mismatch: f returns string, got INTEGER"}},
		{"anonymous return", "let f = fn() -> int { nil }", []string{"1:23 type mismatch: function returns int, got NIL"}},
		{"try non-result", "fn f() { 1? }", []string{"1:11 ? expects RESULT, got INTEGER"}},
		{"try in typed fn", "fn f(r) -> int { r? }", []string{"1:19 type mismatch: f returns int, got RESULT"}},
		{"result pattern scope", "match ok(1) { ok(v) -> v }\nv", []string{"2:1 identifier not found: v"}},
		{"try builtin arg", "try_read_file(5)", []string{"1:1 try_read_file argument 1 must be STRING, got INTEGER"}},
		{"try builtin result", "try_read_file(\"x\") + 1", []string{"1:20 type mismatch: RESULT + INTEGER"}},
		{"ok arity", "ok(1, 2)", []string{"1:1 ok expects 1 argument, got 2"}},
		{"suggest name", "let counter = 1\ncountr + 1", []string{`2:1 identifier not found: countr (did you mean "counter"?)`}},
		{"suggest builtin", `lenn("x")`, []string{`1:1 identifier not found: lenn (did you mean "len"?)`}},
		{"suggest assign", "let mut total = 1\ntotl = 2", []string{`2:1 identifier not found: totl (did you mean "total"?)`}},
		{"suggest member", "import \"strings\" as s\ns.uper(\"a\")", []string{`2:2 module "strings" has no member "uper" (did you mean "upper"?)`}},
		{"no wild guesses", "let abc = 1\nxyz", []string{"2:1 identifier not found: xyz"}},
		{"struct arity", "struct P { a, b }\nP(1)", []string{"2:1 wrong number of arguments: expected 2, got 1"}},
		{"struct field type", "struct P { a: int }\nP(\"s\")", []string{"2:1 type mismatch: field a of P expects int, got STRING"}},
		{"struct unknown named", "struct P { name }\nP(nam: 1)", []string{`2:1 P has no field "nam" (did you mean "name"?)`}},
		{"struct missing named", "struct P { a, b }\nP(a: 1)", []string{"2:1 missing field b for P"}},
		{"struct twice named", "struct P { a }\nP(a: 1, a: 2)", []string{"2:1 field a given twice"}},
		{"struct unknown field", "struct P { a }\nlet p = P(1)\np.b", []string{`3:2 P has no field "b"`}},
		{"struct typed param", "struct P { a }\nstruct Q { a }\nfn f(p: P) { p.a }\nf(Q(1))", []string{"4:1 type mismatch: parameter p expects P, got Q"}},
		{"struct field flows", "struct P { a: int }\nlet p = P(1)\np.a + \"s\"", []string{"3:5 type mismatch: INTEGER + STRING"}},
		{"struct field assign", "struct P { a: int }\nlet mut p: P = P(1)\np.a = \"s\"", []string{"3:1 type mismatch: field a of P expects int, got STRING"}},
		{"struct field assign missing", "struct P { a }\nlet mut p: P = P(1)\np.b = 2", []string{`3:1 P has no field "b"`}},
		{"field assign immutable", "struct P { a }\nlet p = P(1)\np.a = 2", []string{`3:1 cannot assign to immutable binding "p"`}},
		{"field assign on int", "let mut n = 1\nlet mut k: int = 1\nk.x = 2", []string{"3:1 cannot set field x on INTEGER"}},
		{"unknown struct type", "struct Point { x }\nlet p: Pont = 1", []string{"2:1 type mismatch: p expects Pont, got INTEGER", `2:8 unknown type Pont (did you mean "Point"?)`}},
		{"named on function", "fn f(a) { a }\nf(a: 1)", []string{"2:1 named arguments only work with struct constructors, got FUNCTION"}},
		{"extern arg type", "extern \"m\" { fn cos(x: f64) -> f64 }\ncos(\"a\")", []string{"2:1 cos argument 1 must be INTEGER|FLOAT, got STRING"}},
		{"extern arity", "extern \"m\" { fn cos(x: f64) -> f64 }\ncos()", []string{"2:1 cos expects 1 argument, got 0"}},
		{"extern ptr arg", "extern \"c\" { fn fclose(f: ptr) -> i32 }\nfclose(1)", []string{"2:1 fclose argument 1 must be NIL|POINTER|BUFFER, got INTEGER"}},
		{"ptr annotation", "fn close(f: ptr) { f }\nclose(\"x\")", []string{"2:1 type mismatch: parameter f expects ptr, got STRING"}},
		{"no c layout", "struct P { a: int }\nextern \"c\" { fn f(p: P) -> i32 }", []string{"2:22 P has no C layout, declare it inside an extern block"}},
		{"c struct field types", "extern \"c\" { struct S { n: i32 } }\nS(\"x\")", []string{"2:1 type mismatch: field n of S expects int, got STRING"}},
		{"mem arg types", "import \"mem\" as mem\nmem.get_i32(1, 0)", []string{"2:5 mem.get_i32 argument 1 must be BUFFER, got INTEGER"}},
		{"mem result", "import \"mem\" as mem\nlet b = mem.alloc(4)\nmem.get_f32(b, 0) + \"s\"", []string{"3:19 type mismatch: FLOAT + STRING"}},
		{"extern result", "extern \"m\" { fn cos(x: f64) -> f64 }\ncos(1) + \"s\"", []string{"2:8 type mismatch: FLOAT + STRING"}},
		{"element literal", `let xs: array[int] = ["a"]`, []string{"1:1 type mismatch: xs expects array[int], got ARRAY"}},
		{"element loop var", "let xs: array[int] = [1]\nfor x in xs { x + \"s\" }", []string{"2:17 type mismatch: INTEGER + STRING"}},
		{"element literal loop", "for i, s in [\"a\"] { i + s }", []string{"1:23 type mismatch: INTEGER + STRING"}},
		{"map value type", `let m: map[string, int] = {"a": "b"}`, []string{"1:1 type mismatch: m expects map[string, int], got MAP"}},
		{"map key type", `let m: map[string, int] = {1: 2}`, []string{"1:1 type mismatch: m expects map[string, int], got MAP"}},
		{"element param", "fn sum(xs: array[int]) { xs }\nsum([1.5])", []string{"2:1 type mismatch: parameter xs expects array[int], got ARRAY"}},
		{"element struct", "struct Point { a }\nlet ps: array[Pointt] = []", []string{`2:15 unknown type Pointt (did you mean "Point"?)`}},
		{"empty body", "let f = fn() -> int {}", []string{"1:21 type mismatch: function returns int, got NIL"}},
		{"return type flows", "fn f() -> int { 1 }\nf() + \"x\"", []string{"2:5 type mismatch: INTEGER + STRING"}},
		{"redeclare typed", "let x: int = 1\nlet x = \"s\"", []string{"2:1 cannot redeclare x with a different type"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := analyze(t, tt.src, semantic.Options{})
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Fatalf("diagnostics mismatch\ngot:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
			}
		})
	}
}

func TestSemanticAcceptsDynamicCode(t *testing.T) {
	tests := map[string]string{
		"forward reference":  "fn a() { b() }\nfn b() { 1 }\na()",
		"mutable rebinding":  "let mut x = 1\nx = \"now a string\"\nx + \"!\"",
		"redeclare":          "let x = 1\nlet x = \"s\"\nx + \"t\"",
		"unknown params":     "fn f(a, b) { a + b }\nf(1, 2)\nf(\"a\", \"b\")",
		"closure assign":     "let mut n = 0\nlet inc = fn() { n = n + 1 }\ninc()",
		"builtin shadow":     "let len = fn(a, b) { a }\nlen(1, 2)",
		"mixed numbers":      "1 + 2.5 == 3.5",
		"logical ops":        "true && 1\nnil || \"x\"",
		"param shadows":      "fn f(x) { let x = x + 1\nx }",
		"match and if":       "let v = match 1 { 1 -> \"a\", _ -> 2 }\nif v { 1 } else { 2 }",
		"loops":              "let mut n = 0\nwhile n < 3 { n = n + 1\nif n == 2 { continue }\nbreak }\nfor i, c in \"ab\" { n = n + i }",
		"patterns":           "match [1, {\"a\": 2}] { [x, {\"a\": y}] if x < y -> x + y, [_, ..rest] -> rest, _ -> nil }",
		"map members":        "let m = {\"name\": \"unda\"}\nm.name\nm.missing",
		"comments":           "// header\nlet a = 1 // trailing\n// footer",
		"number type":        "fn half(n: number) -> float { n / 2.0 }\nhalf(1)\nhalf(2.5)",
		"any type":           "let mut v: any = 1\nv = \"s\"\nv = nil",
		"fn type":            "fn apply(f: fn, v) { f(v) }\napply(len, \"ab\")\napply(fn(x) { x }, 1)",
		"same type again":    "let x: int = 1\nlet x: int = 2",
		"unknown into typed": "fn f(v) { v }\nlet n: int = f(1)",
		"typed closures":     "let mut n: int = 0\nlet inc = fn() { n = n + 1 }\ninc()",
		"results":            "fn f(r) -> result { let v = r?\nok(v + 1) }\nmatch f(ok(1)) { ok(n) -> n, err(e) -> e }",
		"err as a name":      "let err = 1\nmatch 2 { err -> err }",
		"element types":      "let xs: array[int] = [1, 2]\nlet first = xs[0]\nlet m: map[string, array[int]] = {\"a\": xs}\nfor k, v in m { v }\nlet e: array[string] = []\nlet mixed: array[any] = [1, \"a\"]",
		"returns in loops":   "fn first(xs) -> any { for x in xs { return x } }",
	}
	for name, src := range tests {
		t.Run(name, func(t *testing.T) {
			if got := analyze(t, src, semantic.Options{}); len(got) != 0 {
				t.Fatalf("unexpected diagnostics: %v", got)
			}
		})
	}
}

func TestSemanticNativeOnlyChecks(t *testing.T) {
	src := "import \"time\" as time\ntime.format(time.now(), \"2006\")"
	if got := analyze(t, src, semantic.Options{}); len(got) != 0 {
		t.Fatalf("interpreter mode should accept time.format, got %v", got)
	}
	got := analyze(t, src, semantic.Options{Native: true})
	want := "2:5 time.format is not supported by the C backend yet"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %v, want %q", got, want)
	}
}

func TestSemanticImports(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lib.unda"), []byte("fn two(a, b) { a }\nlet n = 1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cycle_a.unda"), []byte(`import "cycle_b" as b`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cycle_b.unda"), []byte(`import "cycle_a" as a`), 0644); err != nil {
		t.Fatal(err)
	}
	src := "import \"lib\" as lib\nlib.two(1)\nlib.nope\nimport \"missing\" as m"
	prog := semantic.AnalyzeSource(src, "", dir, semantic.Options{})
	var got []string
	for _, d := range prog.Diagnostics {
		got = append(got, fmt.Sprintf("%d:%d %s", d.Line, d.Column, d.Message))
	}
	if len(got) != 3 ||
		got[0] != "2:5 wrong number of arguments: expected 2, got 1" ||
		got[1] != `3:4 module "lib" has no member "nope"` ||
		!strings.HasPrefix(got[2], `4:1 import "missing" failed:`) {
		t.Fatalf("unexpected diagnostics: %q", got)
	}

	prog = semantic.AnalyzeFile(filepath.Join(dir, "cycle_a.unda"), semantic.Options{})
	if len(prog.Diagnostics) != 1 || prog.Diagnostics[0].Message != `import cycle detected at "cycle_a"` {
		t.Fatalf("expected cycle diagnostic, got %v", prog.Diagnostics)
	}
}

// the signature table has to cover exactly the builtins the interpreter has
func TestSemanticSignaturesMatchBuiltins(t *testing.T) {
	for name := range builtins.New(nil) {
		if _, ok := semantic.GlobalBuiltins[name]; !ok {
			t.Errorf("global builtin %s has no signature", name)
		}
	}
	if len(builtins.New(nil)) != len(semantic.GlobalBuiltins) {
		t.Errorf("signature table has extra globals")
	}
	apply := func(object.Object, []object.Object) object.Object { return nil }
	for modName, mod := range builtins.StdModules(apply) {
		sigs, ok := semantic.StdModules[modName]
		if !ok {
			t.Errorf("std module %s has no signatures", modName)
			continue
		}
		for member := range mod.Members {
			if _, ok := sigs[member]; !ok {
				t.Errorf("%s.%s has no signature", modName, member)
			}
		}
		if len(sigs) != len(mod.Members) {
			t.Errorf("signature table for %s has extra members", modName)
		}
	}
}

func TestDiagnosticFormat(t *testing.T) {
	d := diag.Diagnostic{File: "main.unda", Line: 2, Column: 9, Message: "identifier not found: y"}
	got := diag.Format(d, "let x = 1\nlet z = y + 1\n")
	want := "main.unda:2:9: error: identifier not found: y\n  2 | let z = y + 1\n    |         ^\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRuntimeErrorsCarryPosition(t *testing.T) {
	evaluated, _ := testEval(t, "let a = 1\nlet b = a + true")
	errObj, ok := evaluated.(*object.Error)
	if !ok {
		t.Fatalf("expected error, got %T", evaluated)
	}
	if errObj.Line != 2 || errObj.Column != 11 {
		t.Fatalf("wrong position %d:%d", errObj.Line, errObj.Column)
	}
	if errObj.Inspect() != "runtime error: line 2:11: type mismatch: INTEGER + BOOLEAN" {
		t.Fatalf("wrong message %q", errObj.Inspect())
	}
}

func TestEvalTruthinessOfBuiltinResults(t *testing.T) {
	evaluated, _ := testEval(t, `import "strings" as strings
if strings.contains("a", "b") { "truthy" } else { "falsy" }`)
	testExpectedObject(t, evaluated, "falsy")
	evaluated, _ = testEval(t, `import "strings" as strings
!strings.contains("a", "b")`)
	testExpectedObject(t, evaluated, true)
}

func TestEvalCallDepthLimit(t *testing.T) {
	evaluated, _ := testEval(t, "fn f(n) { f(n + 1) }\nf(0)")
	errObj, ok := evaluated.(*object.Error)
	if !ok || errObj.Message != "maximum call depth exceeded" {
		t.Fatalf("expected depth error, got %s", evaluated.Inspect())
	}
}
