package builtins

import (
	"fmt"
	"io"
	"strings"

	"ranalang/object"
)

func New(out io.Writer) map[string]*object.Builtin {
	return map[string]*object.Builtin{
		"print": {
			Fn: func(args ...object.Object) object.Object {
				fmt.Fprint(out, joinArgs(args))
				return &object.Nil{}
			},
		},
		"println": {
			Fn: func(args ...object.Object) object.Object {
				fmt.Fprintln(out, joinArgs(args))
				return &object.Nil{}
			},
		},
		"len": {
			Fn: func(args ...object.Object) object.Object {
				if len(args) != 1 {
					return &object.Error{Message: fmt.Sprintf("len expects 1 argument, got %d", len(args))}
				}
				switch arg := args[0].(type) {
				case *object.String:
					return &object.Integer{Value: int64(len(arg.Value))}
				default:
					return &object.Error{Message: fmt.Sprintf("len does not support %s", arg.Type())}
				}
			},
		},
		"type": {
			Fn: func(args ...object.Object) object.Object {
				if len(args) != 1 {
					return &object.Error{Message: fmt.Sprintf("type expects 1 argument, got %d", len(args))}
				}
				return &object.String{Value: strings.ToLower(string(args[0].Type()))}
			},
		},
	}
}

func joinArgs(args []object.Object) string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		parts = append(parts, arg.Inspect())
	}
	return strings.Join(parts, " ")
}
