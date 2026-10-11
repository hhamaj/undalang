// Package compiler is Unda's native backend: it lowers analyzed programs to C
// and drives the system C compiler to produce an executable.
package compiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"undalang/semantic"
)

type Options struct {
	// Output is the executable path; empty skips invoking the C compiler.
	Output string
	// EmitC, when set, also writes the generated C source to this path.
	EmitC string
	// CC overrides the C compiler; defaults to $CC, then "cc".
	CC string
}

// DiagnosticsError reports that the program failed semantic analysis.
type DiagnosticsError struct {
	Program *semantic.Program
}

func (e *DiagnosticsError) Error() string {
	return fmt.Sprintf("%d error(s)", len(e.Program.Diagnostics))
}

// Build analyzes path, generates C, and compiles it according to opts.
func Build(path string, opts Options) error {
	prog := semantic.AnalyzeFile(path, semantic.Options{Native: true})
	if prog.HasErrors() {
		return &DiagnosticsError{Program: prog}
	}
	src, err := GenerateC(prog)
	if err != nil {
		return err
	}
	if opts.EmitC != "" {
		if err := os.WriteFile(opts.EmitC, []byte(src), 0644); err != nil {
			return err
		}
	}
	if opts.Output == "" {
		return nil
	}
	return CompileC(src, opts.Output, opts.CC)
}

// CompileC writes src to a temporary file and compiles it to output.
func CompileC(src, output, cc string) error {
	if cc == "" {
		cc = os.Getenv("CC")
	}
	if cc == "" {
		cc = "cc"
	}
	dir, err := os.MkdirTemp("", "unda-build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	cfile := filepath.Join(dir, "main.c")
	if err := os.WriteFile(cfile, []byte(src), 0644); err != nil {
		return err
	}
	cmd := exec.Command(cc, "-std=gnu11", "-O2", "-o", output, cfile, "-lm", "-pthread")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("C compiler (%s) failed: %v\n%s", cc, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// DefaultOutput derives an executable name from a source path.
func DefaultOutput(path string) string {
	return strings.TrimSuffix(filepath.Base(path), ".unda")
}

func sortStrings(s []string) { sort.Strings(s) }
