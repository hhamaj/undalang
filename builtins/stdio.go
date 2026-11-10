package builtins

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"undalang/object"
)

// IO is where a program's standard streams go. stdin sits behind one
// buffered reader so read_line and read_stdin share what's been read ahead
type IO struct {
	Out, Err io.Writer
	in       *bufio.Reader
}

func NewIO(in io.Reader, out, errOut io.Writer) *IO {
	s := &IO{Out: out, Err: errOut}
	if in != nil {
		s.in = bufio.NewReader(in)
	}
	return s
}

func (s *IO) add(b map[string]*object.Builtin) {
	b["eprint"] = builtin(func(args ...object.Object) object.Object {
		fmt.Fprint(s.Err, joinArgs(args))
		return &object.Nil{}
	})
	b["eprintln"] = builtin(func(args ...object.Object) object.Object {
		fmt.Fprintln(s.Err, joinArgs(args))
		return &object.Nil{}
	})
	b["read_line"] = builtin(func(args ...object.Object) object.Object {
		if len(args) != 0 {
			return errorf("read_line expects 0 arguments, got %d", len(args))
		}
		if s.in == nil {
			return &object.Nil{}
		}
		line, err := s.in.ReadString('\n')
		if err != nil && line == "" {
			// end of input, nil tells it apart from an empty line
			return &object.Nil{}
		}
		line = strings.TrimSuffix(line, "\n")
		return &object.String{Value: strings.TrimSuffix(line, "\r")}
	})
	b["read_stdin"] = builtin(func(args ...object.Object) object.Object {
		if len(args) != 0 {
			return errorf("read_stdin expects 0 arguments, got %d", len(args))
		}
		if s.in == nil {
			return &object.String{}
		}
		data, err := io.ReadAll(s.in)
		if err != nil {
			return errorf("read_stdin: %v", err)
		}
		return &object.String{Value: string(data)}
	})
}
