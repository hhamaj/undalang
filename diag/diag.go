// Package diag formats compile-time diagnostics with source context and
// provides the path conventions shared by the interpreter and compiler.
package diag

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type Diagnostic struct {
	File    string
	Line    int
	Column  int
	Message string
}

func (d Diagnostic) String() string {
	return Location(d.File, d.Line, d.Column) + "error: " + d.Message
}

// Location renders "file:line:col: " (or "line L:C: " without a file) and
// returns "" when the position is unknown.
func Location(file string, line, column int) string {
	if line <= 0 {
		if file != "" {
			return file + ": "
		}
		return ""
	}
	if file == "" {
		return fmt.Sprintf("line %d:%d: ", line, column)
	}
	return fmt.Sprintf("%s:%d:%d: ", file, line, column)
}

// Format renders a diagnostic followed by the offending source line and a
// caret under the reported column.
func Format(d Diagnostic, source string) string {
	var out strings.Builder
	out.WriteString(d.String())
	out.WriteString("\n")
	lines := strings.Split(source, "\n")
	if d.Line <= 0 || d.Line > len(lines) {
		return out.String()
	}
	text := strings.TrimRight(lines[d.Line-1], "\r")
	gutter := strconv.Itoa(d.Line)
	fmt.Fprintf(&out, "  %s | %s\n", gutter, text)
	pad := strings.Repeat(" ", len(gutter))
	caret := ""
	for i := 0; i < d.Column-1 && i < len(text); i++ {
		if text[i] == '\t' {
			caret += "\t"
		} else {
			caret += " "
		}
	}
	fmt.Fprintf(&out, "  %s | %s^\n", pad, caret)
	return out.String()
}

var parserErrorPattern = regexp.MustCompile(`^line (\d+):(\d+) (.*)$`)

// FromParserError converts a parser error string ("line L:C message") into a
// structured diagnostic.
func FromParserError(file, msg string) Diagnostic {
	m := parserErrorPattern.FindStringSubmatch(msg)
	if m == nil {
		return Diagnostic{File: file, Message: msg}
	}
	line, _ := strconv.Atoi(m[1])
	col, _ := strconv.Atoi(m[2])
	return Diagnostic{File: file, Line: line, Column: col, Message: m[3]}
}

// DisplayPath shortens an absolute path to one relative to the working
// directory when the file lives below it.
func DisplayPath(path string) string {
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	wd, err := os.Getwd()
	if err != nil {
		return abs
	}
	rel, err := filepath.Rel(wd, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return abs
	}
	return rel
}
