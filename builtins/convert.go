package builtins

import (
	"math"
	"strconv"
	"strings"

	"undalang/object"
)

// int, float and string convert between the basic types. a string that
// doesn't hold a number is a failure (try_int and try_float return it as an
// err), any other kind of argument is a bug in the program.
// both backends accept exactly the same text, so the grammar is checked here
// instead of leaning on strconv, which takes more ("1_000", "inf", "0x10")

// asciiSpace is the whitespace allowed around a number, the same set as C's isspace
const asciiSpace = " \t\n\v\f\r"

// intText reports whether s is an optional sign followed by decimal digits
func intText(s string) bool {
	if s != "" && (s[0] == '+' || s[0] == '-') {
		s = s[1:]
	}
	return s != "" && strings.Trim(s, "0123456789") == ""
}

// floatText is intText with an optional fraction and exponent, at least one
// digit in front of the exponent
func floatText(s string) bool {
	i := 0
	digits := func() int {
		start := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		return i - start
	}
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	n := digits()
	if i < len(s) && s[i] == '.' {
		i++
		n += digits()
	}
	if n == 0 {
		return false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		if digits() == 0 {
			return false
		}
	}
	return i == len(s)
}

func toInt(name string, args []object.Object) (object.Object, string) {
	if len(args) != 1 {
		return errorf("%s expects 1 argument, got %d", name, len(args)), ""
	}
	switch arg := args[0].(type) {
	case *object.Integer:
		return arg, ""
	case *object.Float:
		// 2^63 is the first float past the int range, -2^63 still fits
		if math.IsNaN(arg.Value) || arg.Value >= 9223372036854775808.0 || arg.Value < -9223372036854775808.0 {
			return nil, name + ": float is out of range"
		}
		return object.NewInt(int64(arg.Value)), ""
	case *object.String:
		s := strings.Trim(arg.Value, asciiSpace)
		if !intText(s) {
			return nil, name + `: not an integer: "` + arg.Value + `"`
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, name + `: out of range: "` + arg.Value + `"`
		}
		return object.NewInt(n), ""
	}
	return errorf("%s argument must be INTEGER, FLOAT or STRING, got %s", name, args[0].Type()), ""
}

func toFloat(name string, args []object.Object) (object.Object, string) {
	if len(args) != 1 {
		return errorf("%s expects 1 argument, got %d", name, len(args)), ""
	}
	switch arg := args[0].(type) {
	case *object.Integer:
		return &object.Float{Value: float64(arg.Value)}, ""
	case *object.Float:
		return arg, ""
	case *object.String:
		s := strings.Trim(arg.Value, asciiSpace)
		if !floatText(s) {
			return nil, name + `: not a number: "` + arg.Value + `"`
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, name + `: out of range: "` + arg.Value + `"`
		}
		return &object.Float{Value: f}, ""
	}
	return errorf("%s argument must be INTEGER, FLOAT or STRING, got %s", name, args[0].Type()), ""
}

// toString gives the text println would print for the value
func toString(args ...object.Object) object.Object {
	if len(args) != 1 {
		return errorf("string expects 1 argument, got %d", len(args))
	}
	if s, ok := args[0].(*object.String); ok {
		return s
	}
	return &object.String{Value: args[0].Inspect()}
}
