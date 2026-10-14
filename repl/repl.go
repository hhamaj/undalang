package repl

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"undalang/evaluator"
	"undalang/lexer"
	"undalang/object"
	"undalang/parser"
	"undalang/token"
)

const (
	Prompt         = "unda> "
	ContinuePrompt = "...   "
)

func Start(in io.Reader, out io.Writer, errOut io.Writer) {
	scanner := bufio.NewScanner(in)
	env := object.NewEnvironment()
	ctx := evaluator.NewContext("", out)

	for {
		source, ok := readInput(scanner, out)
		if !ok {
			return
		}
		if strings.TrimSpace(source) == "" {
			continue
		}

		l := lexer.New(source)
		p := parser.New(l)
		program := p.ParseProgram()

		if len(p.Errors()) != 0 {
			printParserErrors(errOut, p.Errors())
			continue
		}

		evaluated := evaluator.EvalWithContext(program, env, ctx)
		if evaluated == nil {
			continue
		}
		if evaluated.Type() == object.ErrorObj {
			fmt.Fprintln(errOut, evaluated.Inspect())
			continue
		}
		if evaluated.Type() != object.NilObj {
			fmt.Fprintln(out, evaluated.Inspect())
		}
	}
}

// readInput reads one complete entry and keeps asking for continuation lines
// while the input is unfinished. an empty continuation line submits whatever
// is there so you can bail out of mistakes
func readInput(scanner *bufio.Scanner, out io.Writer) (string, bool) {
	fmt.Fprint(out, Prompt)
	if !scanner.Scan() {
		return "", false
	}
	source := scanner.Text()
	for Incomplete(source) {
		fmt.Fprint(out, ContinuePrompt)
		if !scanner.Scan() {
			return source, true
		}
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			break
		}
		source += "\n" + line
	}
	return source, true
}

// Incomplete reports whether source needs more lines, like unclosed brackets,
// an unterminated string or a trailing operator
func Incomplete(source string) bool {
	l := lexer.New(source)
	depth := 0
	var last token.Token
	for {
		tok := l.NextToken()
		switch tok.Type {
		case token.EOF:
			if depth > 0 {
				return true
			}
			switch last.Type {
			case token.Pipe, token.Plus, token.Minus, token.Asterisk, token.Slash, token.Percent,
				token.And, token.Or, token.Eq, token.NotEq, token.LT, token.GT, token.LTE, token.GTE,
				token.Assign, token.Comma, token.Arrow, token.Dot:
				return true
			}
			return false
		case token.LParen, token.LBrace, token.LBracket:
			depth++
		case token.RParen, token.RBrace, token.RBracket:
			depth--
		case token.Illegal:
			if strings.HasPrefix(tok.Literal, `"`) {
				return true
			}
		}
		last = tok
	}
}

func printParserErrors(out io.Writer, errors []string) {
	fmt.Fprintln(out, "parser errors:")
	for _, msg := range errors {
		fmt.Fprintf(out, "  - %s\n", msg)
	}
}
