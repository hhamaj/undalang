package repl

import (
	"bufio"
	"fmt"
	"io"

	"ranalang/evaluator"
	"ranalang/lexer"
	"ranalang/object"
	"ranalang/parser"
)

const Prompt = "rana> "

func Start(in io.Reader, out io.Writer, errOut io.Writer) {
	scanner := bufio.NewScanner(in)
	env := object.NewEnvironment()
	ctx := evaluator.NewContext("", out)

	for {
		fmt.Fprint(out, Prompt)
		if !scanner.Scan() {
			return
		}

		line := scanner.Text()
		l := lexer.New(line)
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

func printParserErrors(out io.Writer, errors []string) {
	fmt.Fprintln(out, "parser errors:")
	for _, msg := range errors {
		fmt.Fprintf(out, "  - %s\n", msg)
	}
}
