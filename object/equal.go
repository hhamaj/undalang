package object

// Equal is ==. numbers of different kinds are never equal here, the
// operator handles 1 == 1.0 before it gets this far
func Equal(left, right Object) bool {
	switch left := left.(type) {
	case *Boolean:
		right, ok := right.(*Boolean)
		return ok && left.Value == right.Value
	case *Nil:
		_, ok := right.(*Nil)
		return ok
	case *String:
		right, ok := right.(*String)
		return ok && left.Value == right.Value
	case *Integer:
		right, ok := right.(*Integer)
		return ok && left.Value == right.Value
	case *Float:
		right, ok := right.(*Float)
		return ok && left.Value == right.Value
	case *Array:
		right, ok := right.(*Array)
		if !ok || len(left.Elements) != len(right.Elements) {
			return false
		}
		for i := range left.Elements {
			if !Equal(left.Elements[i], right.Elements[i]) {
				return false
			}
		}
		return true
	case *Result:
		right, ok := right.(*Result)
		return ok && left.Ok == right.Ok && Equal(left.Value, right.Value)
	case *Struct:
		right, ok := right.(*Struct)
		if !ok || left.Def != right.Def {
			return false
		}
		for i := range left.Fields {
			if !Equal(left.Fields[i], right.Fields[i]) {
				return false
			}
		}
		return true
	case *Map:
		right, ok := right.(*Map)
		if !ok || len(left.Pairs) != len(right.Pairs) {
			return false
		}
		for key, leftPair := range left.Pairs {
			rightPair, ok := right.Pairs[key]
			if !ok || !Equal(leftPair.Value, rightPair.Value) {
				return false
			}
		}
		return true
	default:
		return left == right
	}
}
