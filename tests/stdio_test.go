package tests

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"undalang/backend"
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
