package tests

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"undalang/backend"
	"undalang/runner"
)

// stdin, stderr and program arguments, through both backends the way
// `unda run` drives them
func TestStdioAndArgs(t *testing.T) {
	src := `import "process" as process
println("args:", process.args(), len(process.args()))
print("name? ")
let name = read_line()
eprintln("got", name)
println("hi", name)
println("empty:", read_line() == "")
println("rest:", read_stdin())
println("eof:", read_line())
`
	path := filepath.Join(t.TempDir(), "io.unda")
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	wantOut := "args: [a, b c] 2\nname? hi ada\nempty: true\nrest: x\ny\neof: nil\n"
	for _, name := range []string{"interp", "native"} {
		t.Run(name, func(t *testing.T) {
			if name == "native" {
				requireCC(t)
			}
			b, _ := backend.Get(name)
			var out, errOut bytes.Buffer
			code := b.Run(path, []string{"a", "b c"}, strings.NewReader("ada\r\n\nx\ny"), &out, &errOut)
			if code != 0 || out.String() != wantOut || errOut.String() != "got ada\n" {
				t.Fatalf("exit %d\nstdout:\n%q\nstderr:\n%q", code, out.String(), errOut.String())
			}
		})
	}
}

// the library functions have to be right, not just the same in both
// backends
func TestStdlibOutput(t *testing.T) {
	src := differentialPrograms["stdlib_v2"] + differentialPrograms["else_if"] + differentialPrograms["string_order"]
	want := `[-1, 1.5, 2, 3] [C, a, b, ç] []
[[ada, 5], [di, 5], [bo, 3], [cy, 3]] [[bo, 3], [cy, 3], [ada, 5], [di, 5]]
2 -1 true false
6 3.5 0 -9223372036854775808
true false true
2 nil
3 -1 [ç, a, y] 4 [] 1
ababab  007 ç  | long
{b: 2} {a: 1} [[a, 1], [b, 2]]
big mid small none
three
[true, false, true, false] [false, true, false, true] [false, true, false, true] [false, false, true, true] [true, false, true, false]
Aç😀 4 1
`
	var out, errOut bytes.Buffer
	if code := runner.RunSource(src, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if out.String() != want {
		t.Fatalf("got\n%s\nwant\n%s", out.String(), want)
	}
}
