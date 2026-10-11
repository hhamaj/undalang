package evaluator

import (
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"undalang/ast"
	"undalang/builtins"
	"undalang/diag"
	"undalang/lexer"
	"undalang/object"
	"undalang/parser"
)

var (
	trueObj        = &object.Boolean{Value: true}
	falseObj       = &object.Boolean{Value: false}
	nilObj         = &object.Nil{}
	breakSignal    = &object.BreakSignal{}
	continueSignal = &object.ContinueSignal{}
)

// MaxCallDepth bounds Unda-level recursion so runaway programs report an
// error instead of exhausting the host stack. The C backend uses the same limit.
const MaxCallDepth = 10000

func Eval(node ast.Node, env *object.Environment, out io.Writer) object.Object {
	ctx := NewContext("", out)
	return EvalWithContext(node, env, ctx)
}

type Context struct {
	BaseDir string
	// File is the display path of the module being evaluated; it is attached
	// to runtime errors.
	File     string
	Out      io.Writer
	loading  map[string]bool
	modules  map[string]*object.Module
	builtins map[string]*object.Builtin
	depth    int
}

func NewContext(baseDir string, out io.Writer) *Context {
	if baseDir == "" {
		if wd, err := os.Getwd(); err == nil {
			baseDir = wd
		}
	}
	return &Context{
		BaseDir:  baseDir,
		Out:      out,
		loading:  map[string]bool{},
		modules:  map[string]*object.Module{},
		builtins: builtins.New(out),
	}
}

// EvalWithContext evaluates node and stamps any runtime error created while
// evaluating it with the node's source position. Errors that already carry a
// position (raised deeper in the tree) are left untouched.
func EvalWithContext(node ast.Node, env *object.Environment, ctx *Context) object.Object {
	result := eval(node, env, ctx)
	if errObj, ok := result.(*object.Error); ok && errObj.Line == 0 {
		if line, col := ast.Position(node); line > 0 {
			errObj.File, errObj.Line, errObj.Column = ctx.File, line, col
		}
	}
	return result
}

func eval(node ast.Node, env *object.Environment, ctx *Context) object.Object {
	switch node := node.(type) {
	case *ast.Program:
		return evalProgram(node, env, ctx)
	case *ast.ExpressionStatement:
		return EvalWithContext(node.Expression, env, ctx)
	case *ast.WhileStatement:
		return evalWhileStatement(node, env, ctx)
	case *ast.ForStatement:
		return evalForStatement(node, env, ctx)
	case *ast.BreakStatement:
		return breakSignal
	case *ast.ContinueStatement:
		return continueSignal
	case *ast.BlockStatement:
		return evalBlockStatement(node, object.NewEnclosedEnvironment(env), ctx)
	case *ast.LetStatement:
		val := EvalWithContext(node.Value, env, ctx)
		if isAbrupt(val) {
			return val
		}
		env.Define(node.Name.Value, val, node.Mutable)
		return val
	case *ast.AssignStatement:
		val := EvalWithContext(node.Value, env, ctx)
		if isAbrupt(val) {
			return val
		}
		return env.Assign(node.Name.Value, val)
	case *ast.ReturnStatement:
		val := EvalWithContext(node.ReturnValue, env, ctx)
		if isAbrupt(val) {
			return val
		}
		return &object.ReturnValue{Value: val}
	case *ast.ImportStatement:
		module := evalImportStatement(node, ctx)
		if isAbrupt(module) {
			return module
		}
		alias := node.Path.Value
		if node.Alias != nil {
			alias = node.Alias.Value
		} else {
			alias = defaultModuleName(alias)
		}
		env.Define(alias, module, false)
		return module
	case *ast.IntegerLiteral:
		return &object.Integer{Value: node.Value}
	case *ast.FloatLiteral:
		return &object.Float{Value: node.Value}
	case *ast.StringLiteral:
		return &object.String{Value: node.Value}
	case *ast.Boolean:
		return nativeBool(node.Value)
	case *ast.NilLiteral:
		return nilObj
	case *ast.PrefixExpression:
		right := EvalWithContext(node.Right, env, ctx)
		if isAbrupt(right) {
			return right
		}
		return evalPrefixExpression(node.Operator, right)
	case *ast.InfixExpression:
		left := EvalWithContext(node.Left, env, ctx)
		if isAbrupt(left) {
			return left
		}
		if node.Operator == "&&" && !isTruthy(left) {
			return falseObj
		}
		if node.Operator == "||" && isTruthy(left) {
			return trueObj
		}
		right := EvalWithContext(node.Right, env, ctx)
		if isAbrupt(right) {
			return right
		}
		return evalInfixExpression(node.Operator, left, right)
	case *ast.PipelineExpression:
		left := EvalWithContext(node.Left, env, ctx)
		if isAbrupt(left) {
			return left
		}
		return evalPipelineExpression(left, node.Right, env, ctx)
	case *ast.IfExpression:
		return evalIfExpression(node, env, ctx)
	case *ast.Identifier:
		return evalIdentifier(node, env, ctx)
	case *ast.FunctionLiteral:
		return &object.Function{Parameters: node.Parameters, Body: node.Body, Env: env, Name: node.Name, File: ctx.File, BaseDir: ctx.BaseDir}
	case *ast.ArrayLiteral:
		elements := evalExpressions(node.Elements, env, ctx)
		if len(elements) == 1 && isAbrupt(elements[0]) {
			return elements[0]
		}
		return &object.Array{Elements: elements}
	case *ast.MapLiteral:
		return evalMapLiteral(node, env, ctx)
	case *ast.IndexExpression:
		left := EvalWithContext(node.Left, env, ctx)
		if isAbrupt(left) {
			return left
		}
		index := EvalWithContext(node.Index, env, ctx)
		if isAbrupt(index) {
			return index
		}
		return evalIndexExpression(left, index)
	case *ast.MemberExpression:
		obj := EvalWithContext(node.Object, env, ctx)
		if isAbrupt(obj) {
			return obj
		}
		return evalMemberExpression(obj, node.Property.Value)
	case *ast.MatchExpression:
		return evalMatchExpression(node, env, ctx)
	case *ast.CallExpression:
		fn := EvalWithContext(node.Function, env, ctx)
		if isAbrupt(fn) {
			return fn
		}
		args := evalExpressions(node.Arguments, env, ctx)
		if len(args) == 1 && isAbrupt(args[0]) {
			return args[0]
		}
		return applyFunctionWithContext(fn, args, ctx)
	}
	return nil
}

func evalProgram(program *ast.Program, env *object.Environment, ctx *Context) object.Object {
	var result object.Object = nilObj

	for _, statement := range program.Statements {
		result = EvalWithContext(statement, env, ctx)

		switch result := result.(type) {
		case *object.ReturnValue:
			return result.Value
		case *object.Error:
			return result
		case *object.BreakSignal, *object.ContinueSignal:
			return newError("%s outside loop", result.Inspect())
		}
	}

	return result
}

func evalBlockStatement(block *ast.BlockStatement, env *object.Environment, ctx *Context) object.Object {
	var result object.Object = nilObj

	for _, statement := range block.Statements {
		result = EvalWithContext(statement, env, ctx)
		if isAbrupt(result) {
			return result
		}
	}

	return result
}

// loopControl interprets a loop body's result. It reports whether the loop
// should stop and, if so, the value the loop statement evaluates to.
func loopControl(result object.Object) (stop bool, value object.Object) {
	switch result.(type) {
	case *object.BreakSignal:
		return true, nilObj
	case *object.ContinueSignal:
		return false, nil
	case *object.ReturnValue, *object.Error:
		return true, result
	}
	return false, nil
}

func evalWhileStatement(node *ast.WhileStatement, env *object.Environment, ctx *Context) object.Object {
	for {
		condition := EvalWithContext(node.Condition, env, ctx)
		if isAbrupt(condition) {
			return condition
		}
		if !isTruthy(condition) {
			return nilObj
		}
		if stop, value := loopControl(EvalWithContext(node.Body, env, ctx)); stop {
			return value
		}
	}
}

func evalForStatement(node *ast.ForStatement, env *object.Environment, ctx *Context) object.Object {
	iterable := EvalWithContext(node.Iterable, env, ctx)
	if isAbrupt(iterable) {
		return iterable
	}
	first, second, err := IterationItems(iterable, len(node.Vars))
	if err != nil {
		return err
	}
	for i := range first {
		// Each iteration gets fresh bindings so closures capture that
		// iteration's values.
		loopEnv := object.NewEnclosedEnvironment(env)
		loopEnv.Define(node.Vars[0].Value, first[i], false)
		if len(node.Vars) == 2 {
			loopEnv.Define(node.Vars[1].Value, second[i], false)
		}
		if stop, value := loopControl(EvalWithContext(node.Body, loopEnv, ctx)); stop {
			return value
		}
	}
	return nilObj
}

// IterationItems lists what a for loop binds. With one variable: array
// elements, map keys (ordered like maps.keys) or string characters. With
// two: index/key in first and the element/value in second.
func IterationItems(iterable object.Object, vars int) (first, second []object.Object, err *object.Error) {
	switch it := iterable.(type) {
	case *object.Array:
		if vars == 1 {
			return it.Elements, nil, nil
		}
		for i, el := range it.Elements {
			first = append(first, &object.Integer{Value: int64(i)})
			second = append(second, el)
		}
	case *object.Map:
		pairs := make([]object.MapPair, 0, len(it.Pairs))
		for _, pair := range it.Pairs {
			pairs = append(pairs, pair)
		}
		sort.Slice(pairs, func(i, j int) bool { return pairs[i].Key.Inspect() < pairs[j].Key.Inspect() })
		for _, pair := range pairs {
			first = append(first, pair.Key)
			second = append(second, pair.Value)
		}
		if vars == 1 {
			second = nil
		}
	case *object.String:
		i := 0
		for _, r := range it.Value {
			char := &object.String{Value: string(r)}
			if vars == 1 {
				first = append(first, char)
			} else {
				first = append(first, &object.Integer{Value: int64(i)})
				second = append(second, char)
			}
			i++
		}
	default:
		return nil, nil, newError("cannot iterate over %s", iterable.Type())
	}
	return first, second, nil
}

func evalPrefixExpression(operator string, right object.Object) object.Object {
	switch operator {
	case "!":
		return evalBangOperatorExpression(right)
	case "-":
		return evalMinusPrefixOperatorExpression(right)
	default:
		return newError("unknown operator: %s%s", operator, right.Type())
	}
}

func evalBangOperatorExpression(right object.Object) object.Object {
	return nativeBool(!isTruthy(right))
}

func evalMinusPrefixOperatorExpression(right object.Object) object.Object {
	switch right := right.(type) {
	case *object.Integer:
		return &object.Integer{Value: -right.Value}
	case *object.Float:
		return &object.Float{Value: -right.Value}
	default:
		return newError("unknown operator: -%s", right.Type())
	}
}

func evalInfixExpression(operator string, left, right object.Object) object.Object {
	switch {
	case isNumber(left) && isNumber(right):
		return evalNumberInfixExpression(operator, left, right)
	case operator == "==":
		return nativeBool(objectsEqual(left, right))
	case operator == "!=":
		return nativeBool(!objectsEqual(left, right))
	case left.Type() == object.StringObj && right.Type() == object.StringObj:
		return evalStringInfixExpression(operator, left.(*object.String), right.(*object.String))
	case operator == "&&" || operator == "||":
		return nativeBool(evalLogical(operator, left, right))
	case left.Type() != right.Type():
		return newError("type mismatch: %s %s %s", left.Type(), operator, right.Type())
	default:
		return newError("unknown operator: %s %s %s", left.Type(), operator, right.Type())
	}
}

func evalNumberInfixExpression(operator string, left, right object.Object) object.Object {
	if li, ok := left.(*object.Integer); ok {
		if ri, ok := right.(*object.Integer); ok {
			switch operator {
			case "+":
				return &object.Integer{Value: li.Value + ri.Value}
			case "-":
				return &object.Integer{Value: li.Value - ri.Value}
			case "*":
				return &object.Integer{Value: li.Value * ri.Value}
			case "/":
				if ri.Value == 0 {
					return newError("division by zero")
				}
				return &object.Integer{Value: li.Value / ri.Value}
			case "%":
				if ri.Value == 0 {
					return newError("modulo by zero")
				}
				return &object.Integer{Value: li.Value % ri.Value}
			case "<":
				return nativeBool(li.Value < ri.Value)
			case ">":
				return nativeBool(li.Value > ri.Value)
			case "<=":
				return nativeBool(li.Value <= ri.Value)
			case ">=":
				return nativeBool(li.Value >= ri.Value)
			case "==":
				return nativeBool(li.Value == ri.Value)
			case "!=":
				return nativeBool(li.Value != ri.Value)
			}
		}
	}

	if operator == "%" {
		return newError("operator %% only supports integers")
	}

	lv := numberValue(left)
	rv := numberValue(right)
	switch operator {
	case "+":
		return &object.Float{Value: lv + rv}
	case "-":
		return &object.Float{Value: lv - rv}
	case "*":
		return &object.Float{Value: lv * rv}
	case "/":
		if rv == 0 {
			return newError("division by zero")
		}
		return &object.Float{Value: lv / rv}
	case "<":
		return nativeBool(lv < rv)
	case ">":
		return nativeBool(lv > rv)
	case "<=":
		return nativeBool(lv <= rv)
	case ">=":
		return nativeBool(lv >= rv)
	case "==":
		return nativeBool(math.Abs(lv-rv) == 0)
	case "!=":
		return nativeBool(math.Abs(lv-rv) != 0)
	default:
		return newError("unknown operator: %s %s %s", left.Type(), operator, right.Type())
	}
}

func evalStringInfixExpression(operator string, left, right *object.String) object.Object {
	switch operator {
	case "+":
		return &object.String{Value: left.Value + right.Value}
	case "==":
		return nativeBool(left.Value == right.Value)
	case "!=":
		return nativeBool(left.Value != right.Value)
	default:
		return newError("unknown operator: STRING %s STRING", operator)
	}
}

func evalLogical(operator string, left, right object.Object) bool {
	if operator == "&&" {
		return isTruthy(left) && isTruthy(right)
	}
	return isTruthy(left) || isTruthy(right)
}

func evalIfExpression(ie *ast.IfExpression, env *object.Environment, ctx *Context) object.Object {
	condition := EvalWithContext(ie.Condition, env, ctx)
	if isAbrupt(condition) {
		return condition
	}

	if isTruthy(condition) {
		return EvalWithContext(ie.Consequence, env, ctx)
	} else if ie.Alternative != nil {
		return EvalWithContext(ie.Alternative, env, ctx)
	}
	return nilObj
}

func evalIdentifier(node *ast.Identifier, env *object.Environment, ctx *Context) object.Object {
	if val, ok := env.Get(node.Value); ok {
		return val
	}

	if builtin, ok := ctx.builtins[node.Value]; ok {
		return builtin
	}

	return newError("identifier not found: %s", node.Value)
}

func evalMapLiteral(node *ast.MapLiteral, env *object.Environment, ctx *Context) object.Object {
	pairs := make(map[object.HashKey]object.MapPair)
	for _, nodePair := range node.Pairs {
		key := EvalWithContext(nodePair.Key, env, ctx)
		if isAbrupt(key) {
			return key
		}
		hashKey, ok := key.(object.Hashable)
		if !ok {
			return newError("unusable as map key: %s", key.Type())
		}
		value := EvalWithContext(nodePair.Value, env, ctx)
		if isAbrupt(value) {
			return value
		}
		pairs[hashKey.HashKey()] = object.MapPair{Key: key, Value: value}
	}
	return &object.Map{Pairs: pairs}
}

func evalIndexExpression(left, index object.Object) object.Object {
	switch {
	case left.Type() == object.ArrayObj && index.Type() == object.IntegerObj:
		array := left.(*object.Array)
		idx := index.(*object.Integer).Value
		if idx < 0 || idx >= int64(len(array.Elements)) {
			return nilObj
		}
		return array.Elements[int(idx)]
	case left.Type() == object.MapObj:
		key, ok := index.(object.Hashable)
		if !ok {
			return newError("unusable as map key: %s", index.Type())
		}
		pair, ok := left.(*object.Map).Pairs[key.HashKey()]
		if !ok {
			return nilObj
		}
		return pair.Value
	case left.Type() == object.StringObj && index.Type() == object.IntegerObj:
		// Strings index by character (code point), not byte.
		idx := index.(*object.Integer).Value
		if idx < 0 {
			return nilObj
		}
		i := int64(0)
		for _, r := range left.(*object.String).Value {
			if i == idx {
				return &object.String{Value: string(r)}
			}
			i++
		}
		return nilObj
	default:
		return newError("index operator not supported: %s[%s]", left.Type(), index.Type())
	}
}

func evalMemberExpression(obj object.Object, property string) object.Object {
	module, ok := obj.(*object.Module)
	if !ok {
		return newError("member access expects module, got %s", obj.Type())
	}
	value, ok := module.Members[property]
	if !ok {
		return newError("module %q has no member %q", module.Name, property)
	}
	return value
}

func evalMatchExpression(node *ast.MatchExpression, env *object.Environment, ctx *Context) object.Object {
	value := EvalWithContext(node.Value, env, ctx)
	if isAbrupt(value) {
		return value
	}
	for _, arm := range node.Arms {
		// Bindings live in a fresh scope per arm so a failed arm leaks nothing.
		armEnv := object.NewEnclosedEnvironment(env)
		ok, abrupt := matchPattern(arm.Pattern, value, armEnv, ctx)
		if abrupt != nil {
			return abrupt
		}
		if !ok {
			continue
		}
		if arm.Guard != nil {
			guard := EvalWithContext(arm.Guard, armEnv, ctx)
			if isAbrupt(guard) {
				return guard
			}
			if !isTruthy(guard) {
				continue
			}
		}
		return EvalWithContext(arm.Value, armEnv, ctx)
	}
	return nilObj
}

// matchPattern reports whether value matches pattern, defining bindings in
// env. A non-nil second result is an error or signal to propagate.
func matchPattern(pattern ast.Pattern, value object.Object, env *object.Environment, ctx *Context) (bool, object.Object) {
	switch p := pattern.(type) {
	case *ast.WildcardPattern:
		return true, nil
	case *ast.BindingPattern:
		env.Define(p.Name.Value, value, false)
		return true, nil
	case *ast.ValuePattern:
		expected := EvalWithContext(p.Value, env, ctx)
		if isAbrupt(expected) {
			return false, expected
		}
		return objectsEqual(value, expected), nil
	case *ast.ArrayPattern:
		arr, ok := value.(*object.Array)
		if !ok || len(arr.Elements) < len(p.Elements) || (!p.HasRest && len(arr.Elements) != len(p.Elements)) {
			return false, nil
		}
		for i, el := range p.Elements {
			if ok, abrupt := matchPattern(el, arr.Elements[i], env, ctx); !ok || abrupt != nil {
				return false, abrupt
			}
		}
		if p.Rest != nil {
			rest := append([]object.Object{}, arr.Elements[len(p.Elements):]...)
			env.Define(p.Rest.Value, &object.Array{Elements: rest}, false)
		}
		return true, nil
	case *ast.MapPattern:
		m, ok := value.(*object.Map)
		if !ok {
			return false, nil
		}
		for i, keyExpr := range p.Keys {
			key := EvalWithContext(keyExpr, env, ctx)
			if isAbrupt(key) {
				return false, key
			}
			hashable, ok := key.(object.Hashable)
			if !ok {
				err := newError("unusable as map key: %s", key.Type())
				err.File, err.Line, err.Column = ctx.File, p.Token.Line, p.Token.Column
				return false, err
			}
			pair, ok := m.Pairs[hashable.HashKey()]
			if !ok {
				return false, nil
			}
			if ok, abrupt := matchPattern(p.Values[i], pair.Value, env, ctx); !ok || abrupt != nil {
				return false, abrupt
			}
		}
		return true, nil
	}
	return false, newError("unsupported pattern %T", pattern)
}

func evalPipelineExpression(left object.Object, right ast.Expression, env *object.Environment, ctx *Context) object.Object {
	switch right := right.(type) {
	case *ast.CallExpression:
		fn := EvalWithContext(right.Function, env, ctx)
		if isAbrupt(fn) {
			return fn
		}
		args := []object.Object{left}
		more := evalExpressions(right.Arguments, env, ctx)
		if len(more) == 1 && isAbrupt(more[0]) {
			return more[0]
		}
		args = append(args, more...)
		return applyFunctionWithContext(fn, args, ctx)
	default:
		fn := EvalWithContext(right, env, ctx)
		if isAbrupt(fn) {
			return fn
		}
		return applyFunctionWithContext(fn, []object.Object{left}, ctx)
	}
}

func evalImportStatement(node *ast.ImportStatement, ctx *Context) object.Object {
	path := node.Path.Value
	if module, ok := builtins.StdModules(ctx.apply)[path]; ok {
		return module
	}

	fullPath := path
	if !strings.HasSuffix(fullPath, ".unda") {
		fullPath += ".unda"
	}
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join(ctx.BaseDir, fullPath)
	}
	cleanPath, err := filepath.Abs(fullPath)
	if err != nil {
		return newError("import %q failed: %v", path, err)
	}
	if module, ok := ctx.modules[cleanPath]; ok {
		return module
	}
	if ctx.loading[cleanPath] {
		return newError("import cycle detected at %q", path)
	}
	input, err := os.ReadFile(cleanPath)
	if err != nil {
		return newError("import %q failed: %v", path, err)
	}

	l := lexer.New(string(input))
	p := parser.New(l)
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		return newError("import %q parser errors: %s", path, strings.Join(p.Errors(), "; "))
	}

	ctx.loading[cleanPath] = true
	previousBase, previousFile := ctx.BaseDir, ctx.File
	ctx.BaseDir, ctx.File = filepath.Dir(cleanPath), diag.DisplayPath(cleanPath)
	moduleEnv := object.NewEnvironment()
	result := EvalWithContext(program, moduleEnv, ctx)
	ctx.BaseDir, ctx.File = previousBase, previousFile
	delete(ctx.loading, cleanPath)
	if isAbrupt(result) {
		return result
	}

	module := &object.Module{Name: defaultModuleName(path), Members: moduleEnv.ExportedBindings()}
	ctx.modules[cleanPath] = module
	return module
}

func evalExpressions(exps []ast.Expression, env *object.Environment, ctx *Context) []object.Object {
	result := []object.Object{}

	for _, e := range exps {
		evaluated := EvalWithContext(e, env, ctx)
		if isAbrupt(evaluated) {
			return []object.Object{evaluated}
		}
		result = append(result, evaluated)
	}

	return result
}

func (ctx *Context) apply(fn object.Object, args []object.Object) object.Object {
	return applyFunctionWithContext(fn, args, ctx)
}

func applyFunctionWithContext(fn object.Object, args []object.Object, ctx *Context) object.Object {
	switch fn := fn.(type) {
	case *object.Function:
		if len(args) != len(fn.Parameters) {
			return newError("wrong number of arguments: expected %d, got %d", len(fn.Parameters), len(args))
		}
		if ctx.depth >= MaxCallDepth {
			return newError("maximum call depth exceeded")
		}
		extendedEnv := object.NewEnclosedEnvironment(fn.Env)
		for paramIdx, param := range fn.Parameters {
			extendedEnv.Define(param.Value, args[paramIdx], false)
		}
		previousBase, previousFile := ctx.BaseDir, ctx.File
		if fn.File != "" || fn.BaseDir != "" {
			ctx.BaseDir, ctx.File = fn.BaseDir, fn.File
		}
		ctx.depth++
		evaluated := EvalWithContext(fn.Body, extendedEnv, ctx)
		ctx.depth--
		ctx.BaseDir, ctx.File = previousBase, previousFile
		switch result := evaluated.(type) {
		case *object.ReturnValue:
			return result.Value
		case *object.BreakSignal, *object.ContinueSignal:
			return newError("%s outside loop", result.Inspect())
		}
		return evaluated
	case *object.Builtin:
		return fn.Fn(args...)
	default:
		return newError("not a function: %s", fn.Type())
	}
}

func nativeBool(input bool) *object.Boolean {
	if input {
		return trueObj
	}
	return falseObj
}

// isTruthy compares by value: builtins return fresh Boolean and Nil objects,
// so identity checks against the shared singletons are not enough.
func isTruthy(obj object.Object) bool {
	switch obj := obj.(type) {
	case *object.Nil:
		return false
	case *object.Boolean:
		return obj.Value
	default:
		return true
	}
}

func isNumber(obj object.Object) bool {
	return obj.Type() == object.IntegerObj || obj.Type() == object.FloatObj
}

func numberValue(obj object.Object) float64 {
	switch obj := obj.(type) {
	case *object.Integer:
		return float64(obj.Value)
	case *object.Float:
		return obj.Value
	default:
		return 0
	}
}

func objectsEqual(left, right object.Object) bool {
	switch left := left.(type) {
	case *object.Boolean:
		right, ok := right.(*object.Boolean)
		return ok && left.Value == right.Value
	case *object.Nil:
		_, ok := right.(*object.Nil)
		return ok
	case *object.String:
		right, ok := right.(*object.String)
		return ok && left.Value == right.Value
	case *object.Integer:
		right, ok := right.(*object.Integer)
		return ok && left.Value == right.Value
	case *object.Float:
		right, ok := right.(*object.Float)
		return ok && left.Value == right.Value
	case *object.Array:
		right, ok := right.(*object.Array)
		if !ok || len(left.Elements) != len(right.Elements) {
			return false
		}
		for i := range left.Elements {
			if !objectsEqual(left.Elements[i], right.Elements[i]) {
				return false
			}
		}
		return true
	case *object.Map:
		right, ok := right.(*object.Map)
		if !ok || len(left.Pairs) != len(right.Pairs) {
			return false
		}
		for key, leftPair := range left.Pairs {
			rightPair, ok := right.Pairs[key]
			if !ok || !objectsEqual(leftPair.Value, rightPair.Value) {
				return false
			}
		}
		return true
	default:
		return left == right
	}
}

func defaultModuleName(path string) string {
	path = strings.TrimSuffix(path, ".unda")
	path = filepath.Base(path)
	path = strings.Trim(path, `"`)
	if path == "" || path == "." {
		return "module"
	}
	return path
}

func newError(format string, a ...interface{}) *object.Error {
	return &object.Error{Message: fmt.Sprintf(format, a...)}
}

func isError(obj object.Object) bool {
	if obj != nil {
		return obj.Type() == object.ErrorObj
	}
	return false
}

// isAbrupt reports values that end evaluation early: errors and the
// return/break/continue signals. They propagate through every expression.
func isAbrupt(obj object.Object) bool {
	switch obj.(type) {
	case *object.Error, *object.ReturnValue, *object.BreakSignal, *object.ContinueSignal:
		return true
	}
	return false
}
