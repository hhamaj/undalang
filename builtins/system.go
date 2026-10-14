package builtins

import (
	"fmt"
	"math/bits"
	"os"
	"sort"
	"syscall"

	"undalang/object"
)

// stops reports results a builtin has to hand straight back when a callback
// gives them: errors, and os.exit on its way out
func stops(obj object.Object) bool {
	switch obj.(type) {
	case *object.Error, *object.ExitSignal:
		return true
	}
	return false
}

func intArgs(name string, args []object.Object, n int) ([]int64, object.Object) {
	if err := argCount(name, args, n); err != nil {
		return nil, err
	}
	out := make([]int64, n)
	for i, a := range args {
		v, ok := a.(*object.Integer)
		if !ok {
			return nil, errorf("%s argument %d must be INTEGER, got %s", name, i+1, a.Type())
		}
		out[i] = v.Value
	}
	return out, nil
}

// bitsModule works on the 64 bits of an int
func bitsModule() map[string]object.Object {
	two := func(name string, op func(a, b int64) int64) *object.Builtin {
		return builtin(func(args ...object.Object) object.Object {
			v, err := intArgs(name, args, 2)
			if err != nil {
				return err
			}
			return object.NewInt(op(v[0], v[1]))
		})
	}
	shift := func(name string, op func(a int64, n uint) int64) *object.Builtin {
		return builtin(func(args ...object.Object) object.Object {
			v, err := intArgs(name, args, 2)
			if err != nil {
				return err
			}
			if v[1] < 0 || v[1] > 63 {
				return errorf("%s shift must be between 0 and 63, got %d", name, v[1])
			}
			return object.NewInt(op(v[0], uint(v[1])))
		})
	}
	return map[string]object.Object{
		"and": two("bits.and", func(a, b int64) int64 { return a & b }),
		"or":  two("bits.or", func(a, b int64) int64 { return a | b }),
		"xor": two("bits.xor", func(a, b int64) int64 { return a ^ b }),
		"not": builtin(func(args ...object.Object) object.Object {
			v, err := intArgs("bits.not", args, 1)
			if err != nil {
				return err
			}
			return object.NewInt(^v[0])
		}),
		"count": builtin(func(args ...object.Object) object.Object {
			v, err := intArgs("bits.count", args, 1)
			if err != nil {
				return err
			}
			return object.NewInt(int64(bits.OnesCount64(uint64(v[0]))))
		}),
		"shl":  shift("bits.shl", func(a int64, n uint) int64 { return a << n }),
		"shr":  shift("bits.shr", func(a int64, n uint) int64 { return a >> n }),
		"ushr": shift("bits.ushr", func(a int64, n uint) int64 { return int64(uint64(a) >> n) }),
	}
}

func osModule() map[string]object.Object {
	return map[string]object.Object{
		"exit": builtin(func(args ...object.Object) object.Object {
			v, err := intArgs("os.exit", args, 1)
			if err != nil {
				return err
			}
			if v[0] < 0 || v[0] > 255 {
				return errorf("os.exit code must be between 0 and 255, got %d", v[0])
			}
			return &object.ExitSignal{Code: int(v[0])}
		}),
		"exists": builtin(func(args ...object.Object) object.Object {
			path, err := pathArg("os.exists", args)
			if err != nil {
				return err
			}
			_, statErr := os.Stat(path)
			return &object.Boolean{Value: statErr == nil}
		}),
		"is_dir": builtin(func(args ...object.Object) object.Object {
			path, err := pathArg("os.is_dir", args)
			if err != nil {
				return err
			}
			info, statErr := os.Stat(path)
			return &object.Boolean{Value: statErr == nil && info.IsDir()}
		}),
		"cwd": builtin(func(args ...object.Object) object.Object {
			if err := argCount("os.cwd", args, 0); err != nil {
				return err
			}
			// syscall.Getwd, not os.Getwd: the latter trusts $PWD, getcwd(3)
			// in the native runtime doesn't
			dir, err := syscall.Getwd()
			if err != nil {
				return errorf("os.cwd failed: %v", err)
			}
			return &object.String{Value: dir}
		}),
		"getpid": builtin(func(args ...object.Object) object.Object {
			if err := argCount("os.getpid", args, 0); err != nil {
				return err
			}
			return object.NewInt(int64(os.Getpid()))
		}),
		"list_dir":     plain("os.list_dir", listDir),
		"try_list_dir": attempt("os.try_list_dir", listDir),
		"mkdir":        plain("os.mkdir", mkdir),
		"try_mkdir":    attempt("os.try_mkdir", mkdir),
		"remove":       plain("os.remove", remove),
		"try_remove":   attempt("os.try_remove", remove),
	}
}

func pathArg(name string, args []object.Object) (string, object.Object) {
	if err := argCount(name, args, 1); err != nil {
		return "", err
	}
	path, ok := expectString(args[0], name+" path")
	if !ok {
		return "", pathErr(name+" path", args[0])
	}
	return path, nil
}

// listDir gives the names in a directory, sorted
func listDir(name string, args []object.Object) (object.Object, string) {
	path, err := pathArg(name, args)
	if err != nil {
		return err, ""
	}
	info, statErr := os.Stat(path)
	if statErr != nil {
		return nil, fmt.Sprintf("%s failed: %v", name, statErr)
	}
	if !info.IsDir() {
		return nil, fmt.Sprintf("%s failed: %s is not a directory", name, path)
	}
	entries, readErr := os.ReadDir(path)
	if readErr != nil {
		return nil, fmt.Sprintf("%s failed: %v", name, readErr)
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	sort.Strings(names)
	return stringsToArray(names), ""
}

func mkdir(name string, args []object.Object) (object.Object, string) {
	path, err := pathArg(name, args)
	if err != nil {
		return err, ""
	}
	if mkErr := os.Mkdir(path, 0755); mkErr != nil {
		return nil, fmt.Sprintf("%s failed: %v", name, mkErr)
	}
	return &object.Nil{}, ""
}

// remove deletes a file or an empty directory
func remove(name string, args []object.Object) (object.Object, string) {
	path, err := pathArg(name, args)
	if err != nil {
		return err, ""
	}
	if rmErr := os.Remove(path); rmErr != nil {
		return nil, fmt.Sprintf("%s failed: %v", name, rmErr)
	}
	return &object.Nil{}, ""
}
