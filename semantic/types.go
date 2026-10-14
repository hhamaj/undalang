package semantic

import (
	"fmt"
	"math/bits"
	"sort"
	"strings"

	"undalang/ast"
)

// Kind is a bit set of the runtime value kinds an expression can produce. a
// set with exactly one bit is a statically known type. checks only fire on
// known types so dynamic code keeps working as before
type Kind uint16

const (
	KInt Kind = 1 << iota
	KFloat
	KString
	KBool
	KNil
	KArray
	KMap
	KFunction
	KBuiltin
	KModule
	KResult
	KStruct
	KStructDef
	KPointer

	KNever   Kind = 0
	KNumber       = KInt | KFloat
	KCall         = KFunction | KBuiltin
	KHash         = KInt | KString | KBool | KNil
	KUnknown      = KInt | KFloat | KString | KBool | KNil | KArray | KMap | KFunction | KBuiltin | KModule | KResult | KStruct | KStructDef | KPointer
)

var kindNames = []struct {
	kind Kind
	name string
}{
	{KInt, "INTEGER"}, {KFloat, "FLOAT"}, {KString, "STRING"}, {KBool, "BOOLEAN"},
	{KNil, "NIL"}, {KArray, "ARRAY"}, {KMap, "MAP"}, {KFunction, "FUNCTION"},
	{KBuiltin, "BUILTIN"}, {KModule, "MODULE"}, {KResult, "RESULT"},
	{KStruct, "STRUCT VALUE"}, {KStructDef, "STRUCT"}, {KPointer, "POINTER"},
}

// Known reports a single statically known kind. struct values don't count:
// their runtime type name is the struct's name, which the generic checks
// can't spell, so those leave them to the runtime
func (k Kind) Known() bool { return bits.OnesCount16(uint16(k)) == 1 && k != KStruct }

// String uses the runtime's type names so static and runtime messages match
func (k Kind) String() string {
	if k == KUnknown {
		return "UNKNOWN"
	}
	parts := []string{}
	for _, kn := range kindNames {
		if k&kn.kind != 0 {
			parts = append(parts, kn.name)
		}
	}
	if len(parts) == 0 {
		return "NEVER"
	}
	return strings.Join(parts, "|")
}

// Type is the static approximation of a value
type Type struct {
	Kind Kind
	// Arity is the parameter count when Kind is KFunction and we know the
	// function literal, -1 otherwise
	Arity   int
	Builtin *Signature
	Module  *ModuleType
	// Func is set for a known function literal, calls check their arguments
	// against its parameter types
	Func *ast.FunctionLiteral
	// Struct is the declaration behind a KStruct value or a KStructDef
	// constructor, when it's known
	Struct *ast.StructStatement
}

// Name spells the type the way the runtime does, so a struct value is its
// struct's name
func (t Type) Name() string {
	if t.Kind == KStruct && t.Struct != nil {
		return t.Struct.Name.Value
	}
	return t.Kind.String()
}

// AnnotationKind turns a type annotation into the kinds it accepts, nil
// means anything goes
func AnnotationKind(t *ast.TypeAnnotation) Kind {
	if t == nil || t.Name == "any" {
		return KUnknown
	}
	if t.IsStruct() {
		return KStruct
	}
	var k Kind
	for _, name := range ast.TypeNames[t.Name] {
		for _, kn := range kindNames {
			if kn.name == name {
				k |= kn.kind
			}
		}
	}
	return k
}

// fits reports whether a value of type t could fit annotation a. a value
// whose kinds don't overlap the annotation at all can never fit
func fits(t Type, a *ast.TypeAnnotation) bool {
	if a == nil || t.Kind == KNever {
		return true
	}
	if a.IsStruct() && t.Kind == KStruct && t.Struct != nil {
		return t.Struct.Name.Value == a.Name
	}
	return t.Kind&AnnotationKind(a) != 0
}

// ModuleType lists the module members known at compile time
type ModuleType struct {
	Name    string
	Members map[string]Type
	// Std marks standard modules, their members are builtins
	Std bool
}

func unknown() Type        { return Type{Kind: KUnknown, Arity: -1} }
func kindType(k Kind) Type { return Type{Kind: k, Arity: -1} }

func join(a, b Type) Type {
	if a.Kind == KNever {
		return b
	}
	if b.Kind == KNever {
		return a
	}
	if a.Kind == b.Kind && a.Arity == b.Arity && a.Builtin == b.Builtin && a.Module == b.Module && a.Func == b.Func && a.Struct == b.Struct {
		return a
	}
	return kindType(a.Kind | b.Kind)
}

// Signature describes a builtin function for arity and argument checks
type Signature struct {
	Name    string
	MinArgs int
	MaxArgs int // -1 means variadic
	Params  []Kind
	Returns Kind
	// NativeUnsupported is set for builtins the C backend doesn't implement
	NativeUnsupported bool
}

func (s *Signature) arityMessage(got int) string {
	var want string
	switch {
	case s.MaxArgs < 0:
		want = fmt.Sprintf("at least %d %s", s.MinArgs, plural(s.MinArgs))
	case s.MinArgs == s.MaxArgs:
		want = fmt.Sprintf("%d %s", s.MinArgs, plural(s.MinArgs))
	default:
		want = fmt.Sprintf("%d or %d arguments", s.MinArgs, s.MaxArgs)
	}
	return fmt.Sprintf("%s expects %s, got %d", s.Name, want, got)
}

func plural(n int) string {
	if n == 1 {
		return "argument"
	}
	return "arguments"
}

func sig(name string, min, max int, returns Kind, params ...Kind) *Signature {
	return &Signature{Name: name, MinArgs: min, MaxArgs: max, Params: params, Returns: returns}
}

// GlobalBuiltins mirrors builtins.New
var GlobalBuiltins = map[string]*Signature{
	"print":       sig("print", 0, -1, KNil),
	"println":     sig("println", 0, -1, KNil),
	"len":         sig("len", 1, 1, KInt, KString|KArray|KMap),
	"type":        sig("type", 1, 1, KString, KUnknown),
	"ok":          sig("ok", 1, 1, KResult, KUnknown),
	"err":         sig("err", 1, 1, KResult, KUnknown),
	"read_file":   sig("read_file", 1, 1, KString, KString),
	"write_file":  sig("write_file", 2, 2, KInt, KString, KString),
	"append_file": sig("append_file", 2, 2, KInt, KString, KString),

	"try_read_file":   sig("try_read_file", 1, 1, KResult, KString),
	"try_write_file":  sig("try_write_file", 2, 2, KResult, KString, KString),
	"try_append_file": sig("try_append_file", 2, 2, KResult, KString, KString),
}

// StdModules mirrors builtins.StdModules
var StdModules = map[string]map[string]*Signature{
	"strings": {
		"contains":   sig("strings.contains", 2, 2, KBool, KString, KString),
		"has_prefix": sig("strings.has_prefix", 2, 2, KBool, KString, KString),
		"has_suffix": sig("strings.has_suffix", 2, 2, KBool, KString, KString),
		"join":       sig("strings.join", 2, 2, KString, KArray, KString),
		"lower":      sig("strings.lower", 1, 1, KString, KString),
		"replace":    sig("strings.replace", 3, 3, KString, KString, KString, KString),
		"split":      sig("strings.split", 2, 2, KArray, KString, KString),
		"trim":       sig("strings.trim", 1, 1, KString, KString),
		"upper":      sig("strings.upper", 1, 1, KString, KString),
	},
	"arrays": {
		"append":  sig("arrays.append", 2, 2, KArray, KArray, KUnknown),
		"filter":  sig("arrays.filter", 2, 2, KArray, KArray, KUnknown),
		"first":   sig("arrays.first", 1, 1, KUnknown, KArray),
		"join":    sig("arrays.join", 2, 2, KString, KArray, KString),
		"last":    sig("arrays.last", 1, 1, KUnknown, KArray),
		"map":     sig("arrays.map", 2, 2, KArray, KArray, KUnknown),
		"range":   sig("arrays.range", 2, 2, KArray, KInt, KInt),
		"reduce":  sig("arrays.reduce", 3, 3, KUnknown, KArray, KUnknown, KUnknown),
		"reverse": sig("arrays.reverse", 1, 1, KArray, KArray),
		"slice":   sig("arrays.slice", 3, 3, KArray, KArray, KInt, KInt),
	},
	"maps": {
		"get":    sig("maps.get", 2, 3, KUnknown, KMap, KUnknown, KUnknown),
		"has":    sig("maps.has", 2, 2, KBool, KMap, KUnknown),
		"keys":   sig("maps.keys", 1, 1, KArray, KMap),
		"merge":  sig("maps.merge", 2, 2, KMap, KMap, KMap),
		"set":    sig("maps.set", 3, 3, KMap, KMap, KUnknown, KUnknown),
		"values": sig("maps.values", 1, 1, KArray, KMap),
	},
	"json": {
		"parse":     sig("json.parse", 1, 1, KUnknown, KString),
		"stringify": sig("json.stringify", 1, 1, KString, KUnknown),
		"try_parse": sig("json.try_parse", 1, 1, KResult, KString),
	},
	"math": {
		"abs":   sig("math.abs", 1, 1, KFloat, KNumber),
		"ceil":  sig("math.ceil", 1, 1, KFloat, KNumber),
		"floor": sig("math.floor", 1, 1, KFloat, KNumber),
		"max":   sig("math.max", 2, 2, KFloat, KNumber, KNumber),
		"min":   sig("math.min", 2, 2, KFloat, KNumber, KNumber),
		"pow":   sig("math.pow", 2, 2, KFloat, KNumber, KNumber),
		"round": sig("math.round", 1, 1, KFloat, KNumber),
		"sqrt":  sig("math.sqrt", 1, 1, KFloat, KNumber),
	},
	"time": {
		"format": {Name: "time.format", MinArgs: 2, MaxArgs: 2, Params: []Kind{KString, KString}, Returns: KString, NativeUnsupported: true},
		"now":    sig("time.now", 0, 0, KString),
		"unix":   sig("time.unix", 0, 0, KInt),
	},
	"process": {
		"args":    sig("process.args", 0, 0, KArray),
		"env":     sig("process.env", 1, 1, KString, KString),
		"run":     sig("process.run", 1, -1, KString, KString),
		"try_run": sig("process.try_run", 1, -1, KResult, KString),
	},
	"shell": {
		"run":     sig("shell.run", 1, 1, KString, KString),
		"try_run": sig("shell.try_run", 1, 1, KResult, KString),
	},
}

func stdModuleType(name string) *ModuleType {
	members := map[string]Type{}
	for member, s := range StdModules[name] {
		members[member] = Type{Kind: KBuiltin, Arity: -1, Builtin: s}
	}
	return &ModuleType{Name: name, Members: members, Std: true}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// infixResult mirrors evaluator.evalInfixExpression for statically known
// kinds. it returns the result kind or the runtime error message
func infixResult(op string, l, r Kind) (Kind, string) {
	isNum := func(k Kind) bool { return k == KInt || k == KFloat }
	switch {
	case isNum(l) && isNum(r):
		switch op {
		case "<", ">", "<=", ">=", "==", "!=":
			return KBool, ""
		case "+", "-", "*", "/":
			if l == KInt && r == KInt {
				return KInt, ""
			}
			return KFloat, ""
		case "%":
			if l == KInt && r == KInt {
				return KInt, ""
			}
			return KNever, "operator % only supports integers"
		}
		return KNever, fmt.Sprintf("unknown operator: %s %s %s", l, op, r)
	case op == "==" || op == "!=":
		return KBool, ""
	case l == KString && r == KString:
		if op == "+" {
			return KString, ""
		}
		return KNever, fmt.Sprintf("unknown operator: STRING %s STRING", op)
	case op == "&&" || op == "||":
		return KBool, ""
	case l != r:
		return KNever, fmt.Sprintf("type mismatch: %s %s %s", l, op, r)
	default:
		return KNever, fmt.Sprintf("unknown operator: %s %s %s", l, op, r)
	}
}
