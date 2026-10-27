package tests

import (
	"bytes"
	"strings"
	"testing"

	"undalang/repl"
)

func TestREPLMultiLineInput(t *testing.T) {
	input := strings.Join([]string{
		"fn add(a, b) {",
		"    return a + b",
		"}",
		"add(2, 3)",
		`"a,b" |>`,
		"    len",
		"let broken = (1 +",
		"",
		"[1,",
		" 2]",
	}, "\n") + "\n"
	var out, errOut bytes.Buffer
	repl.Start(strings.NewReader(input), &out, &errOut)

	got := strings.ReplaceAll(out.String(), repl.ContinuePrompt, "|")
	got = strings.ReplaceAll(got, repl.Prompt, ">")
	// definitions echo their value like single-line input always did
	want := ">||fn add(a, b) {\nreturn (a + b)\n}\n>5\n>|3\n>|>|[1, 2]\n>"
	if got != want {
		t.Fatalf("stdout\ngot  %q\nwant %q", got, want)
	}
	if !strings.Contains(errOut.String(), "unexpected end of input") {
		t.Fatalf("expected parser error for abandoned input, got %q", errOut.String())
	}
}

func TestREPLIncomplete(t *testing.T) {
	tests := map[string]bool{
		"let x = 1":         false,
		"fn f() {":          true,
		`"open`:             true,
		`"a {" + "b"`:       false,
		"x |>":              true,
		"// only { comment": false,
		"[1, 2]":            false,
		"match x {\n1 -> 2": true,
	}
	for src, want := range tests {
		if got := repl.Incomplete(src); got != want {
			t.Errorf("Incomplete(%q) = %v, want %v", src, got, want)
		}
	}
}

// an update that fails its type check mustn't leave half a change behind,
// even when it would have changed the value in place
func TestREPLFailedUpdateChangesNothing(t *testing.T) {
	input := strings.Join([]string{
		"let mut xs: array[int] = [1, 2]",
		"xs[0] = 5",
		`let mut ps: map[string, array[int]] = {"a": [1]}`,
		`ps["a"][0] = 7`,
		"fn put(v) { xs[1] = v }",
		`fn deep(v) { ps["a"][0] = v }`,
		`put("s")`,
		`deep("s")`,
		"xs",
		"ps",
	}, "\n") + "\n"
	var out, errOut bytes.Buffer
	repl.Start(strings.NewReader(input), &out, &errOut)
	got := out.String()
	want := "[5, 2]\n" + repl.Prompt + "{a: [7]}\n" + repl.Prompt
	if !strings.HasSuffix(got, want) {
		t.Fatalf("values changed by failed updates:\n%s", got)
	}
	if strings.Count(errOut.String(), "type mismatch") != 2 {
		t.Fatalf("expected two type errors, got %q", errOut.String())
	}
}
