package lexer

import "undalang/token"

type Lexer struct {
	input        string
	position     int
	readPosition int
	ch           byte
	line         int
	column       int
}

func New(input string) *Lexer {
	l := &Lexer{input: input, line: 1}
	l.readChar()
	return l
}

func (l *Lexer) NextToken() token.Token {
	l.skipWhitespace()

	tok := token.Token{Line: l.line, Column: l.column}

	switch l.ch {
	case '=':
		if l.peekChar() == '=' {
			tok = l.twoCharToken(token.Eq)
		} else {
			tok = l.newToken(token.Assign, l.ch)
			l.readChar()
		}
	case '+':
		tok = l.newToken(token.Plus, l.ch)
		l.readChar()
	case '-':
		if l.peekChar() == '>' {
			tok = l.twoCharToken(token.Arrow)
		} else {
			tok = l.newToken(token.Minus, l.ch)
			l.readChar()
		}
	case '!':
		if l.peekChar() == '=' {
			tok = l.twoCharToken(token.NotEq)
		} else {
			tok = l.newToken(token.Bang, l.ch)
			l.readChar()
		}
	case '*':
		tok = l.newToken(token.Asterisk, l.ch)
		l.readChar()
	case '/':
		tok = l.newToken(token.Slash, l.ch)
		l.readChar()
	case '%':
		tok = l.newToken(token.Percent, l.ch)
		l.readChar()
	case '<':
		if l.peekChar() == '=' {
			tok = l.twoCharToken(token.LTE)
		} else {
			tok = l.newToken(token.LT, l.ch)
			l.readChar()
		}
	case '>':
		if l.peekChar() == '=' {
			tok = l.twoCharToken(token.GTE)
		} else {
			tok = l.newToken(token.GT, l.ch)
			l.readChar()
		}
	case '&':
		if l.peekChar() == '&' {
			tok = l.twoCharToken(token.And)
		} else {
			tok = l.newToken(token.Illegal, l.ch)
			l.readChar()
		}
	case '|':
		if l.peekChar() == '|' {
			tok = l.twoCharToken(token.Or)
		} else if l.peekChar() == '>' {
			tok = l.twoCharToken(token.Pipe)
		} else {
			tok = l.newToken(token.Illegal, l.ch)
			l.readChar()
		}
	case ',':
		tok = l.newToken(token.Comma, l.ch)
		l.readChar()
	case '?':
		tok = l.newToken(token.Question, l.ch)
		l.readChar()
	case ';':
		tok = l.newToken(token.Semicolon, l.ch)
		l.readChar()
	case ':':
		tok = l.newToken(token.Colon, l.ch)
		l.readChar()
	case '.':
		if l.peekChar() == '.' {
			tok = l.twoCharToken(token.DotDot)
		} else {
			tok = l.newToken(token.Dot, l.ch)
			l.readChar()
		}
	case '(':
		tok = l.newToken(token.LParen, l.ch)
		l.readChar()
	case ')':
		tok = l.newToken(token.RParen, l.ch)
		l.readChar()
	case '{':
		tok = l.newToken(token.LBrace, l.ch)
		l.readChar()
	case '}':
		tok = l.newToken(token.RBrace, l.ch)
		l.readChar()
	case '[':
		tok = l.newToken(token.LBracket, l.ch)
		l.readChar()
	case ']':
		tok = l.newToken(token.RBracket, l.ch)
		l.readChar()
	case '"':
		lit, ok := l.readString()
		tok.Type, tok.Literal = token.String, lit
		if !ok {
			tok.Type, tok.Literal = token.Illegal, `"`+lit
		}
	case 0:
		tok.Literal = ""
		tok.Type = token.EOF
	default:
		if isLetter(l.ch) {
			tok.Literal = l.readIdentifier()
			tok.Type = token.LookupIdent(tok.Literal)
			return tok
		}
		if isDigit(l.ch) {
			lit, typ := l.readNumber()
			tok.Literal = lit
			tok.Type = typ
			return tok
		}
		tok = l.newToken(token.Illegal, l.ch)
		l.readChar()
	}

	return tok
}

func (l *Lexer) readChar() {
	if l.readPosition >= len(l.input) {
		l.ch = 0
	} else {
		l.ch = l.input[l.readPosition]
	}
	l.position = l.readPosition
	l.readPosition++
	if l.ch == '\n' {
		l.line++
		l.column = 0
	} else {
		l.column++
	}
}

func (l *Lexer) peekChar() byte {
	if l.readPosition >= len(l.input) {
		return 0
	}
	return l.input[l.readPosition]
}

// skipWhitespace skips `//` line comments too
func (l *Lexer) skipWhitespace() {
	for {
		switch {
		case l.ch == ' ' || l.ch == '\t' || l.ch == '\n' || l.ch == '\r':
			l.readChar()
		case l.ch == '/' && l.peekChar() == '/':
			for l.ch != '\n' && l.ch != 0 {
				l.readChar()
			}
		default:
			return
		}
	}
}

func (l *Lexer) readIdentifier() string {
	pos := l.position
	for isLetter(l.ch) || isDigit(l.ch) {
		l.readChar()
	}
	return l.input[pos:l.position]
}

func (l *Lexer) readNumber() (string, token.Type) {
	pos := l.position
	typ := token.Type(token.Int)

	for isDigit(l.ch) {
		l.readChar()
	}
	if l.ch == '.' && isDigit(l.peekChar()) {
		typ = token.Float
		l.readChar()
		for isDigit(l.ch) {
			l.readChar()
		}
	}
	// exponent part like 1e5, 2.5e-3 or 3E+2
	if l.ch == 'e' || l.ch == 'E' {
		next := l.peekChar()
		signed := (next == '+' || next == '-') && l.readPosition+1 < len(l.input) && isDigit(l.input[l.readPosition+1])
		if isDigit(next) || signed {
			typ = token.Float
			l.readChar()
			if signed {
				l.readChar()
			}
			for isDigit(l.ch) {
				l.readChar()
			}
		}
	}

	return l.input[pos:l.position], typ
}

// readString reads a string literal. ok is false if the input ends before the
// closing quote
func (l *Lexer) readString() (value string, ok bool) {
	l.readChar()
	pos := l.position

	for l.ch != '"' && l.ch != 0 {
		if l.ch == '\\' && l.peekChar() != 0 {
			l.readChar()
		}
		l.readChar()
	}

	raw := l.input[pos:l.position]
	if l.ch != '"' {
		return raw, false
	}
	l.readChar()
	return unescape(raw), true
}

func (l *Lexer) twoCharToken(typ token.Type) token.Token {
	ch := l.ch
	line, col := l.line, l.column
	l.readChar()
	lit := string(ch) + string(l.ch)
	l.readChar()
	return token.Token{Type: typ, Literal: lit, Line: line, Column: col}
}

func (l *Lexer) newToken(tokenType token.Type, ch byte) token.Token {
	return token.Token{Type: tokenType, Literal: string(ch), Line: l.line, Column: l.column}
}

func isLetter(ch byte) bool {
	return 'a' <= ch && ch <= 'z' || 'A' <= ch && ch <= 'Z' || ch == '_'
}

func isDigit(ch byte) bool {
	return '0' <= ch && ch <= '9'
}

func unescape(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			out = append(out, s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			out = append(out, '\n')
		case 't':
			out = append(out, '\t')
		case '"':
			out = append(out, '"')
		case '\\':
			out = append(out, '\\')
		default:
			out = append(out, s[i])
		}
	}
	return string(out)
}
