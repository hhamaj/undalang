package builtins

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"undalang/object"
)

func New(out io.Writer) map[string]*object.Builtin {
	b := map[string]*object.Builtin{
		"print":   builtin(func(args ...object.Object) object.Object { fmt.Fprint(out, joinArgs(args)); return &object.Nil{} }),
		"println": builtin(func(args ...object.Object) object.Object { fmt.Fprintln(out, joinArgs(args)); return &object.Nil{} }),
		"len":     builtin(lenBuiltin),
		"type": builtin(func(args ...object.Object) object.Object {
			if len(args) != 1 {
				return errorf("type expects 1 argument, got %d", len(args))
			}
			return &object.String{Value: strings.ToLower(string(args[0].Type()))}
		}),
		"ok":              builtin(resultBuiltin("ok", true)),
		"err":             builtin(resultBuiltin("err", false)),
		"read_file":       plain("read_file", readFile),
		"try_read_file":   attempt("try_read_file", readFile),
		"write_file":      plain("write_file", writeFile),
		"try_write_file":  attempt("try_write_file", writeFile),
		"append_file":     plain("append_file", appendFile),
		"try_append_file": attempt("try_append_file", appendFile),
		"int":             plain("int", toInt),
		"try_int":         attempt("try_int", toInt),
		"float":           plain("float", toFloat),
		"try_float":       attempt("try_float", toFloat),
		"string":          builtin(toString),
	}
	return b
}

func resultBuiltin(name string, ok bool) object.BuiltinFunction {
	return func(args ...object.Object) object.Object {
		if len(args) != 1 {
			return errorf("%s expects 1 argument, got %d", name, len(args))
		}
		return &object.Result{Ok: ok, Value: args[0]}
	}
}

// Applier calls an Unda function value. higher-order builtins go through it
// so callbacks run in the caller's evaluation context
type Applier func(fn object.Object, args []object.Object) object.Object

func StdModules(apply Applier) map[string]*object.Module {
	return map[string]*object.Module{
		"strings": {Name: "strings", Members: map[string]object.Object{
			"contains":   builtin(stringsContains),
			"has_prefix": builtin(stringsHasPrefix),
			"has_suffix": builtin(stringsHasSuffix),
			"join":       builtin(stringsJoin),
			"lower":      builtin(oneString("strings.lower", strings.ToLower)),
			"replace":    builtin(stringsReplace),
			"split":      builtin(stringsSplit),
			"trim":       builtin(oneString("strings.trim", strings.TrimSpace)),
			"upper":      builtin(oneString("strings.upper", strings.ToUpper)),
		}},
		"arrays": {Name: "arrays", Members: map[string]object.Object{
			"append":  builtin(arraysAppend),
			"filter":  builtin(arraysFilter(apply)),
			"first":   builtin(arraysFirst),
			"join":    builtin(arraysJoin),
			"last":    builtin(arraysLast),
			"map":     builtin(arraysMap(apply)),
			"range":   builtin(arraysRange),
			"reduce":  builtin(arraysReduce(apply)),
			"reverse": builtin(arraysReverse),
			"slice":   builtin(arraysSlice),
		}},
		"maps": {Name: "maps", Members: map[string]object.Object{
			"get":    builtin(mapsGet),
			"has":    builtin(mapsHas),
			"keys":   builtin(mapsKeys),
			"merge":  builtin(mapsMerge),
			"set":    builtin(mapsSet),
			"values": builtin(mapsValues),
		}},
		"json": {Name: "json", Members: map[string]object.Object{
			"parse":     plain("json.parse", jsonParse),
			"try_parse": attempt("json.try_parse", jsonParse),
			"stringify": builtin(jsonStringify),
		}},
		"math": {Name: "math", Members: map[string]object.Object{
			"abs":   builtin(mathOne("math.abs", math.Abs)),
			"ceil":  builtin(mathOne("math.ceil", math.Ceil)),
			"floor": builtin(mathOne("math.floor", math.Floor)),
			"max":   builtin(mathTwo("math.max", math.Max)),
			"min":   builtin(mathTwo("math.min", math.Min)),
			"pow":   builtin(mathTwo("math.pow", math.Pow)),
			"round": builtin(mathOne("math.round", math.Round)),
			"sqrt":  builtin(mathOne("math.sqrt", math.Sqrt)),
		}},
		"time": {Name: "time", Members: map[string]object.Object{
			"format": builtin(timeFormat),
			"now": builtin(func(args ...object.Object) object.Object {
				if len(args) != 0 {
					return errorf("time.now expects 0 arguments, got %d", len(args))
				}
				return &object.String{Value: time.Now().Format(time.RFC3339)}
			}),
			"unix": builtin(func(args ...object.Object) object.Object {
				if len(args) != 0 {
					return errorf("time.unix expects 0 arguments, got %d", len(args))
				}
				return object.NewInt(time.Now().Unix())
			}),
		}},
		"process": {Name: "process", Members: map[string]object.Object{
			"args": builtin(func(args ...object.Object) object.Object {
				if len(args) != 0 {
					return errorf("process.args expects 0 arguments, got %d", len(args))
				}
				return stringsToArray(os.Args)
			}),
			"env":     builtin(processEnv),
			"run":     plain("process.run", processRun),
			"try_run": attempt("process.try_run", processRun),
		}},
		"shell": {Name: "shell", Members: map[string]object.Object{
			"run":     plain("shell.run", shellRun),
			"try_run": attempt("shell.try_run", shellRun),
		}},
		"mem":  {Name: "mem", Members: memModule()},
		"bits": {Name: "bits", Members: bitsModule()},
		"os":   {Name: "os", Members: osModule()},
	}
}

func builtin(fn object.BuiltinFunction) *object.Builtin { return &object.Builtin{Fn: fn} }

// fallible is a builtin that can fail for reasons outside the program, like a
// missing file or a command exiting non-zero. it gives back its value (an
// argument error counts as a value, that's the caller's bug) or a failure
// message. every one of them becomes two builtins, see plain and attempt
type fallible func(name string, args []object.Object) (object.Object, string)

// plain makes failing a runtime error
func plain(name string, fn fallible) *object.Builtin {
	return builtin(func(args ...object.Object) object.Object {
		v, fail := fn(name, args)
		if fail != "" {
			return errorf("%s", fail)
		}
		return v
	})
}

// attempt is the try_ flavor, it returns ok(value) or err(message)
func attempt(name string, fn fallible) *object.Builtin {
	return builtin(func(args ...object.Object) object.Object {
		v, fail := fn(name, args)
		if fail != "" {
			return &object.Result{Value: &object.String{Value: fail}}
		}
		if _, isErr := v.(*object.Error); isErr {
			return v
		}
		return &object.Result{Ok: true, Value: v}
	})
}

func lenBuiltin(args ...object.Object) object.Object {
	if len(args) != 1 {
		return errorf("len expects 1 argument, got %d", len(args))
	}
	switch arg := args[0].(type) {
	case *object.String:
		return object.NewInt(int64(utf8.RuneCountInString(arg.Value)))
	case *object.Array:
		return object.NewInt(int64(len(arg.Elements)))
	case *object.Map:
		return object.NewInt(int64(len(arg.Pairs)))
	default:
		return errorf("len does not support %s", arg.Type())
	}
}

func readFile(name string, args []object.Object) (object.Object, string) {
	if len(args) != 1 {
		return errorf("%s expects 1 argument, got %d", name, len(args)), ""
	}
	path, ok := expectString(args[0], name+" path")
	if !ok {
		return pathErr(name+" path", args[0]), ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Sprintf("%s failed for %q: %v", name, path, err)
	}
	return &object.String{Value: string(data)}, ""
}

func writeFile(name string, args []object.Object) (object.Object, string) {
	path, content, argErr := pathAndContent(name, args)
	if argErr != nil {
		return argErr, ""
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return nil, fmt.Sprintf("%s failed for %q: %v", name, path, err)
	}
	return object.NewInt(int64(len(content))), ""
}

func pathAndContent(name string, args []object.Object) (string, string, object.Object) {
	if len(args) != 2 {
		return "", "", errorf("%s expects 2 arguments, got %d", name, len(args))
	}
	path, ok := expectString(args[0], name+" path")
	if !ok {
		return "", "", pathErr(name+" path", args[0])
	}
	content, ok := expectString(args[1], name+" content")
	if !ok {
		return "", "", pathErr(name+" content", args[1])
	}
	return path, content, nil
}

func appendFile(name string, args []object.Object) (object.Object, string) {
	path, content, argErr := pathAndContent(name, args)
	if argErr != nil {
		return argErr, ""
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Sprintf("%s failed for %q: %v", name, path, err)
	}
	defer f.Close()
	n, err := f.WriteString(content)
	if err != nil {
		return nil, fmt.Sprintf("%s failed for %q: %v", name, path, err)
	}
	return object.NewInt(int64(n)), ""
}

func stringsSplit(args ...object.Object) object.Object {
	if len(args) != 2 {
		return errorf("strings.split expects 2 arguments, got %d", len(args))
	}
	s, ok := expectString(args[0], "strings.split input")
	if !ok {
		return pathErr("strings.split input", args[0])
	}
	sep, ok := expectString(args[1], "strings.split separator")
	if !ok {
		return pathErr("strings.split separator", args[1])
	}
	return stringsToArray(strings.Split(s, sep))
}

func stringsJoin(args ...object.Object) object.Object {
	if len(args) != 2 {
		return errorf("strings.join expects 2 arguments, got %d", len(args))
	}
	arr, ok := args[0].(*object.Array)
	if !ok {
		return errorf("strings.join first argument must be ARRAY, got %s", args[0].Type())
	}
	sep, ok := expectString(args[1], "strings.join separator")
	if !ok {
		return pathErr("strings.join separator", args[1])
	}
	parts := make([]string, 0, len(arr.Elements))
	for _, el := range arr.Elements {
		s, ok := el.(*object.String)
		if !ok {
			return errorf("strings.join array elements must be STRING, got %s", el.Type())
		}
		parts = append(parts, s.Value)
	}
	return &object.String{Value: strings.Join(parts, sep)}
}

func stringsContains(args ...object.Object) object.Object {
	if len(args) != 2 {
		return errorf("strings.contains expects 2 arguments, got %d", len(args))
	}
	a, ok := expectString(args[0], "strings.contains input")
	if !ok {
		return pathErr("strings.contains input", args[0])
	}
	b, ok := expectString(args[1], "strings.contains substr")
	if !ok {
		return pathErr("strings.contains substr", args[1])
	}
	return &object.Boolean{Value: strings.Contains(a, b)}
}

func stringsHasPrefix(args ...object.Object) object.Object {
	if len(args) != 2 {
		return errorf("strings.has_prefix expects 2 arguments, got %d", len(args))
	}
	a, ok := expectString(args[0], "strings.has_prefix input")
	if !ok {
		return pathErr("strings.has_prefix input", args[0])
	}
	b, ok := expectString(args[1], "strings.has_prefix prefix")
	if !ok {
		return pathErr("strings.has_prefix prefix", args[1])
	}
	return &object.Boolean{Value: strings.HasPrefix(a, b)}
}

func stringsHasSuffix(args ...object.Object) object.Object {
	if len(args) != 2 {
		return errorf("strings.has_suffix expects 2 arguments, got %d", len(args))
	}
	a, ok := expectString(args[0], "strings.has_suffix input")
	if !ok {
		return pathErr("strings.has_suffix input", args[0])
	}
	b, ok := expectString(args[1], "strings.has_suffix suffix")
	if !ok {
		return pathErr("strings.has_suffix suffix", args[1])
	}
	return &object.Boolean{Value: strings.HasSuffix(a, b)}
}

func stringsReplace(args ...object.Object) object.Object {
	if len(args) != 3 {
		return errorf("strings.replace expects 3 arguments, got %d", len(args))
	}
	s, ok := expectString(args[0], "strings.replace input")
	if !ok {
		return pathErr("strings.replace input", args[0])
	}
	old, ok := expectString(args[1], "strings.replace old")
	if !ok {
		return pathErr("strings.replace old", args[1])
	}
	newValue, ok := expectString(args[2], "strings.replace new")
	if !ok {
		return pathErr("strings.replace new", args[2])
	}
	return &object.String{Value: strings.ReplaceAll(s, old, newValue)}
}

func arraysAppend(args ...object.Object) object.Object {
	if len(args) != 2 {
		return errorf("arrays.append expects 2 arguments, got %d", len(args))
	}
	arr, ok := args[0].(*object.Array)
	if !ok {
		return errorf("arrays.append first argument must be ARRAY, got %s", args[0].Type())
	}
	out := append([]object.Object{}, arr.Elements...)
	out = append(out, args[1])
	return &object.Array{Elements: out}
}

func arraysFirst(args ...object.Object) object.Object {
	if len(args) != 1 {
		return errorf("arrays.first expects 1 argument, got %d", len(args))
	}
	arr, ok := args[0].(*object.Array)
	if !ok {
		return errorf("arrays.first expects ARRAY, got %s", args[0].Type())
	}
	if len(arr.Elements) == 0 {
		return &object.Nil{}
	}
	return arr.Elements[0]
}

func arraysLast(args ...object.Object) object.Object {
	if len(args) != 1 {
		return errorf("arrays.last expects 1 argument, got %d", len(args))
	}
	arr, ok := args[0].(*object.Array)
	if !ok {
		return errorf("arrays.last expects ARRAY, got %s", args[0].Type())
	}
	if len(arr.Elements) == 0 {
		return &object.Nil{}
	}
	return arr.Elements[len(arr.Elements)-1]
}

func arraysJoin(args ...object.Object) object.Object { return stringsJoin(args...) }

func arraysRange(args ...object.Object) object.Object {
	if len(args) != 2 {
		return errorf("arrays.range expects 2 arguments, got %d", len(args))
	}
	start, ok := args[0].(*object.Integer)
	if !ok {
		return errorf("arrays.range start must be INTEGER, got %s", args[0].Type())
	}
	end, ok := args[1].(*object.Integer)
	if !ok {
		return errorf("arrays.range end must be INTEGER, got %s", args[1].Type())
	}
	elements := []object.Object{}
	step := int64(1)
	if start.Value > end.Value {
		step = -1
	}
	for i := start.Value; ; i += step {
		if step > 0 && i >= end.Value {
			break
		}
		if step < 0 && i <= end.Value {
			break
		}
		elements = append(elements, object.NewInt(i))
	}
	return &object.Array{Elements: elements}
}

func arraysReverse(args ...object.Object) object.Object {
	if len(args) != 1 {
		return errorf("arrays.reverse expects 1 argument, got %d", len(args))
	}
	arr, ok := args[0].(*object.Array)
	if !ok {
		return errorf("arrays.reverse expects ARRAY, got %s", args[0].Type())
	}
	out := append([]object.Object{}, arr.Elements...)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return &object.Array{Elements: out}
}

func arraysSlice(args ...object.Object) object.Object {
	if len(args) != 3 {
		return errorf("arrays.slice expects 3 arguments, got %d", len(args))
	}
	arr, ok := args[0].(*object.Array)
	if !ok {
		return errorf("arrays.slice first argument must be ARRAY, got %s", args[0].Type())
	}
	start, ok := args[1].(*object.Integer)
	if !ok {
		return errorf("arrays.slice start must be INTEGER, got %s", args[1].Type())
	}
	end, ok := args[2].(*object.Integer)
	if !ok {
		return errorf("arrays.slice end must be INTEGER, got %s", args[2].Type())
	}
	if start.Value < 0 || end.Value < start.Value || int(end.Value) > len(arr.Elements) {
		return errorf("arrays.slice bounds out of range: %d..%d for length %d", start.Value, end.Value, len(arr.Elements))
	}
	return &object.Array{Elements: append([]object.Object{}, arr.Elements[int(start.Value):int(end.Value)]...)}
}

func arraysMap(apply Applier) object.BuiltinFunction {
	return func(args ...object.Object) object.Object {
		if len(args) != 2 {
			return errorf("arrays.map expects 2 arguments, got %d", len(args))
		}
		arr, ok := args[0].(*object.Array)
		if !ok {
			return errorf("arrays.map first argument must be ARRAY, got %s", args[0].Type())
		}
		fn := args[1]
		elements := make([]object.Object, 0, len(arr.Elements))
		for _, el := range arr.Elements {
			result := apply(fn, []object.Object{el})
			if stops(result) {
				return result
			}
			elements = append(elements, result)
		}
		return &object.Array{Elements: elements}
	}
}

func arraysFilter(apply Applier) object.BuiltinFunction {
	return func(args ...object.Object) object.Object {
		if len(args) != 2 {
			return errorf("arrays.filter expects 2 arguments, got %d", len(args))
		}
		arr, ok := args[0].(*object.Array)
		if !ok {
			return errorf("arrays.filter first argument must be ARRAY, got %s", args[0].Type())
		}
		fn := args[1]
		elements := []object.Object{}
		for _, el := range arr.Elements {
			result := apply(fn, []object.Object{el})
			if stops(result) {
				return result
			}
			if truthy(result) {
				elements = append(elements, el)
			}
		}
		return &object.Array{Elements: elements}
	}
}

func arraysReduce(apply Applier) object.BuiltinFunction {
	return func(args ...object.Object) object.Object {
		if len(args) != 3 {
			return errorf("arrays.reduce expects 3 arguments, got %d", len(args))
		}
		arr, ok := args[0].(*object.Array)
		if !ok {
			return errorf("arrays.reduce first argument must be ARRAY, got %s", args[0].Type())
		}
		acc := args[1]
		fn := args[2]
		for _, el := range arr.Elements {
			acc = apply(fn, []object.Object{acc, el})
			if stops(acc) {
				return acc
			}
		}
		return acc
	}
}

func mapsGet(args ...object.Object) object.Object {
	if len(args) != 2 && len(args) != 3 {
		return errorf("maps.get expects 2 or 3 arguments, got %d", len(args))
	}
	m, ok := args[0].(*object.Map)
	if !ok {
		return errorf("maps.get first argument must be MAP, got %s", args[0].Type())
	}
	key, ok := args[1].(object.Hashable)
	if !ok {
		return errorf("maps.get key is not hashable: %s", args[1].Type())
	}
	if pair, ok := m.Pairs[key.HashKey()]; ok {
		return pair.Value
	}
	if len(args) == 3 {
		return args[2]
	}
	return &object.Nil{}
}

func mapsHas(args ...object.Object) object.Object {
	if len(args) != 2 {
		return errorf("maps.has expects 2 arguments, got %d", len(args))
	}
	m, ok := args[0].(*object.Map)
	if !ok {
		return errorf("maps.has first argument must be MAP, got %s", args[0].Type())
	}
	key, ok := args[1].(object.Hashable)
	if !ok {
		return errorf("maps.has key is not hashable: %s", args[1].Type())
	}
	_, exists := m.Pairs[key.HashKey()]
	return &object.Boolean{Value: exists}
}

func mapsSet(args ...object.Object) object.Object {
	if len(args) != 3 {
		return errorf("maps.set expects 3 arguments, got %d", len(args))
	}
	m, ok := args[0].(*object.Map)
	if !ok {
		return errorf("maps.set first argument must be MAP, got %s", args[0].Type())
	}
	key, ok := args[1].(object.Hashable)
	if !ok {
		return errorf("maps.set key is not hashable: %s", args[1].Type())
	}
	pairs := map[object.HashKey]object.MapPair{}
	for k, v := range m.Pairs {
		pairs[k] = v
	}
	pairs[key.HashKey()] = object.MapPair{Key: args[1], Value: args[2]}
	return &object.Map{Pairs: pairs}
}

func mapsMerge(args ...object.Object) object.Object {
	if len(args) != 2 {
		return errorf("maps.merge expects 2 arguments, got %d", len(args))
	}
	left, ok := args[0].(*object.Map)
	if !ok {
		return errorf("maps.merge first argument must be MAP, got %s", args[0].Type())
	}
	right, ok := args[1].(*object.Map)
	if !ok {
		return errorf("maps.merge second argument must be MAP, got %s", args[1].Type())
	}
	pairs := map[object.HashKey]object.MapPair{}
	for k, v := range left.Pairs {
		pairs[k] = v
	}
	for k, v := range right.Pairs {
		pairs[k] = v
	}
	return &object.Map{Pairs: pairs}
}

func mapsKeys(args ...object.Object) object.Object {
	if len(args) != 1 {
		return errorf("maps.keys expects 1 argument, got %d", len(args))
	}
	m, ok := args[0].(*object.Map)
	if !ok {
		return errorf("maps.keys expects MAP, got %s", args[0].Type())
	}
	keys := []object.Object{}
	for _, pair := range m.Pairs {
		keys = append(keys, pair.Key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Inspect() < keys[j].Inspect() })
	return &object.Array{Elements: keys}
}

func mapsValues(args ...object.Object) object.Object {
	if len(args) != 1 {
		return errorf("maps.values expects 1 argument, got %d", len(args))
	}
	m, ok := args[0].(*object.Map)
	if !ok {
		return errorf("maps.values expects MAP, got %s", args[0].Type())
	}
	keys := make([]object.HashKey, 0, len(m.Pairs))
	for k := range m.Pairs {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return m.Pairs[keys[i]].Key.Inspect() < m.Pairs[keys[j]].Key.Inspect() })
	values := []object.Object{}
	for _, k := range keys {
		values = append(values, m.Pairs[k].Value)
	}
	return &object.Array{Elements: values}
}

func jsonParse(name string, args []object.Object) (object.Object, string) {
	if len(args) != 1 {
		return errorf("%s expects 1 argument, got %d", name, len(args)), ""
	}
	s, ok := expectString(args[0], name+" input")
	if !ok {
		return pathErr(name+" input", args[0]), ""
	}
	var data interface{}
	if err := json.Unmarshal([]byte(s), &data); err != nil {
		return nil, fmt.Sprintf("%s failed: %v", name, err)
	}
	return fromGoValue(data), ""
}

func jsonStringify(args ...object.Object) object.Object {
	if len(args) != 1 {
		return errorf("json.stringify expects 1 argument, got %d", len(args))
	}
	data, err := toGoValue(args[0])
	if err != nil {
		return errorf("json.stringify failed: %v", err)
	}
	bytes, err := json.Marshal(data)
	if err != nil {
		return errorf("json.stringify failed: %v", err)
	}
	return &object.String{Value: string(bytes)}
}

func processEnv(args ...object.Object) object.Object {
	if len(args) != 1 {
		return errorf("process.env expects 1 argument, got %d", len(args))
	}
	name, ok := expectString(args[0], "process.env name")
	if !ok {
		return pathErr("process.env name", args[0])
	}
	return &object.String{Value: os.Getenv(name)}
}

func processRun(name string, args []object.Object) (object.Object, string) {
	if len(args) < 1 {
		return errorf("%s expects at least 1 argument, got %d", name, len(args)), ""
	}
	command, ok := expectString(args[0], name+" command")
	if !ok {
		return pathErr(name+" command", args[0]), ""
	}
	cmdArgs := []string{}
	for _, arg := range args[1:] {
		s, ok := expectString(arg, name+" argument")
		if !ok {
			return pathErr(name+" argument", arg), ""
		}
		cmdArgs = append(cmdArgs, s)
	}
	return runCommand(name, exec.Command(command, cmdArgs...))
}

func shellRun(name string, args []object.Object) (object.Object, string) {
	if len(args) != 1 {
		return errorf("%s expects 1 argument, got %d", name, len(args)), ""
	}
	command, ok := expectString(args[0], name+" command")
	if !ok {
		return pathErr(name+" command", args[0]), ""
	}
	return runCommand(name, exec.Command("sh", "-c", command))
}

func runCommand(name string, cmd *exec.Cmd) (object.Object, string) {
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Sprintf("%s failed: %v: %s", name, err, strings.TrimSpace(string(output)))
	}
	return &object.String{Value: string(output)}, ""
}

func timeFormat(args ...object.Object) object.Object {
	if len(args) != 2 {
		return errorf("time.format expects 2 arguments, got %d", len(args))
	}
	value, ok := expectString(args[0], "time.format time")
	if !ok {
		return pathErr("time.format time", args[0])
	}
	layout, ok := expectString(args[1], "time.format layout")
	if !ok {
		return pathErr("time.format layout", args[1])
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return errorf("time.format expects RFC3339 time: %v", err)
	}
	return &object.String{Value: t.Format(layout)}
}

func oneString(name string, fn func(string) string) object.BuiltinFunction {
	return func(args ...object.Object) object.Object {
		if len(args) != 1 {
			return errorf("%s expects 1 argument, got %d", name, len(args))
		}
		s, ok := expectString(args[0], name+" input")
		if !ok {
			return pathErr(name+" input", args[0])
		}
		return &object.String{Value: fn(s)}
	}
}

func mathOne(name string, fn func(float64) float64) object.BuiltinFunction {
	return func(args ...object.Object) object.Object {
		if len(args) != 1 {
			return errorf("%s expects 1 argument, got %d", name, len(args))
		}
		v, ok := number(args[0])
		if !ok {
			return errorf("%s expects number, got %s", name, args[0].Type())
		}
		return &object.Float{Value: fn(v)}
	}
}

func mathTwo(name string, fn func(float64, float64) float64) object.BuiltinFunction {
	return func(args ...object.Object) object.Object {
		if len(args) != 2 {
			return errorf("%s expects 2 arguments, got %d", name, len(args))
		}
		a, ok := number(args[0])
		if !ok {
			return errorf("%s expects number, got %s", name, args[0].Type())
		}
		b, ok := number(args[1])
		if !ok {
			return errorf("%s expects number, got %s", name, args[1].Type())
		}
		return &object.Float{Value: fn(a, b)}
	}
}

func joinArgs(args []object.Object) string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		parts = append(parts, arg.Inspect())
	}
	return strings.Join(parts, " ")
}

func expectString(obj object.Object, _ string) (string, bool) {
	s, ok := obj.(*object.String)
	if !ok {
		return "", false
	}
	return s.Value, true
}

func pathErr(name string, obj object.Object) object.Object {
	return errorf("%s must be STRING, got %s", name, obj.Type())
}

func number(obj object.Object) (float64, bool) {
	switch obj := obj.(type) {
	case *object.Integer:
		return float64(obj.Value), true
	case *object.Float:
		return obj.Value, true
	default:
		return 0, false
	}
}

func stringsToArray(values []string) object.Object {
	elements := make([]object.Object, 0, len(values))
	for _, value := range values {
		elements = append(elements, &object.String{Value: value})
	}
	return &object.Array{Elements: elements}
}

func fromGoValue(v interface{}) object.Object {
	switch v := v.(type) {
	case nil:
		return &object.Nil{}
	case bool:
		return &object.Boolean{Value: v}
	case float64:
		if math.Trunc(v) == v {
			return object.NewInt(int64(v))
		}
		return &object.Float{Value: v}
	case string:
		return &object.String{Value: v}
	case []interface{}:
		elements := make([]object.Object, 0, len(v))
		for _, item := range v {
			elements = append(elements, fromGoValue(item))
		}
		return &object.Array{Elements: elements}
	case map[string]interface{}:
		pairs := map[object.HashKey]object.MapPair{}
		for k, val := range v {
			key := &object.String{Value: k}
			pairs[key.HashKey()] = object.MapPair{Key: key, Value: fromGoValue(val)}
		}
		return &object.Map{Pairs: pairs}
	default:
		return &object.Nil{}
	}
}

func toGoValue(obj object.Object) (interface{}, error) {
	switch obj := obj.(type) {
	case *object.Nil:
		return nil, nil
	case *object.Boolean:
		return obj.Value, nil
	case *object.Integer:
		return obj.Value, nil
	case *object.Float:
		return obj.Value, nil
	case *object.String:
		return obj.Value, nil
	case *object.Array:
		out := make([]interface{}, 0, len(obj.Elements))
		for _, el := range obj.Elements {
			v, err := toGoValue(el)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case *object.Map:
		out := map[string]interface{}{}
		for _, pair := range obj.Pairs {
			key, ok := pair.Key.(*object.String)
			if !ok {
				return nil, fmt.Errorf("JSON object keys must be STRING, got %s", pair.Key.Type())
			}
			v, err := toGoValue(pair.Value)
			if err != nil {
				return nil, err
			}
			out[key.Value] = v
		}
		return out, nil
	default:
		return nil, fmt.Errorf("cannot encode %s", obj.Type())
	}
}

func truthy(obj object.Object) bool {
	switch obj := obj.(type) {
	case *object.Nil:
		return false
	case *object.Boolean:
		return obj.Value
	default:
		return true
	}
}

func errorf(format string, a ...interface{}) object.Object {
	return &object.Error{Message: fmt.Sprintf(format, a...)}
}
