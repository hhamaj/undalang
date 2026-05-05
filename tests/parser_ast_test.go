package tests

import (
	"testing"

	"ranalang/ast"
	"ranalang/lexer"
	"ranalang/parser"
	"ranalang/token"
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
	}

	for _, tt := range tests {
		program := parse(t, tt.input)
		if program.String() != tt.expected {
			t.Fatalf("expected=%q, got=%q", tt.expected, program.String())
		}
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
