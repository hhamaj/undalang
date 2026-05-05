package token

type Type string

type Token struct {
	Type    Type
	Literal string
	Line    int
	Column  int
}

const (
	Illegal = "ILLEGAL"
	EOF     = "EOF"

	Ident  = "IDENT"
	Int    = "INT"
	Float  = "FLOAT"
	String = "STRING"

	Assign   = "="
	Plus     = "+"
	Minus    = "-"
	Bang     = "!"
	Asterisk = "*"
	Slash    = "/"
	Percent  = "%"

	LT = "<"
	GT = ">"

	Eq    = "=="
	NotEq = "!="
	LTE   = "<="
	GTE   = ">="
	And   = "&&"
	Or    = "||"
	Pipe  = "|>"
	Arrow = "->"

	Comma     = ","
	Semicolon = ";"
	Colon     = ":"
	Dot       = "."

	LParen   = "("
	RParen   = ")"
	LBrace   = "{"
	RBrace   = "}"
	LBracket = "["
	RBracket = "]"

	Function = "FUNCTION"
	Let      = "LET"
	Mut      = "MUT"
	True     = "TRUE"
	False    = "FALSE"
	If       = "IF"
	Else     = "ELSE"
	Return   = "RETURN"
	Nil      = "NIL"
	Import   = "IMPORT"
	As       = "AS"
	Match    = "MATCH"
)

var keywords = map[string]Type{
	"fn":     Function,
	"let":    Let,
	"mut":    Mut,
	"true":   True,
	"false":  False,
	"if":     If,
	"else":   Else,
	"return": Return,
	"nil":    Nil,
	"import": Import,
	"as":     As,
	"match":  Match,
}

func LookupIdent(ident string) Type {
	if tok, ok := keywords[ident]; ok {
		return tok
	}
	return Ident
}
