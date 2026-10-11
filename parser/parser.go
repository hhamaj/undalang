package parser

import (
	"fmt"
	"strconv"

	"undalang/ast"
	"undalang/lexer"
	"undalang/token"
)

type (
	prefixParseFn func() ast.Expression
	infixParseFn  func(ast.Expression) ast.Expression
)

const (
	_ int = iota
	lowest
	pipePrec
	orPrec
	andPrec
	equals
	lessGreater
	sum
	product
	prefix
	call
	index
)

var precedences = map[token.Type]int{
	token.Or:       orPrec,
	token.And:      andPrec,
	token.Pipe:     pipePrec,
	token.Eq:       equals,
	token.NotEq:    equals,
	token.LT:       lessGreater,
	token.GT:       lessGreater,
	token.LTE:      lessGreater,
	token.GTE:      lessGreater,
	token.Plus:     sum,
	token.Minus:    sum,
	token.Slash:    product,
	token.Asterisk: product,
	token.Percent:  product,
	token.LParen:   call,
	token.LBracket: index,
	token.Dot:      index,
}

type Parser struct {
	l *lexer.Lexer

	errors []string

	curToken  token.Token
	peekToken token.Token

	prefixParseFns map[token.Type]prefixParseFn
	infixParseFns  map[token.Type]infixParseFn
}

func New(l *lexer.Lexer) *Parser {
	p := &Parser{l: l, errors: []string{}}

	p.prefixParseFns = make(map[token.Type]prefixParseFn)
	p.registerPrefix(token.Ident, p.parseIdentifier)
	p.registerPrefix(token.Int, p.parseIntegerLiteral)
	p.registerPrefix(token.Float, p.parseFloatLiteral)
	p.registerPrefix(token.String, p.parseStringLiteral)
	p.registerPrefix(token.True, p.parseBoolean)
	p.registerPrefix(token.False, p.parseBoolean)
	p.registerPrefix(token.Nil, p.parseNil)
	p.registerPrefix(token.Bang, p.parsePrefixExpression)
	p.registerPrefix(token.Minus, p.parsePrefixExpression)
	p.registerPrefix(token.LParen, p.parseGroupedExpression)
	p.registerPrefix(token.LBracket, p.parseArrayLiteral)
	p.registerPrefix(token.LBrace, p.parseMapLiteral)
	p.registerPrefix(token.If, p.parseIfExpression)
	p.registerPrefix(token.Function, p.parseFunctionLiteral)
	p.registerPrefix(token.Match, p.parseMatchExpression)

	p.infixParseFns = make(map[token.Type]infixParseFn)
	for _, t := range []token.Type{
		token.Plus, token.Minus, token.Slash, token.Asterisk, token.Percent,
		token.Eq, token.NotEq, token.LT, token.GT, token.LTE, token.GTE,
		token.And, token.Or,
	} {
		p.registerInfix(t, p.parseInfixExpression)
	}
	p.registerInfix(token.LParen, p.parseCallExpression)
	p.registerInfix(token.LBracket, p.parseIndexExpression)
	p.registerInfix(token.Dot, p.parseMemberExpression)
	p.registerInfix(token.Pipe, p.parsePipelineExpression)

	p.nextToken()
	p.nextToken()

	return p
}

func (p *Parser) Errors() []string {
	return p.errors
}

func (p *Parser) ParseProgram() *ast.Program {
	program := &ast.Program{}
	program.Statements = []ast.Statement{}

	for !p.curTokenIs(token.EOF) {
		stmt := p.parseStatement()
		if stmt != nil {
			program.Statements = append(program.Statements, stmt)
		}
		p.nextToken()
	}

	return program
}

func (p *Parser) parseStatement() ast.Statement {
	switch {
	case p.curTokenIs(token.Let):
		return p.parseLetStatement()
	case p.curTokenIs(token.Function) && p.peekTokenIs(token.Ident):
		return p.parseNamedFunctionStatement()
	case p.curTokenIs(token.Return):
		return p.parseReturnStatement()
	case p.curTokenIs(token.Import):
		return p.parseImportStatement()
	case p.curTokenIs(token.While):
		return p.parseWhileStatement()
	case p.curTokenIs(token.For):
		return p.parseForStatement()
	case p.curTokenIs(token.Break):
		stmt := &ast.BreakStatement{Token: p.curToken}
		p.skipSemicolon()
		return stmt
	case p.curTokenIs(token.Continue):
		stmt := &ast.ContinueStatement{Token: p.curToken}
		p.skipSemicolon()
		return stmt
	case p.curTokenIs(token.Ident) && p.peekTokenIs(token.Assign):
		return p.parseAssignStatement()
	default:
		return p.parseExpressionStatement()
	}
}

func (p *Parser) skipSemicolon() {
	if p.peekTokenIs(token.Semicolon) {
		p.nextToken()
	}
}

func (p *Parser) parseWhileStatement() ast.Statement {
	stmt := &ast.WhileStatement{Token: p.curToken}
	p.nextToken()
	stmt.Condition = p.parseExpression(lowest)
	if stmt.Condition == nil || !p.expectPeek(token.LBrace) {
		return nil
	}
	stmt.Body = p.parseBlockStatement()
	return stmt
}

func (p *Parser) parseForStatement() ast.Statement {
	stmt := &ast.ForStatement{Token: p.curToken}
	if !p.expectPeek(token.Ident) {
		return nil
	}
	stmt.Vars = append(stmt.Vars, &ast.Identifier{Token: p.curToken, Value: p.curToken.Literal})
	if p.peekTokenIs(token.Comma) {
		p.nextToken()
		if !p.expectPeek(token.Ident) {
			return nil
		}
		stmt.Vars = append(stmt.Vars, &ast.Identifier{Token: p.curToken, Value: p.curToken.Literal})
	}
	if !p.expectPeek(token.In) {
		return nil
	}
	p.nextToken()
	stmt.Iterable = p.parseExpression(lowest)
	if stmt.Iterable == nil || !p.expectPeek(token.LBrace) {
		return nil
	}
	stmt.Body = p.parseBlockStatement()
	return stmt
}

func (p *Parser) parseImportStatement() *ast.ImportStatement {
	stmt := &ast.ImportStatement{Token: p.curToken}

	if !p.expectPeek(token.String) {
		return nil
	}
	stmt.Path = &ast.StringLiteral{Token: p.curToken, Value: p.curToken.Literal}

	if p.peekTokenIs(token.As) {
		p.nextToken()
		if !p.expectPeek(token.Ident) {
			return nil
		}
		stmt.Alias = &ast.Identifier{Token: p.curToken, Value: p.curToken.Literal}
	}

	if p.peekTokenIs(token.Semicolon) {
		p.nextToken()
	}

	return stmt
}

func (p *Parser) parseLetStatement() *ast.LetStatement {
	stmt := &ast.LetStatement{Token: p.curToken}

	if p.peekTokenIs(token.Mut) {
		stmt.Mutable = true
		p.nextToken()
	}

	if !p.expectPeek(token.Ident) {
		return nil
	}

	stmt.Name = &ast.Identifier{Token: p.curToken, Value: p.curToken.Literal}

	if !p.expectPeek(token.Assign) {
		return nil
	}

	p.nextToken()
	stmt.Value = p.parseExpression(lowest)

	if p.peekTokenIs(token.Semicolon) {
		p.nextToken()
	}

	return stmt
}

func (p *Parser) parseNamedFunctionStatement() *ast.LetStatement {
	tok := p.curToken
	p.nextToken()
	name := &ast.Identifier{Token: p.curToken, Value: p.curToken.Literal}

	if !p.expectPeek(token.LParen) {
		return nil
	}

	lit := &ast.FunctionLiteral{Token: tok, Name: name.Value}
	lit.Parameters = p.parseFunctionParameters()
	if !p.expectPeek(token.LBrace) {
		return nil
	}
	lit.Body = p.parseBlockStatement()

	return &ast.LetStatement{Token: token.Token{Type: token.Let, Literal: "let", Line: tok.Line, Column: tok.Column}, Name: name, Value: lit}
}

func (p *Parser) parseAssignStatement() *ast.AssignStatement {
	stmt := &ast.AssignStatement{Token: p.peekToken}
	stmt.Name = &ast.Identifier{Token: p.curToken, Value: p.curToken.Literal}

	p.nextToken()
	p.nextToken()
	stmt.Value = p.parseExpression(lowest)

	if p.peekTokenIs(token.Semicolon) {
		p.nextToken()
	}

	return stmt
}

func (p *Parser) parseReturnStatement() *ast.ReturnStatement {
	stmt := &ast.ReturnStatement{Token: p.curToken}

	p.nextToken()
	stmt.ReturnValue = p.parseExpression(lowest)

	if p.peekTokenIs(token.Semicolon) {
		p.nextToken()
	}

	return stmt
}

func (p *Parser) parseExpressionStatement() *ast.ExpressionStatement {
	stmt := &ast.ExpressionStatement{Token: p.curToken}
	stmt.Expression = p.parseExpression(lowest)

	if p.peekTokenIs(token.Semicolon) {
		p.nextToken()
	}

	return stmt
}

func (p *Parser) parseExpression(precedence int) ast.Expression {
	prefix := p.prefixParseFns[p.curToken.Type]
	if prefix == nil {
		p.noPrefixParseFnError(p.curToken)
		return nil
	}
	leftExp := prefix()

	for !p.peekTokenIs(token.Semicolon) && precedence < p.peekPrecedence() {
		infix := p.infixParseFns[p.peekToken.Type]
		if infix == nil {
			return leftExp
		}
		p.nextToken()
		leftExp = infix(leftExp)
	}

	return leftExp
}

func (p *Parser) parseIdentifier() ast.Expression {
	return &ast.Identifier{Token: p.curToken, Value: p.curToken.Literal}
}

func (p *Parser) parseIntegerLiteral() ast.Expression {
	lit := &ast.IntegerLiteral{Token: p.curToken}
	value, err := strconv.ParseInt(p.curToken.Literal, 0, 64)
	if err != nil {
		p.errors = append(p.errors, p.atCur(fmt.Sprintf("could not parse %q as integer", p.curToken.Literal)))
		return nil
	}
	lit.Value = value
	return lit
}

func (p *Parser) parseFloatLiteral() ast.Expression {
	lit := &ast.FloatLiteral{Token: p.curToken}
	value, err := strconv.ParseFloat(p.curToken.Literal, 64)
	if err != nil {
		p.errors = append(p.errors, p.atCur(fmt.Sprintf("could not parse %q as float", p.curToken.Literal)))
		return nil
	}
	lit.Value = value
	return lit
}

func (p *Parser) parseStringLiteral() ast.Expression {
	return &ast.StringLiteral{Token: p.curToken, Value: p.curToken.Literal}
}

func (p *Parser) parseBoolean() ast.Expression {
	return &ast.Boolean{Token: p.curToken, Value: p.curTokenIs(token.True)}
}

func (p *Parser) parseNil() ast.Expression {
	return &ast.NilLiteral{Token: p.curToken}
}

func (p *Parser) parsePrefixExpression() ast.Expression {
	expression := &ast.PrefixExpression{Token: p.curToken, Operator: p.curToken.Literal}
	p.nextToken()
	expression.Right = p.parseExpression(prefix)
	return expression
}

func (p *Parser) parseInfixExpression(left ast.Expression) ast.Expression {
	expression := &ast.InfixExpression{Token: p.curToken, Operator: p.curToken.Literal, Left: left}
	precedence := p.curPrecedence()
	p.nextToken()
	expression.Right = p.parseExpression(precedence)
	return expression
}

func (p *Parser) parsePipelineExpression(left ast.Expression) ast.Expression {
	expression := &ast.PipelineExpression{Token: p.curToken, Left: left}
	precedence := p.curPrecedence()
	p.nextToken()
	expression.Right = p.parseExpression(precedence)
	return expression
}

func (p *Parser) parseGroupedExpression() ast.Expression {
	p.nextToken()
	exp := p.parseExpression(lowest)
	if !p.expectPeek(token.RParen) {
		return nil
	}
	return exp
}

func (p *Parser) parseIfExpression() ast.Expression {
	expression := &ast.IfExpression{Token: p.curToken}

	p.nextToken()
	expression.Condition = p.parseExpression(lowest)

	if !p.expectPeek(token.LBrace) {
		return nil
	}
	expression.Consequence = p.parseBlockStatement()

	if p.peekTokenIs(token.Else) {
		p.nextToken()
		if !p.expectPeek(token.LBrace) {
			return nil
		}
		expression.Alternative = p.parseBlockStatement()
	}

	return expression
}

func (p *Parser) parseBlockStatement() *ast.BlockStatement {
	block := &ast.BlockStatement{Token: p.curToken}
	block.Statements = []ast.Statement{}

	p.nextToken()

	for !p.curTokenIs(token.RBrace) && !p.curTokenIs(token.EOF) {
		stmt := p.parseStatement()
		if stmt != nil {
			block.Statements = append(block.Statements, stmt)
		}
		p.nextToken()
	}

	return block
}

func (p *Parser) parseFunctionLiteral() ast.Expression {
	lit := &ast.FunctionLiteral{Token: p.curToken}

	if !p.expectPeek(token.LParen) {
		return nil
	}

	lit.Parameters = p.parseFunctionParameters()

	if !p.expectPeek(token.LBrace) {
		return nil
	}

	lit.Body = p.parseBlockStatement()

	return lit
}

func (p *Parser) parseArrayLiteral() ast.Expression {
	array := &ast.ArrayLiteral{Token: p.curToken}
	array.Elements = p.parseExpressionList(token.RBracket)
	return array
}

func (p *Parser) parseMapLiteral() ast.Expression {
	lit := &ast.MapLiteral{Token: p.curToken, Pairs: []ast.MapPair{}}

	for !p.peekTokenIs(token.RBrace) {
		p.nextToken()
		key := p.parseExpression(lowest)
		if key == nil {
			return nil
		}
		if !p.expectPeek(token.Colon) {
			return nil
		}
		p.nextToken()
		value := p.parseExpression(lowest)
		if value == nil {
			return nil
		}
		lit.Pairs = append(lit.Pairs, ast.MapPair{Key: key, Value: value})
		if !p.peekTokenIs(token.RBrace) && !p.expectPeek(token.Comma) {
			return nil
		}
	}

	if !p.expectPeek(token.RBrace) {
		return nil
	}
	return lit
}

func (p *Parser) parseFunctionParameters() []*ast.Identifier {
	identifiers := []*ast.Identifier{}

	if p.peekTokenIs(token.RParen) {
		p.nextToken()
		return identifiers
	}

	p.nextToken()
	ident := &ast.Identifier{Token: p.curToken, Value: p.curToken.Literal}
	identifiers = append(identifiers, ident)

	for p.peekTokenIs(token.Comma) {
		p.nextToken()
		p.nextToken()
		ident := &ast.Identifier{Token: p.curToken, Value: p.curToken.Literal}
		identifiers = append(identifiers, ident)
	}

	if !p.expectPeek(token.RParen) {
		return nil
	}

	return identifiers
}

func (p *Parser) parseCallExpression(function ast.Expression) ast.Expression {
	exp := &ast.CallExpression{Token: p.curToken, Function: function}
	exp.Arguments = p.parseExpressionList(token.RParen)
	return exp
}

func (p *Parser) parseIndexExpression(left ast.Expression) ast.Expression {
	exp := &ast.IndexExpression{Token: p.curToken, Left: left}
	p.nextToken()
	exp.Index = p.parseExpression(lowest)
	if !p.expectPeek(token.RBracket) {
		return nil
	}
	return exp
}

func (p *Parser) parseMemberExpression(left ast.Expression) ast.Expression {
	exp := &ast.MemberExpression{Token: p.curToken, Object: left}
	if !p.expectPeek(token.Ident) {
		return nil
	}
	exp.Property = &ast.Identifier{Token: p.curToken, Value: p.curToken.Literal}
	return exp
}

func (p *Parser) parseMatchExpression() ast.Expression {
	expression := &ast.MatchExpression{Token: p.curToken}
	p.nextToken()
	expression.Value = p.parseExpression(lowest)
	if expression.Value == nil {
		return nil
	}
	if !p.expectPeek(token.LBrace) {
		return nil
	}

	for !p.peekTokenIs(token.RBrace) {
		p.nextToken()
		arm := ast.MatchArm{}
		if p.curTokenIs(token.Ident) && p.curToken.Literal == "_" {
			arm.Wildcard = true
		} else {
			arm.Pattern = p.parseExpression(lowest)
			if arm.Pattern == nil {
				return nil
			}
		}
		if !p.expectPeek(token.Arrow) {
			return nil
		}
		p.nextToken()
		arm.Value = p.parseExpression(lowest)
		if arm.Value == nil {
			return nil
		}
		expression.Arms = append(expression.Arms, arm)
		if p.peekTokenIs(token.Comma) {
			p.nextToken()
		}
	}

	if !p.expectPeek(token.RBrace) {
		return nil
	}
	return expression
}

func (p *Parser) parseExpressionList(end token.Type) []ast.Expression {
	list := []ast.Expression{}

	if p.peekTokenIs(end) {
		p.nextToken()
		return list
	}

	p.nextToken()
	list = append(list, p.parseExpression(lowest))

	for p.peekTokenIs(token.Comma) {
		p.nextToken()
		p.nextToken()
		list = append(list, p.parseExpression(lowest))
	}

	if !p.expectPeek(end) {
		return nil
	}

	return list
}

func (p *Parser) nextToken() {
	p.curToken = p.peekToken
	p.peekToken = p.l.NextToken()
}

func (p *Parser) registerPrefix(tokenType token.Type, fn prefixParseFn) {
	p.prefixParseFns[tokenType] = fn
}

func (p *Parser) registerInfix(tokenType token.Type, fn infixParseFn) {
	p.infixParseFns[tokenType] = fn
}

func (p *Parser) expectPeek(t token.Type) bool {
	if p.peekTokenIs(t) {
		p.nextToken()
		return true
	}
	p.peekError(t)
	return false
}

func (p *Parser) peekError(t token.Type) {
	msg := fmt.Sprintf("line %d:%d expected next token to be %s, got %s instead",
		p.peekToken.Line, p.peekToken.Column, t, p.peekToken.Type)
	p.errors = append(p.errors, msg)
}

func (p *Parser) noPrefixParseFnError(tok token.Token) {
	p.errors = append(p.errors, fmt.Sprintf("line %d:%d no parser for token %s (%q)", tok.Line, tok.Column, tok.Type, tok.Literal))
}

func (p *Parser) atCur(msg string) string {
	return fmt.Sprintf("line %d:%d %s", p.curToken.Line, p.curToken.Column, msg)
}

func (p *Parser) peekPrecedence() int {
	if p, ok := precedences[p.peekToken.Type]; ok {
		return p
	}
	return lowest
}

func (p *Parser) curPrecedence() int {
	if p, ok := precedences[p.curToken.Type]; ok {
		return p
	}
	return lowest
}

func (p *Parser) curTokenIs(t token.Type) bool {
	return p.curToken.Type == t
}

func (p *Parser) peekTokenIs(t token.Type) bool {
	return p.peekToken.Type == t
}
