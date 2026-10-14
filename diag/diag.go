// Package diag formats compile time diagnostics with source context and has
// the path conventions the interpreter and compiler share
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

// Location renders "file:line:col: " (or "line L:C: " when there is no file)
// and returns "" when the position is unknown
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

// Format prints a diagnostic, then the offending source line with a caret
// under the column
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

// FromParserError turns a parser error string ("line L:C message") into a
// structured diagnostic
func FromParserError(file, msg string) Diagnostic {
	m := parserErrorPattern.FindStringSubmatch(msg)
	if m == nil {
		return Diagnostic{File: file, Message: msg}
	}
	line, _ := strconv.Atoi(m[1])
	col, _ := strconv.Atoi(m[2])
	return Diagnostic{File: file, Line: line, Column: col, Message: m[3]}
}

// DisplayPath makes an absolute path relative to the working directory when
// the file is under it
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

// DidYouMean picks the candidate closest to name and returns it as a hint
// like ` (did you mean "upper"?)`, or "" when nothing is close. close means
// at most one edit per three characters, so short names never get a guess
func DidYouMean(name string, candidates []string) string {
	best, bestDist := "", len(name)/3+1
	for _, c := range candidates {
		if c == name {
			continue
		}
		if d := editDistance(name, c); d < bestDist || (d == bestDist && best != "" && c < best) {
			best, bestDist = c, d
		}
	}
	if best == "" {
		return ""
	}
	return fmt.Sprintf(" (did you mean %q?)", best)
}

func editDistance(a, b string) int {
	ar, br := []rune(a), []rune(b)
	prev := make([]int, len(br)+1)
	cur := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(br)]
}
