package evaluator

import (
	"fmt"
	"io"
	"math"

	"ranalang/ast"
	"ranalang/builtins"
	"ranalang/object"
)

var (
	trueObj  = &object.Boolean{Value: true}
	falseObj = &object.Boolean{Value: false}
	nilObj   = &object.Nil{}
)

func Eval(node ast.Node, env *object.Environment, out io.Writer) object.Object {
	switch node := node.(type) {
	case *ast.Program:
		return evalProgram(node, env, out)
	case *ast.ExpressionStatement:
		return Eval(node.Expression, env, out)
	case *ast.BlockStatement:
		return evalBlockStatement(node, object.NewEnclosedEnvironment(env), out)
	case *ast.LetStatement:
		val := Eval(node.Value, env, out)
		if isError(val) {
			return val
		}
		env.Define(node.Name.Value, val, node.Mutable)
		return val
	case *ast.AssignStatement:
		val := Eval(node.Value, env, out)
		if isError(val) {
			return val
		}
		return env.Assign(node.Name.Value, val)
	case *ast.ReturnStatement:
		val := Eval(node.ReturnValue, env, out)
		if isError(val) {
			return val
		}
		return &object.ReturnValue{Value: val}
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
		right := Eval(node.Right, env, out)
		if isError(right) {
			return right
		}
		return evalPrefixExpression(node.Operator, right)
	case *ast.InfixExpression:
		left := Eval(node.Left, env, out)
		if isError(left) {
			return left
		}
		if node.Operator == "&&" && !isTruthy(left) {
			return falseObj
		}
		if node.Operator == "||" && isTruthy(left) {
			return trueObj
		}
		right := Eval(node.Right, env, out)
		if isError(right) {
			return right
		}
		return evalInfixExpression(node.Operator, left, right)
	case *ast.IfExpression:
		return evalIfExpression(node, env, out)
	case *ast.Identifier:
		return evalIdentifier(node, env, out)
	case *ast.FunctionLiteral:
		return &object.Function{Parameters: node.Parameters, Body: node.Body, Env: env, Name: node.Name}
	case *ast.CallExpression:
		fn := Eval(node.Function, env, out)
		if isError(fn) {
			return fn
		}
		args := evalExpressions(node.Arguments, env, out)
		if len(args) == 1 && isError(args[0]) {
			return args[0]
		}
		return applyFunction(fn, args, out)
	}
	return nil
}

func evalProgram(program *ast.Program, env *object.Environment, out io.Writer) object.Object {
	var result object.Object = nilObj

	for _, statement := range program.Statements {
		result = Eval(statement, env, out)

		switch result := result.(type) {
		case *object.ReturnValue:
			return result.Value
		case *object.Error:
			return result
		}
	}

	return result
}

func evalBlockStatement(block *ast.BlockStatement, env *object.Environment, out io.Writer) object.Object {
	var result object.Object = nilObj

	for _, statement := range block.Statements {
		result = Eval(statement, env, out)
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

func evalIfExpression(ie *ast.IfExpression, env *object.Environment, out io.Writer) object.Object {
	condition := Eval(ie.Condition, env, out)
	if isError(condition) {
		return condition
	}

	if isTruthy(condition) {
		return Eval(ie.Consequence, env, out)
	} else if ie.Alternative != nil {
		return Eval(ie.Alternative, env, out)
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

func evalExpressions(exps []ast.Expression, env *object.Environment, out io.Writer) []object.Object {
	result := []object.Object{}

	for _, e := range exps {
		evaluated := Eval(e, env, out)
		if isError(evaluated) {
			return []object.Object{evaluated}
		}
		result = append(result, evaluated)
	}

	return result
}

func applyFunction(fn object.Object, args []object.Object, out io.Writer) object.Object {
	switch fn := fn.(type) {
	case *object.Function:
		if len(args) != len(fn.Parameters) {
			return newError("wrong number of arguments: expected %d, got %d", len(fn.Parameters), len(args))
		}
		extendedEnv := object.NewEnclosedEnvironment(fn.Env)
		for paramIdx, param := range fn.Parameters {
			extendedEnv.Define(param.Value, args[paramIdx], false)
		}
		evaluated := Eval(fn.Body, extendedEnv, out)
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
	default:
		return left == right
	}
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
