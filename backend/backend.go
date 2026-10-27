// Package backend puts the ways to run a program behind one interface. the
// front end (lexer, parser, semantic analysis) is shared, a backend only
// decides how the analyzed program executes
package backend

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"undalang/compiler"
	"undalang/diag"
	"undalang/runner"
	"undalang/semantic"
)

// Backend runs a program file and returns its exit code. diagnostics and
// runtime errors go to stderr
type Backend interface {
	Name() string
	// Describe says what the backend is good at, for --help
	Describe() string
	Run(path string, stdin io.Reader, stdout, stderr io.Writer) int
}

var backends = map[string]Backend{}

func register(b Backend) { backends[b.Name()] = b }

func init() {
	register(interp{})
	register(native{})
}

// Get returns a backend by name
func Get(name string) (Backend, bool) {
	b, ok := backends[name]
	return b, ok
}

// Names lists the backends, sorted
func Names() []string {
	out := make([]string, 0, len(backends))
	for name := range backends {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// PrintDiagnostics writes diagnostics with their source lines
func PrintDiagnostics(w io.Writer, prog *semantic.Program) {
	for _, d := range prog.Diagnostics {
		fmt.Fprint(w, diag.Format(d, prog.Source(d.File)))
	}
}

type interp struct{}

func (interp) Name() string     { return "interp" }
func (interp) Describe() string { return "tree-walking interpreter, runs without checking first" }
func (interp) Run(path string, _ io.Reader, stdout, stderr io.Writer) int {
	return runner.RunFile(path, stdout, stderr)
}

type native struct{}

func (native) Name() string     { return "native" }
func (native) Describe() string { return "compiles to C and runs the executable, needs a C compiler" }
func (native) Run(path string, stdin io.Reader, stdout, stderr io.Writer) int {
	dir, err := os.MkdirTemp("", "unda-run-")
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	defer os.RemoveAll(dir)
	exe := filepath.Join(dir, compiler.DefaultOutput(path))
	if err := compiler.Build(path, compiler.Options{Output: exe}); err != nil {
		var diagErr *compiler.DiagnosticsError
		if errors.As(err, &diagErr) {
			PrintDiagnostics(stderr, diagErr.Program)
		} else {
			fmt.Fprintf(stderr, "error: %v\n", err)
		}
		return 1
	}
	cmd := exec.Command(exe)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}
