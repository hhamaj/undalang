package semantic

import "undalang/ast"

// containsFunction reports whether a function literal shows up anywhere in n
func containsFunction(n ast.Node) bool {
	found := false
	walk(n, func(n ast.Node) bool {
		if _, ok := n.(*ast.FunctionLiteral); ok {
			found = true
		}
		return !found
	})
	return found
}

// walk visits n and its children in source order until visit returns false
func walk(n ast.Node, visit func(ast.Node) bool) bool {
	if n == nil || !visit(n) {
		return false
	}
	children := []ast.Node{}
	addExpr := func(e ast.Expression) {
		if e != nil {
			children = append(children, e)
		}
	}
	switch n := n.(type) {
	case *ast.Program:
		for _, s := range n.Statements {
			children = append(children, s)
		}
	case *ast.BlockStatement:
		for _, s := range n.Statements {
			children = append(children, s)
		}
	case *ast.LetStatement:
		addExpr(n.Value)
	case *ast.AssignStatement:
		addExpr(n.Value)
	case *ast.ReturnStatement:
		addExpr(n.ReturnValue)
	case *ast.ExpressionStatement:
		addExpr(n.Expression)
	case *ast.WhileStatement:
		addExpr(n.Condition)
		children = append(children, n.Body)
	case *ast.ForStatement:
		addExpr(n.Iterable)
		children = append(children, n.Body)
	case *ast.PrefixExpression:
		addExpr(n.Right)
	case *ast.InfixExpression:
		addExpr(n.Left)
		addExpr(n.Right)
	case *ast.PipelineExpression:
		addExpr(n.Left)
		addExpr(n.Right)
	case *ast.IfExpression:
		addExpr(n.Condition)
		children = append(children, n.Consequence)
		if n.Alternative != nil {
			children = append(children, n.Alternative)
		}
	case *ast.FunctionLiteral:
		children = append(children, n.Body)
	case *ast.ArrayLiteral:
		for _, e := range n.Elements {
			addExpr(e)
		}
	case *ast.MapLiteral:
		for _, p := range n.Pairs {
			addExpr(p.Key)
			addExpr(p.Value)
		}
	case *ast.IndexExpression:
		addExpr(n.Left)
		addExpr(n.Index)
	case *ast.MemberExpression:
		addExpr(n.Object)
	case *ast.TryExpression:
		addExpr(n.Value)
	case *ast.ResultPattern:
		children = append(children, n.Inner)
	case *ast.CallExpression:
		addExpr(n.Function)
		for _, a := range n.Arguments {
			addExpr(a)
		}
	case *ast.MatchExpression:
		addExpr(n.Value)
		for _, arm := range n.Arms {
			children = append(children, arm.Pattern)
			addExpr(arm.Guard)
			addExpr(arm.Value)
		}
	case *ast.ValuePattern:
		addExpr(n.Value)
	case *ast.ArrayPattern:
		for _, el := range n.Elements {
			children = append(children, el)
		}
	case *ast.MapPattern:
		for i := range n.Keys {
			addExpr(n.Keys[i])
			children = append(children, n.Values[i])
		}
	}
	for _, c := range children {
		if !walk(c, visit) {
			return false
		}
	}
	return true
}
