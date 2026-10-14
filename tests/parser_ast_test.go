package tests

import (
	"strings"
	"testing"

	"undalang/ast"
	"undalang/lexer"
	"undalang/parser"
	"undalang/token"
)

func TestASTString(t *testing.T) {
	program := &ast.Program{
		Statements: []ast.Statement{
			&ast.LetStatement{
				Token: token.Token{Type: token.Let, Literal: "let"},
				Name:  &ast.Identifier{Token: token.Token{Type: token.Ident, Literal: "x"}, Value: "x"},
				Value: &ast.IntegerLiteral{Token: token.Token{Type: token.Int, Literal: "5"}, Value: 5},
			},
		},
	}

	if program.String() != "let x = 5" {
		t.Fatalf("program.String() wrong. got=%q", program.String())
	}
}

func TestParserLetAndMutStatements(t *testing.T) {
	program := parse(t, `let x = 5
let mut y = 10`)

	if len(program.Statements) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(program.Statements))
	}

	first, ok := program.Statements[0].(*ast.LetStatement)
	if !ok {
		t.Fatalf("stmt 0 not *ast.LetStatement. got=%T", program.Statements[0])
	}
	if first.Name.Value != "x" || first.Mutable {
		t.Fatalf("bad first let stmt: name=%q mutable=%t", first.Name.Value, first.Mutable)
	}

	second, ok := program.Statements[1].(*ast.LetStatement)
	if !ok {
		t.Fatalf("stmt 1 not *ast.LetStatement. got=%T", program.Statements[1])
	}
	if second.Name.Value != "y" || !second.Mutable {
		t.Fatalf("bad second let stmt: name=%q mutable=%t", second.Name.Value, second.Mutable)
	}
}

func TestParserNamedFunctionStatement(t *testing.T) {
	program := parse(t, `fn add(a, b) { return a + b }`)
	if len(program.Statements) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(program.Statements))
	}

	stmt, ok := program.Statements[0].(*ast.LetStatement)
	if !ok {
		t.Fatalf("stmt not *ast.LetStatement. got=%T", program.Statements[0])
	}
	fn, ok := stmt.Value.(*ast.FunctionLiteral)
	if !ok {
		t.Fatalf("value not *ast.FunctionLiteral. got=%T", stmt.Value)
	}
	if stmt.Name.Value != "add" || fn.Name != "add" {
		t.Fatalf("bad fn name: stmt=%q fn=%q", stmt.Name.Value, fn.Name)
	}
	if len(fn.Parameters) != 2 {
		t.Fatalf("expected 2 params, got %d", len(fn.Parameters))
	}
}

func TestParserOperatorPrecedence(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"-a * b", "((-a) * b)"},
		{"a + b * c + d", "((a + (b * c)) + d)"},
		{"a < b == c > d", "((a < b) == (c > d))"},
		{"a || b && c", "(a || (b && c))"},
		{"add(1, 2 * 3, 4 + 5)", "add(1, (2 * 3), (4 + 5))"},
		{`[1, 2 + 3][0]`, "([1, (2 + 3)][0])"},
		{`{"a": 1, "b": 2}["a"]`, `({"a": 1, "b": 2}["a"])`},
		{`"a,b" |> strings.split(",") |> arrays.join("-")`, `(("a,b" |> strings.split(",")) |> arrays.join("-"))`},
	}

	for _, tt := range tests {
		program := parse(t, tt.input)
		if program.String() != tt.expected {
			t.Fatalf("expected=%q, got=%q", tt.expected, program.String())
		}
	}
}

func TestParserPhase1Forms(t *testing.T) {
	program := parse(t, `import "strings" as s
match "ok" {
    "ok" -> s.upper("yes"),
    _ -> "no",
}`)
	if len(program.Statements) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(program.Statements))
	}
	importStmt, ok := program.Statements[0].(*ast.ImportStatement)
	if !ok {
		t.Fatalf("stmt 0 not *ast.ImportStatement. got=%T", program.Statements[0])
	}
	if importStmt.Path.Value != "strings" || importStmt.Alias.Value != "s" {
		t.Fatalf("bad import stmt: %s", importStmt.String())
	}
	exprStmt, ok := program.Statements[1].(*ast.ExpressionStatement)
	if !ok {
		t.Fatalf("stmt 1 not *ast.ExpressionStatement. got=%T", program.Statements[1])
	}
	match, ok := exprStmt.Expression.(*ast.MatchExpression)
	if !ok {
		t.Fatalf("expression not *ast.MatchExpression. got=%T", exprStmt.Expression)
	}
	if _, wildcard := match.Arms[len(match.Arms)-1].Pattern.(*ast.WildcardPattern); len(match.Arms) != 2 || !wildcard {
		t.Fatalf("bad match arms: %+v", match.Arms)
	}
}

func parse(t *testing.T, input string) *ast.Program {
	t.Helper()
	l := lexer.New(input)
	p := parser.New(l)
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parser errors: %v", p.Errors())
	}
	return program
}

func TestParserTypeAnnotations(t *testing.T) {
	tests := map[string]string{
		"let x: int = 5":                                    "let x: int = 5",
		"let mut s: string = \"a\"":                         `let mut s: string = "a"`,
		"fn add(a: int, b) -> int { a }":                    "let add = fn add(a: int, b) -> int a",
		"let f = fn(x: nil, y: fn) -> any { x }":            "let f = fn(x: nil, y: fn) -> any x",
		"let g = fn() -> number { 1 }":                      "let g = fn() -> number 1",
		"match 1 { n -> fn(v: float) -> float { v } }":      "match 1 { n -> fn(v: float) -> float v }",
		"let v = f(x)? + m.k?":                              "let v = ((f(x)?) + (m.k?))",
		"match r { ok(v) -> v, err(_) -> 0 }":               "match r { ok(v) -> v, err(_) -> 0 }",
		"match r { ok([a, ..]) if a -> a, err -> 1 }":       "match r { ok([a, ..]) if a -> a, err -> 1 }",
		"fn f() -> result { ok(1) }":                        "let f = fn f() -> result ok(1)",
		"struct P {\n  x: int\n  y\n}":                      "struct P { x: int, y }",
		"let p: P = P(y: 1, x: 2)":                          "let p: P = P(y: 1, x: 2)",
		"p.a.b = 3":                                         "p.a.b = 3",
		"let xs: array[int] = []":                           "let xs: array[int] = []",
		"fn f(m: map[string, array[Point]]) -> array { m }": "let f = fn f(m: map[string, array[Point]]) -> array m",
	}
	for input, want := range tests {
		program := parse(t, input)
		if got := program.String(); got != want {
			t.Errorf("%q:\ngot  %q\nwant %q", input, got, want)
		}
	}
}

func TestParserReportsOneErrorPerStatement(t *testing.T) {
	tests := map[string][]string{
		"println(1 +)":            {`line 1:12 unexpected ")"`},
		"fn f( { }":               {"line 1:9 expected next token to be ), got } instead"},
		"let s = \"abc":           {"line 1:9 unterminated string literal"},
		"let x = @":               {`line 1:9 unexpected character "@"`},
		"let = 5\nlet y = 2\nz +": {"line 1:5 expected next token to be IDENT, got = instead", "line 4:1 unexpected end of input"},
		"let x: foo = 1":          {`line 1:8 unknown type "foo"`},
		"struct point { x }":      {`line 1:8 struct names start with an uppercase letter, got "point"`},
		"struct P { a, a }":       {"line 1:15 duplicate field a in P"},
		"struct STRING { a }":     {"line 1:8 STRING is a builtin type name"},
		"1 |> P(a: 2)":            {"line 1:12 named arguments can't follow |>, the piped value has no name"},
		"f(1).x = 2":              {"line 1:6 can only assign to fields of a named value"},
		"extern \"m\" { fn cos(x: double) -> f64 }": {`line 1:24 unknown C type "double" (did you mean "f64"?)`},
		"extern \"m\" { fn cos(x) }":                {"line 1:22 expected next token to be :, got ) instead"},
		"let m: map[float, int] = {}":               {`line 1:22 map keys can't be float, use int, string, bool, nil or any`},
		"let m: array[int = 1":                      {"line 1:18 expected next token to be ], got = instead"},
		"let x: integer = 1":                        {`line 1:8 unknown type "integer" (did you mean "int"?)`},
		"let x: strin = \"\"":                       {`line 1:8 unknown type "strin" (did you mean "string"?)`},
		"fn f(a: ) { a }":                           {"line 1:9 expected next token to be IDENT, got ) instead"},
		"fn f() -> { 1 }":                           {"line 1:11 expected next token to be IDENT, got { instead"},
	}
	for input, want := range tests {
		p := parser.New(lexer.New(input + "\n"))
		p.ParseProgram()
		if strings.Join(p.Errors(), "\n") != strings.Join(want, "\n") {
			t.Errorf("%q:\ngot  %q\nwant %q", input, p.Errors(), want)
		}
	}
}
