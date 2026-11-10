package lexer

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"undalang/token"
)

type Lexer struct {
	input        string
	position     int
	readPosition int
	ch           byte
	line         int
	column       int
}

func New(input string) *Lexer {
	return NewAt(input, 1, 1)
}

// NewAt lexes input that starts at line:col of a bigger source, for the
// expressions inside a string template
func NewAt(input string, line, col int) *Lexer {
	l := &Lexer{input: input, line: line, column: col - 1}
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
		lit, template, ok := l.readString()
		tok.Type, tok.Literal = token.String, lit
		if template {
			tok.Type = token.Template
		}
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
// closing quote. template is set when it holds ${...} parts, then value is
// the raw text and the parser splits and unescapes it
func (l *Lexer) readString() (value string, template, ok bool) {
	start := l.position
	end := SkipString(l.input, start)
	if end < 0 {
		end = len(l.input)
	}
	for l.position < end && l.ch != 0 {
		l.readChar()
	}
	raw := l.input[start+1 : end]
	if l.ch != '"' {
		return raw, false, false
	}
	l.readChar()
	if HasInterpolation(raw) {
		return raw, true, true
	}
	return unescape(raw), false, true
}

// SkipString takes s[i] == '"' and returns the index of the closing quote,
// stepping over escapes and whole ${...} parts, which can hold strings of
// their own. -1 if the string never ends
func SkipString(s string, i int) int {
	for j := i + 1; j < len(s); j++ {
		switch {
		case s[j] == '\\':
			j++
		case s[j] == '$' && j+1 < len(s) && s[j+1] == '{':
			end := MatchBrace(s, j+1)
			if end < 0 {
				return -1
			}
			j = end
		case s[j] == '"':
			return j
		}
	}
	return -1
}

// MatchBrace takes s[i] == '{' and returns the index of its closing brace
func MatchBrace(s string, i int) int {
	depth := 0
	for j := i; j < len(s); j++ {
		switch s[j] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return j
			}
		case '"':
			j = SkipString(s, j)
			if j < 0 {
				return -1
			}
		}
	}
	return -1
}

// HasInterpolation reports whether the raw text of a string has a ${ that
// isn't escaped
func HasInterpolation(raw string) bool {
	for j := 0; j < len(raw); j++ {
		if raw[j] == '\\' {
			j++
		} else if raw[j] == '$' && j+1 < len(raw) && raw[j+1] == '{' {
			return true
		}
	}
	return false
}

// Unescape turns the escapes of a string literal into the characters
func Unescape(s string) string { return unescape(s) }

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
		case 'r':
			out = append(out, '\r')
		case '0':
			out = append(out, 0)
		case 'x':
			// \x41, ascii only so strings stay valid utf-8
			if i+2 < len(s) {
				if v, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil && v < 0x80 {
					out = append(out, byte(v))
					i += 2
					continue
				}
			}
			out = append(out, 'x')
		case 'u':
			// \u{1F600}
			if i+1 < len(s) && s[i+1] == '{' {
				if end := strings.IndexByte(s[i:], '}'); end > 2 {
					if v, err := strconv.ParseUint(s[i+2:i+end], 16, 32); err == nil && utf8.ValidRune(rune(v)) {
						out = utf8.AppendRune(out, rune(v))
						i += end
						continue
					}
				}
			}
			out = append(out, 'u')
		case '"':
			out = append(out, '"')
		case '$':
			out = append(out, '$')
		case '\\':
			out = append(out, '\\')
		default:
			out = append(out, s[i])
		}
	}
	return string(out)
}
