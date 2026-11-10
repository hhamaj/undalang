package builtins

import (
	"fmt"
	"strings"

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
