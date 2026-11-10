package runner

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"undalang/builtins"
	"undalang/diag"
	"undalang/evaluator"
	"undalang/lexer"
	"undalang/object"
	"undalang/parser"
)

func RunFile(path string, out io.Writer, errOut io.Writer) int {
	return RunFileIO(path, os.Stdin, out, errOut)
}

// RunFileIO runs a file with its own stdin
func RunFileIO(path string, in io.Reader, out io.Writer, errOut io.Writer) int {
	if !strings.HasSuffix(path, ".unda") {
		fmt.Fprintf(errOut, "error: expected a .unda file, got %q\n", path)
		return 1
	}

	input, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(errOut, "error: could not read %s: %v\n", path, err)
		return 1
	}

	return runSource(string(input), filepath.Dir(path), diag.DisplayPath(path), builtins.NewIO(in, out, errOut))
}

func RunSource(input string, out io.Writer, errOut io.Writer) int {
	return RunSourceInDir(input, "", out, errOut)
}

func RunSourceInDir(input string, baseDir string, out io.Writer, errOut io.Writer) int {
	return runSource(input, baseDir, "", builtins.NewIO(nil, out, errOut))
}

func runSource(input, baseDir, file string, stdio *builtins.IO) int {
	errOut := stdio.Err
	l := lexer.New(input)
	p := parser.New(l)
	program := p.ParseProgram()

	if len(p.Errors()) != 0 {
		printParserErrors(errOut, p.Errors())
		return 1
	}

	env := object.NewEnvironment()
	ctx := evaluator.NewContextIO(baseDir, stdio)
	ctx.File = file
	result := evaluator.EvalWithContext(program, env, ctx)
	if exit, ok := result.(*object.ExitSignal); ok {
		return exit.Code
	}
	if errObj, ok := result.(*object.Error); ok {
		fmt.Fprintln(errOut, errObj.Inspect())
		return 1
	}

	return 0
}

func printParserErrors(out io.Writer, errors []string) {
	fmt.Fprintln(out, "parser errors:")
	for _, msg := range errors {
		fmt.Fprintf(out, "  - %s\n", msg)
	}
}
