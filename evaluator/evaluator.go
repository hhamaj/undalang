package evaluator

import (
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"undalang/ast"
	"undalang/builtins"
	"undalang/lexer"
	"undalang/object"
	"undalang/parser"
)

var (
	trueObj  = &object.Boolean{Value: true}
	falseObj = &object.Boolean{Value: false}
	nilObj   = &object.Nil{}
)

func init() {
	builtins.ApplyFunction = applyFunction
}

func Eval(node ast.Node, env *object.Environment, out io.Writer) object.Object {
	ctx := NewContext("", out)
	return EvalWithContext(node, env, ctx)
}

type Context struct {
	BaseDir string
	Out     io.Writer
	loading map[string]bool
	modules map[string]*object.Module
}

func NewContext(baseDir string, out io.Writer) *Context {
	if baseDir == "" {
		if wd, err := os.Getwd(); err == nil {
			baseDir = wd
		}
	}
	return &Context{BaseDir: baseDir, Out: out, loading: map[string]bool{}, modules: map[string]*object.Module{}}
}

func EvalWithContext(node ast.Node, env *object.Environment, ctx *Context) object.Object {
	out := ctx.Out
	switch node := node.(type) {
	case *ast.Program:
		return evalProgram(node, env, ctx)
	case *ast.ExpressionStatement:
		return EvalWithContext(node.Expression, env, ctx)
	case *ast.BlockStatement:
		return evalBlockStatement(node, object.NewEnclosedEnvironment(env), ctx)
	case *ast.LetStatement:
		val := EvalWithContext(node.Value, env, ctx)
		if isError(val) {
			return val
		}
		env.Define(node.Name.Value, val, node.Mutable)
		return val
	case *ast.AssignStatement:
		val := EvalWithContext(node.Value, env, ctx)
		if isError(val) {
			return val
		}
		return env.Assign(node.Name.Value, val)
	case *ast.ReturnStatement:
		val := EvalWithContext(node.ReturnValue, env, ctx)
		if isError(val) {
			return val
		}
		return &object.ReturnValue{Value: val}
	case *ast.ImportStatement:
		module := evalImportStatement(node, ctx)
		if isError(module) {
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
		if isError(right) {
			return right
		}
		return evalPrefixExpression(node.Operator, right)
	case *ast.InfixExpression:
		left := EvalWithContext(node.Left, env, ctx)
		if isError(left) {
			return left
		}
		if node.Operator == "&&" && !isTruthy(left) {
			return falseObj
		}
		if node.Operator == "||" && isTruthy(left) {
			return trueObj
		}
		right := EvalWithContext(node.Right, env, ctx)
		if isError(right) {
			return right
		}
		return evalInfixExpression(node.Operator, left, right)
	case *ast.PipelineExpression:
		left := EvalWithContext(node.Left, env, ctx)
		if isError(left) {
			return left
		}
		return evalPipelineExpression(left, node.Right, env, ctx)
	case *ast.IfExpression:
		return evalIfExpression(node, env, ctx)
	case *ast.Identifier:
		return evalIdentifier(node, env, out)
	case *ast.FunctionLiteral:
		return &object.Function{Parameters: node.Parameters, Body: node.Body, Env: env, Name: node.Name}
	case *ast.ArrayLiteral:
		elements := evalExpressions(node.Elements, env, ctx)
		if len(elements) == 1 && isError(elements[0]) {
			return elements[0]
		}
		return &object.Array{Elements: elements}
	case *ast.MapLiteral:
		return evalMapLiteral(node, env, ctx)
	case *ast.IndexExpression:
		left := EvalWithContext(node.Left, env, ctx)
		if isError(left) {
			return left
		}
		index := EvalWithContext(node.Index, env, ctx)
		if isError(index) {
			return index
		}
		return evalIndexExpression(left, index)
	case *ast.MemberExpression:
		obj := EvalWithContext(node.Object, env, ctx)
		if isError(obj) {
			return obj
		}
		return evalMemberExpression(obj, node.Property.Value)
	case *ast.MatchExpression:
		return evalMatchExpression(node, env, ctx)
	case *ast.CallExpression:
		fn := EvalWithContext(node.Function, env, ctx)
		if isError(fn) {
			return fn
		}
		args := evalExpressions(node.Arguments, env, ctx)
		if len(args) == 1 && isError(args[0]) {
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
		}
	}

	return result
}

func evalBlockStatement(block *ast.BlockStatement, env *object.Environment, ctx *Context) object.Object {
	var result object.Object = nilObj

	for _, statement := range block.Statements {
		result = EvalWithContext(statement, env, ctx)
		if result != nil {
			rt := result.Type()
			if rt == object.ReturnValueObj || rt == object.ErrorObj {
				return result
			}
		}
	}

	return result
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
	switch right {
	case trueObj:
		return falseObj
	case falseObj:
		return trueObj
	case nilObj:
		return trueObj
	default:
		return nativeBool(!isTruthy(right))
	}
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
	if isError(condition) {
		return condition
	}

	if isTruthy(condition) {
		return EvalWithContext(ie.Consequence, env, ctx)
	} else if ie.Alternative != nil {
		return EvalWithContext(ie.Alternative, env, ctx)
	}
	return nilObj
}

func evalIdentifier(node *ast.Identifier, env *object.Environment, out io.Writer) object.Object {
	if val, ok := env.Get(node.Value); ok {
		return val
	}

	if builtin, ok := builtins.New(out)[node.Value]; ok {
		return builtin
	}

	return newError("identifier not found: %s", node.Value)
}

func evalMapLiteral(node *ast.MapLiteral, env *object.Environment, ctx *Context) object.Object {
	pairs := make(map[object.HashKey]object.MapPair)
	for _, nodePair := range node.Pairs {
		key := EvalWithContext(nodePair.Key, env, ctx)
		if isError(key) {
			return key
		}
		hashKey, ok := key.(object.Hashable)
		if !ok {
			return newError("unusable as map key: %s", key.Type())
		}
		value := EvalWithContext(nodePair.Value, env, ctx)
		if isError(value) {
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
		s := left.(*object.String).Value
		idx := index.(*object.Integer).Value
		if idx < 0 || idx >= int64(len(s)) {
			return nilObj
		}
		return &object.String{Value: string(s[idx])}
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
	if isError(value) {
		return value
	}
	for _, arm := range node.Arms {
		if arm.Wildcard {
			return EvalWithContext(arm.Value, env, ctx)
		}
		pattern := EvalWithContext(arm.Pattern, env, ctx)
		if isError(pattern) {
			return pattern
		}
		if objectsEqual(value, pattern) {
			return EvalWithContext(arm.Value, env, ctx)
		}
	}
	return nilObj
}

func evalPipelineExpression(left object.Object, right ast.Expression, env *object.Environment, ctx *Context) object.Object {
	switch right := right.(type) {
	case *ast.CallExpression:
		fn := EvalWithContext(right.Function, env, ctx)
		if isError(fn) {
			return fn
		}
		args := []object.Object{left}
		more := evalExpressions(right.Arguments, env, ctx)
		if len(more) == 1 && isError(more[0]) {
			return more[0]
		}
		args = append(args, more...)
		return applyFunctionWithContext(fn, args, ctx)
	default:
		fn := EvalWithContext(right, env, ctx)
		if isError(fn) {
			return fn
		}
		return applyFunctionWithContext(fn, []object.Object{left}, ctx)
	}
}

func evalImportStatement(node *ast.ImportStatement, ctx *Context) object.Object {
	path := node.Path.Value
	if module, ok := builtins.StdModules(ctx.Out)[path]; ok {
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
	previousBase := ctx.BaseDir
	ctx.BaseDir = filepath.Dir(cleanPath)
	moduleEnv := object.NewEnvironment()
	result := EvalWithContext(program, moduleEnv, ctx)
	ctx.BaseDir = previousBase
	delete(ctx.loading, cleanPath)
	if isError(result) {
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
		if isError(evaluated) {
			return []object.Object{evaluated}
		}
		result = append(result, evaluated)
	}

	return result
}

func applyFunction(fn object.Object, args []object.Object, out io.Writer) object.Object {
	return applyFunctionWithContext(fn, args, NewContext("", out))
}

func applyFunctionWithContext(fn object.Object, args []object.Object, ctx *Context) object.Object {
	switch fn := fn.(type) {
	case *object.Function:
		if len(args) != len(fn.Parameters) {
			return newError("wrong number of arguments: expected %d, got %d", len(fn.Parameters), len(args))
		}
		extendedEnv := object.NewEnclosedEnvironment(fn.Env)
		for paramIdx, param := range fn.Parameters {
			extendedEnv.Define(param.Value, args[paramIdx], false)
		}
		evaluated := EvalWithContext(fn.Body, extendedEnv, ctx)
		if returnValue, ok := evaluated.(*object.ReturnValue); ok {
			return returnValue.Value
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

func isTruthy(obj object.Object) bool {
	switch obj {
	case nilObj:
		return false
	case trueObj:
		return true
	case falseObj:
		return false
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
