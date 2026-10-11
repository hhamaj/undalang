package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"undalang/compiler"
	"undalang/diag"
	"undalang/repl"
	"undalang/runner"
	"undalang/semantic"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "repl":
		repl.Start(os.Stdin, os.Stdout, os.Stderr)
	case "run":
		os.Exit(runCmd(os.Args[2:]))
	case "check":
		os.Exit(checkCmd(os.Args[2:]))
	case "build":
		os.Exit(buildCmd(os.Args[2:]))
	default:
		fmt.Fprintf(os.Stderr, "error: unknown command %q\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  unda repl")
	fmt.Fprintln(os.Stderr, "  unda run [--native] path/to/file.unda")
	fmt.Fprintln(os.Stderr, "  unda check [--native] path/to/file.unda")
	fmt.Fprintln(os.Stderr, "  unda build path/to/file.unda [-o output] [--emit-c file.c]")
}

// parseArgs separates flags from positional arguments so flags may appear
// anywhere. valueFlags lists flags that take a value.
func parseArgs(args []string, valueFlags, boolFlags []string) (map[string]string, []string, error) {
	flags := map[string]string{}
	var positional []string
	isIn := func(list []string, s string) bool {
		for _, v := range list {
			if v == s {
				return true
			}
		}
		return false
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positional = append(positional, arg)
			continue
		}
		name, value, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		switch {
		case isIn(boolFlags, name):
			flags[name] = "true"
		case isIn(valueFlags, name):
			if !hasValue {
				if i+1 >= len(args) {
					return nil, nil, fmt.Errorf("flag %s needs a value", arg)
				}
				i++
				value = args[i]
			}
			flags[name] = value
		default:
			return nil, nil, fmt.Errorf("unknown flag %s", arg)
		}
	}
	return flags, positional, nil
}

func singleFile(cmd string, args []string, valueFlags, boolFlags []string) (map[string]string, string, bool) {
	flags, files, err := parseArgs(args, valueFlags, boolFlags)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		usage()
		return nil, "", false
	}
	if len(files) != 1 {
		fmt.Fprintf(os.Stderr, "error: unda %s expects one file\n", cmd)
		usage()
		return nil, "", false
	}
	if !strings.HasSuffix(files[0], ".unda") {
		fmt.Fprintf(os.Stderr, "error: expected a .unda file, got %q\n", files[0])
		return nil, "", false
	}
	return flags, files[0], true
}

func runCmd(args []string) int {
	flags, file, ok := singleFile("run", args, nil, []string{"native"})
	if !ok {
		return 1
	}
	if flags["native"] == "" {
		return runner.RunFile(file, os.Stdout, os.Stderr)
	}

	dir, err := os.MkdirTemp("", "unda-run-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer os.RemoveAll(dir)
	exe := filepath.Join(dir, compiler.DefaultOutput(file))
	if code := build(file, compiler.Options{Output: exe}); code != 0 {
		return code
	}
	cmd := exec.Command(exe)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func checkCmd(args []string) int {
	flags, file, ok := singleFile("check", args, nil, []string{"native"})
	if !ok {
		return 1
	}
	prog := semantic.AnalyzeFile(file, semantic.Options{Native: flags["native"] != ""})
	if prog.HasErrors() {
		printDiagnostics(prog)
		return 1
	}
	return 0
}

func buildCmd(args []string) int {
	flags, file, ok := singleFile("build", args, []string{"o", "emit-c"}, nil)
	if !ok {
		return 1
	}
	opts := compiler.Options{Output: flags["o"], EmitC: flags["emit-c"]}
	if opts.Output == "" && opts.EmitC == "" {
		opts.Output = compiler.DefaultOutput(file)
	}
	return build(file, opts)
}

func build(file string, opts compiler.Options) int {
	err := compiler.Build(file, opts)
	if err == nil {
		return 0
	}
	var diagErr *compiler.DiagnosticsError
	if errors.As(err, &diagErr) {
		printDiagnostics(diagErr.Program)
		return 1
	}
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	return 1
}

func printDiagnostics(prog *semantic.Program) {
	for _, d := range prog.Diagnostics {
		fmt.Fprint(os.Stderr, diag.Format(d, prog.Source(d.File)))
	}
}
