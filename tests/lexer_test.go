package tests

import (
	"testing"

	"undalang/lexer"
	"undalang/token"
)

func TestLexerTokens(t *testing.T) {
	input := `let mut x = 10
let y = 20.5
println("hi", x)
if x <= y && true || false {
    return nil
}
x = x + 1 - 2 * 3 / 4 % 5
!x != y == 9 >= 8 < 7 > 6
import "strings" as s
[1, 2] |> s.join(",")
match x { _ -> {"ok": true}[ "ok" ] }`

	tests := []struct {
		expectedType    token.Type
		expectedLiteral string
	}{
		{token.Let, "let"},
		{token.Mut, "mut"},
		{token.Ident, "x"},
		{token.Assign, "="},
		{token.Int, "10"},
		{token.Let, "let"},
		{token.Ident, "y"},
		{token.Assign, "="},
		{token.Float, "20.5"},
		{token.Ident, "println"},
		{token.LParen, "("},
		{token.String, "hi"},
		{token.Comma, ","},
		{token.Ident, "x"},
		{token.RParen, ")"},
		{token.If, "if"},
		{token.Ident, "x"},
		{token.LTE, "<="},
		{token.Ident, "y"},
		{token.And, "&&"},
		{token.True, "true"},
		{token.Or, "||"},
		{token.False, "false"},
		{token.LBrace, "{"},
		{token.Return, "return"},
		{token.Nil, "nil"},
		{token.RBrace, "}"},
		{token.Ident, "x"},
		{token.Assign, "="},
		{token.Ident, "x"},
		{token.Plus, "+"},
		{token.Int, "1"},
		{token.Minus, "-"},
		{token.Int, "2"},
		{token.Asterisk, "*"},
		{token.Int, "3"},
		{token.Slash, "/"},
		{token.Int, "4"},
		{token.Percent, "%"},
		{token.Int, "5"},
		{token.Bang, "!"},
		{token.Ident, "x"},
		{token.NotEq, "!="},
		{token.Ident, "y"},
		{token.Eq, "=="},
		{token.Int, "9"},
		{token.GTE, ">="},
		{token.Int, "8"},
		{token.LT, "<"},
		{token.Int, "7"},
		{token.GT, ">"},
		{token.Int, "6"},
		{token.Import, "import"},
		{token.String, "strings"},
		{token.As, "as"},
		{token.Ident, "s"},
		{token.LBracket, "["},
		{token.Int, "1"},
		{token.Comma, ","},
		{token.Int, "2"},
		{token.RBracket, "]"},
		{token.Pipe, "|>"},
		{token.Ident, "s"},
		{token.Dot, "."},
		{token.Ident, "join"},
		{token.LParen, "("},
		{token.String, ","},
		{token.RParen, ")"},
		{token.Match, "match"},
		{token.Ident, "x"},
		{token.LBrace, "{"},
		{token.Ident, "_"},
		{token.Arrow, "->"},
		{token.LBrace, "{"},
		{token.String, "ok"},
		{token.Colon, ":"},
		{token.True, "true"},
		{token.RBrace, "}"},
		{token.LBracket, "["},
		{token.String, "ok"},
		{token.RBracket, "]"},
		{token.RBrace, "}"},
		{token.EOF, ""},
	}

	l := lexer.New(input)
	for i, tt := range tests {
		tok := l.NextToken()
		if tok.Type != tt.expectedType {
			t.Fatalf("tests[%d] token type wrong. expected=%q, got=%q", i, tt.expectedType, tok.Type)
		}
		if tok.Literal != tt.expectedLiteral {
			t.Fatalf("tests[%d] literal wrong. expected=%q, got=%q", i, tt.expectedLiteral, tok.Literal)
		}
	}
}

func TestLexerNumbersCommentsAndStrings(t *testing.T) {
	input := `1e5 2.5e-3 3E+2 7 1.5 1e x // comment "ignored"
1..3 "open`
	tests := []struct {
		typ     token.Type
		literal string
	}{
		{token.Float, "1e5"}, {token.Float, "2.5e-3"}, {token.Float, "3E+2"},
		{token.Int, "7"}, {token.Float, "1.5"}, {token.Int, "1"}, {token.Ident, "e"},
		{token.Ident, "x"}, {token.Int, "1"}, {token.DotDot, ".."}, {token.Int, "3"},
		{token.Illegal, `"open`}, {token.EOF, ""},
	}
	l := lexer.New(input)
	for i, tt := range tests {
		tok := l.NextToken()
		if tok.Type != tt.typ || tok.Literal != tt.literal {
			t.Fatalf("token %d: got %s %q, want %s %q", i, tok.Type, tok.Literal, tt.typ, tt.literal)
		}
	}
}
