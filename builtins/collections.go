package builtins

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"undalang/object"
)

// order compares two values for sorting: numbers with numbers, strings with
// strings, arrays element by element. anything else can't be ordered and
// gives a message instead. compiler/runtime has the same rules in u_order
func order(a, b object.Object) (int, string) {
	switch a := a.(type) {
	case *object.Integer:
		switch b := b.(type) {
		case *object.Integer:
			return cmp3(a.Value < b.Value, a.Value > b.Value), ""
		case *object.Float:
			return cmp3(float64(a.Value) < b.Value, float64(a.Value) > b.Value), ""
		}
	case *object.Float:
		switch b := b.(type) {
		case *object.Integer:
			return cmp3(a.Value < float64(b.Value), a.Value > float64(b.Value)), ""
		case *object.Float:
			return cmp3(a.Value < b.Value, a.Value > b.Value), ""
		}
	case *object.String:
		if b, ok := b.(*object.String); ok {
			return strings.Compare(a.Value, b.Value), ""
		}
	case *object.Array:
		if b, ok := b.(*object.Array); ok {
			for i := 0; i < len(a.Elements) && i < len(b.Elements); i++ {
				if c, msg := order(a.Elements[i], b.Elements[i]); c != 0 || msg != "" {
					return c, msg
				}
			}
			return cmp3(len(a.Elements) < len(b.Elements), len(a.Elements) > len(b.Elements)), ""
		}
	}
	return 0, fmt.Sprintf("can't order %s and %s", a.Type(), b.Type())
}

func cmp3(less, greater bool) int {
	switch {
	case less:
		return -1
	case greater:
		return 1
	}
	return 0
}

// sortStable sorts xs by keys, keeping equal keys in their original order.
// it's a plain top-down merge sort, the same one as u_merge_sort in the C
// runtime, so both compare the same pairs and fail on the same one
func sortStable(name string, xs, keys []object.Object) object.Object {
	idx := make([]int, len(xs))
	for i := range idx {
		idx[i] = i
	}
	tmp := make([]int, len(xs))
	if msg := mergeSort(idx, tmp, keys); msg != "" {
		return errorf("%s: %s", name, msg)
	}
	out := make([]object.Object, len(xs))
	for i, j := range idx {
		out[i] = xs[j]
	}
	return &object.Array{Elements: out}
}

func mergeSort(idx, tmp []int, keys []object.Object) string {
	if len(idx) < 2 {
		return ""
	}
	mid := len(idx) / 2
	if msg := mergeSort(idx[:mid], tmp[:mid], keys); msg != "" {
		return msg
	}
	if msg := mergeSort(idx[mid:], tmp[mid:], keys); msg != "" {
		return msg
	}
	copy(tmp, idx)
	i, j, k := 0, mid, 0
	for i < mid && j < len(idx) {
		c, msg := order(keys[tmp[j]], keys[tmp[i]])
		if msg != "" {
			return msg
		}
		// take from the right only when strictly smaller, that keeps it stable
		if c < 0 {
			idx[k] = tmp[j]
			j++
		} else {
			idx[k] = tmp[i]
			i++
		}
		k++
	}
	k += copy(idx[k:], tmp[i:mid])
	copy(idx[k:], tmp[j:])
	return ""
}

func wantArray(name string, args []object.Object, n int) (*object.Array, object.Object) {
	if len(args) != n {
		return nil, errorf("%s expects %d arguments, got %d", name, n, len(args))
	}
	arr, ok := args[0].(*object.Array)
	if !ok {
		return nil, errorf("%s first argument must be ARRAY, got %s", name, args[0].Type())
	}
	return arr, nil
}

func arraysSort(args ...object.Object) object.Object {
	if len(args) != 1 {
		return errorf("arrays.sort expects 1 argument, got %d", len(args))
	}
	arr, ok := args[0].(*object.Array)
	if !ok {
		return errorf("arrays.sort expects ARRAY, got %s", args[0].Type())
	}
	return sortStable("arrays.sort", arr.Elements, arr.Elements)
}

func arraysSortBy(apply Applier) object.BuiltinFunction {
	return func(args ...object.Object) object.Object {
		arr, e := wantArray("arrays.sort_by", args, 2)
		if e != nil {
			return e
		}
		keys := make([]object.Object, len(arr.Elements))
		for i, el := range arr.Elements {
			keys[i] = apply(args[1], []object.Object{el})
			if stops(keys[i]) {
				return keys[i]
			}
		}
		return sortStable("arrays.sort_by", arr.Elements, keys)
	}
}

func arraysIndexOf(args ...object.Object) object.Object {
	arr, e := wantArray("arrays.index_of", args, 2)
	if e != nil {
		return e
	}
	for i, el := range arr.Elements {
		if object.Equal(el, args[1]) {
			return object.NewInt(int64(i))
		}
	}
	return object.NewInt(-1)
}

func arraysContains(args ...object.Object) object.Object {
	arr, e := wantArray("arrays.contains", args, 2)
	if e != nil {
		return e
	}
	for _, el := range arr.Elements {
		if object.Equal(el, args[1]) {
			return &object.Boolean{Value: true}
		}
	}
	return &object.Boolean{Value: false}
}

func arraysSum(args ...object.Object) object.Object {
	if len(args) != 1 {
		return errorf("arrays.sum expects 1 argument, got %d", len(args))
	}
	arr, ok := args[0].(*object.Array)
	if !ok {
		return errorf("arrays.sum expects ARRAY, got %s", args[0].Type())
	}
	var isum int64
	var fsum float64
	float := false
	for i, el := range arr.Elements {
		switch v := el.(type) {
		case *object.Integer:
			isum += v.Value
			fsum += float64(v.Value)
		case *object.Float:
			float = true
			fsum += v.Value
		default:
			return errorf("arrays.sum element %d is %s, not a number", i, el.Type())
		}
	}
	if float {
		return &object.Float{Value: fsum}
	}
	return object.NewInt(isum)
}

// arraysTest covers any, all and find, which only differ in when they stop
func arraysTest(name string, apply Applier, done func(hit bool) bool, result func(el object.Object, stopped bool) object.Object) object.BuiltinFunction {
	return func(args ...object.Object) object.Object {
		arr, e := wantArray(name, args, 2)
		if e != nil {
			return e
		}
		for _, el := range arr.Elements {
			r := apply(args[1], []object.Object{el})
			if stops(r) {
				return r
			}
			if done(truthy(r)) {
				return result(el, true)
			}
		}
		return result(nil, false)
	}
}

func arraysAny(apply Applier) object.BuiltinFunction {
	return arraysTest("arrays.any", apply, func(hit bool) bool { return hit },
		func(_ object.Object, stopped bool) object.Object { return &object.Boolean{Value: stopped} })
}

func arraysAll(apply Applier) object.BuiltinFunction {
	return arraysTest("arrays.all", apply, func(hit bool) bool { return !hit },
		func(_ object.Object, stopped bool) object.Object { return &object.Boolean{Value: !stopped} })
}

func arraysFind(apply Applier) object.BuiltinFunction {
	return arraysTest("arrays.find", apply, func(hit bool) bool { return hit },
		func(el object.Object, stopped bool) object.Object {
			if stopped {
				return el
			}
			return &object.Nil{}
		})
}

func mapsDelete(args ...object.Object) object.Object {
	if len(args) != 2 {
		return errorf("maps.delete expects 2 arguments, got %d", len(args))
	}
	m, ok := args[0].(*object.Map)
	if !ok {
		return errorf("maps.delete first argument must be MAP, got %s", args[0].Type())
	}
	key, ok := args[1].(object.Hashable)
	if !ok {
		return errorf("maps.delete key is not hashable: %s", args[1].Type())
	}
	out := &object.Map{Pairs: make(map[object.HashKey]object.MapPair, len(m.Pairs))}
	for k, v := range m.Pairs {
		if k != key.HashKey() {
			out.Pairs[k] = v
		}
	}
	return out
}

// mapsEntries gives [key, value] pairs in the order for loops use
func mapsEntries(args ...object.Object) object.Object {
	if len(args) != 1 {
		return errorf("maps.entries expects 1 argument, got %d", len(args))
	}
	m, ok := args[0].(*object.Map)
	if !ok {
		return errorf("maps.entries expects MAP, got %s", args[0].Type())
	}
	keys, ok := mapsKeys(m).(*object.Array)
	if !ok {
		return errorf("maps.entries: could not list keys")
	}
	out := make([]object.Object, 0, len(keys.Elements))
	for _, k := range keys.Elements {
		pair := m.Pairs[k.(object.Hashable).HashKey()]
		out = append(out, &object.Array{Elements: []object.Object{pair.Key, pair.Value}})
	}
	return &object.Array{Elements: out}
}

func wantStrings(name string, args []object.Object, n int) ([]string, object.Object) {
	if len(args) != n {
		return nil, errorf("%s expects %d arguments, got %d", name, n, len(args))
	}
	out := make([]string, 0, n)
	for i, a := range args {
		s, ok := a.(*object.String)
		if !ok {
			return nil, errorf("%s argument %d must be STRING, got %s", name, i+1, a.Type())
		}
		out = append(out, s.Value)
	}
	return out, nil
}

// stringsIndexOf counts in characters like indexing does, -1 when missing
func stringsIndexOf(args ...object.Object) object.Object {
	s, e := wantStrings("strings.index_of", args, 2)
	if e != nil {
		return e
	}
	i := strings.Index(s[0], s[1])
	if i < 0 {
		return object.NewInt(-1)
	}
	return object.NewInt(int64(utf8.RuneCountInString(s[0][:i])))
}

func stringsChars(args ...object.Object) object.Object {
	s, e := wantStrings("strings.chars", args, 1)
	if e != nil {
		return e
	}
	// like strings.split(s, ""), a broken byte is its own character
	out := []object.Object{}
	for _, c := range strings.Split(s[0], "") {
		out = append(out, &object.String{Value: c})
	}
	return &object.Array{Elements: out}
}

// stringsLines splits on \n, drops a \r before it and doesn't give an empty
// last line for text that ends in a newline
func stringsLines(args ...object.Object) object.Object {
	s, e := wantStrings("strings.lines", args, 1)
	if e != nil {
		return e
	}
	text := strings.TrimSuffix(s[0], "\n")
	out := []object.Object{}
	if text == "" && s[0] == "" {
		return &object.Array{Elements: out}
	}
	for _, line := range strings.Split(text, "\n") {
		out = append(out, &object.String{Value: strings.TrimSuffix(line, "\r")})
	}
	return &object.Array{Elements: out}
}

func stringsRepeat(args ...object.Object) object.Object {
	if len(args) != 2 {
		return errorf("strings.repeat expects 2 arguments, got %d", len(args))
	}
	s, ok := args[0].(*object.String)
	if !ok {
		return errorf("strings.repeat first argument must be STRING, got %s", args[0].Type())
	}
	n, ok := args[1].(*object.Integer)
	if !ok {
		return errorf("strings.repeat count must be INTEGER, got %s", args[1].Type())
	}
	if n.Value < 0 {
		return errorf("strings.repeat count can't be negative: %d", n.Value)
	}
	if len(s.Value) > 0 && n.Value > int64(1<<31)/int64(len(s.Value)) {
		return errorf("strings.repeat result too long")
	}
	return &object.String{Value: strings.Repeat(s.Value, int(n.Value))}
}

// stringsPad pads to a width in characters with a one character fill
func stringsPad(name string, left bool) object.BuiltinFunction {
	return func(args ...object.Object) object.Object {
		if len(args) != 2 && len(args) != 3 {
			return errorf("%s expects 2 or 3 arguments, got %d", name, len(args))
		}
		s, ok := args[0].(*object.String)
		if !ok {
			return errorf("%s first argument must be STRING, got %s", name, args[0].Type())
		}
		width, ok := args[1].(*object.Integer)
		if !ok {
			return errorf("%s width must be INTEGER, got %s", name, args[1].Type())
		}
		fill := " "
		if len(args) == 3 {
			f, ok := args[2].(*object.String)
			if !ok || utf8.RuneCountInString(f.Value) != 1 {
				return errorf("%s fill must be a one character STRING", name)
			}
			fill = f.Value
		}
		n := width.Value - int64(utf8.RuneCountInString(s.Value))
		if n <= 0 {
			return s
		}
		if n > 1<<31 {
			return errorf("%s result too long", name)
		}
		pad := strings.Repeat(fill, int(n))
		if left {
			return &object.String{Value: pad + s.Value}
		}
		return &object.String{Value: s.Value + pad}
	}
}
