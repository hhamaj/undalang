package parser

import (
	"fmt"
	"strconv"
	"strings"

	"undalang/ast"
	"undalang/diag"
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
	token.Question: index,
}

type Parser struct {
	l *lexer.Lexer

	errors []string

	curToken  token.Token
	peekToken token.Token
	// ahead holds tokens read past peekToken by peekAt
	ahead []token.Token
	// cFields makes struct fields take C types, inside extern blocks
	cFields bool

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
	p.registerInfix(token.Question, func(left ast.Expression) ast.Expression {
		return &ast.TryExpression{Token: p.curToken, Value: left}
	})

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
		before := len(p.errors)
		stmt := p.parseStatement()
		if len(p.errors) > before {
			// later errors in the same statement are almost always fallout
			// from the first one so we skip to the next line
			p.errors = p.errors[:before+1]
			for !p.peekTokenIs(token.EOF) && p.peekToken.Line == p.curToken.Line {
				p.nextToken()
			}
		}
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
	case p.curTokenIs(token.Struct):
		return p.parseStructStatement()
	case p.curTokenIs(token.Extern):
		return p.parseExternStatement()
	default:
		return p.parseExpressionOrPathAssign()
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

	if p.peekTokenIs(token.Colon) {
		p.nextToken()
		if stmt.Type = p.parseTypeAnnotation(); stmt.Type == nil {
			return nil
		}
	}

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
	if !p.parseSignature(lit) {
		return nil
	}

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

// parseStructStatement parses `struct Name { a: int, b }`. the commas
// between fields are optional so one field per line reads fine
func (p *Parser) parseStructStatement() ast.Statement {
	stmt := &ast.StructStatement{Token: p.curToken}
	if !p.expectPeek(token.Ident) {
		return nil
	}
	stmt.Name = &ast.Identifier{Token: p.curToken, Value: p.curToken.Literal}
	if !ast.IsStructName(stmt.Name.Value) {
		p.errors = append(p.errors, p.atCur(fmt.Sprintf("struct names start with an uppercase letter, got %q", stmt.Name.Value)))
		return nil
	}
	if reservedTypeNames[stmt.Name.Value] {
		p.errors = append(p.errors, p.atCur(fmt.Sprintf("%s is a builtin type name", stmt.Name.Value)))
		return nil
	}
	if !p.expectPeek(token.LBrace) {
		return nil
	}
	seen := map[string]bool{}
	for !p.peekTokenIs(token.RBrace) {
		if !p.expectPeek(token.Ident) {
			return nil
		}
		if seen[p.curToken.Literal] {
			p.errors = append(p.errors, p.atCur(fmt.Sprintf("duplicate field %s in %s", p.curToken.Literal, stmt.Name.Value)))
			return nil
		}
		seen[p.curToken.Literal] = true
		stmt.Fields = append(stmt.Fields, &ast.Identifier{Token: p.curToken, Value: p.curToken.Literal})
		var typ *ast.TypeAnnotation
		if p.peekTokenIs(token.Colon) {
			p.nextToken()
			if typ = p.fieldType(); typ == nil {
				return nil
			}
		}
		stmt.Types = append(stmt.Types, typ)
		if p.peekTokenIs(token.Comma) {
			p.nextToken()
		}
	}
	p.nextToken()
	return stmt
}

// parseExternStatement parses `extern "lib" { fn name(a: i32) -> f64 ... }`.
// every parameter needs a C type, the return type is optional (void)
func (p *Parser) parseExternStatement() ast.Statement {
	stmt := &ast.ExternStatement{Token: p.curToken}
	if !p.expectPeek(token.String) {
		return nil
	}
	stmt.Library = &ast.StringLiteral{Token: p.curToken, Value: p.curToken.Literal}
	if !p.expectPeek(token.LBrace) {
		return nil
	}
	for !p.peekTokenIs(token.RBrace) {
		if p.peekTokenIs(token.Struct) {
			p.nextToken()
			st := p.parseExternStruct()
			if st == nil {
				return nil
			}
			stmt.Structs = append(stmt.Structs, st)
			continue
		}
		if !p.expectPeek(token.Function) || !p.expectPeek(token.Ident) {
			return nil
		}
		fn := &ast.ExternFunc{Name: &ast.Identifier{Token: p.curToken, Value: p.curToken.Literal}}
		if !p.expectPeek(token.LParen) {
			return nil
		}
		for !p.peekTokenIs(token.RParen) {
			if len(fn.Params) > 0 && !p.expectPeek(token.Comma) {
				return nil
			}
			if !p.expectPeek(token.Ident) {
				return nil
			}
			fn.Params = append(fn.Params, &ast.Identifier{Token: p.curToken, Value: p.curToken.Literal})
			if !p.expectPeek(token.Colon) {
				return nil
			}
			typ := p.parseCType()
			if typ == nil {
				return nil
			}
			fn.Types = append(fn.Types, typ)
		}
		p.nextToken()
		if p.peekTokenIs(token.Arrow) {
			p.nextToken()
			if fn.Return = p.parseCType(); fn.Return == nil {
				return nil
			}
		}
		stmt.Funcs = append(stmt.Funcs, fn)
		if p.peekTokenIs(token.Semicolon) {
			p.nextToken()
		}
	}
	p.nextToken()
	return stmt
}

func (p *Parser) fieldType() *ast.TypeAnnotation {
	if p.cFields {
		return p.parseCType()
	}
	return p.parseTypeAnnotation()
}

// parseExternStruct parses `struct Name { field: ctype ... }` inside an
// extern block. the Unda side gets the field types the C types take
func (p *Parser) parseExternStruct() *ast.ExternStruct {
	p.cFields = true
	st, ok := p.parseStructStatement().(*ast.StructStatement)
	p.cFields = false
	if !ok || st == nil {
		return nil
	}
	out := &ast.ExternStruct{Decl: st, CTypes: st.Types}
	st.Types = make([]*ast.TypeAnnotation, len(out.CTypes))
	for i, t := range out.CTypes {
		if t == nil || !(ast.CTypes[t.Name] || ast.IsStructName(t.Name)) {
			p.errors = append(p.errors, p.atCur(fmt.Sprintf("field %s of %s needs a C type", st.Fields[i].Value, st.Name.Value)))
			return nil
		}
		if name := undaTypeForC(t.Name); name != "" {
			st.Types[i] = &ast.TypeAnnotation{Token: t.Token, Name: name}
		}
	}
	return out
}

// undaTypeForC is the Unda type a C field takes. cstring and ptr fields can
// be NULL, so they stay untyped and get checked when converted
func undaTypeForC(c string) string {
	switch {
	case c == "f32" || c == "f64":
		return "number"
	case c == "bool":
		return "bool"
	case c == "cstring" || c == "ptr":
		return ""
	case ast.IsStructName(c):
		return c
	}
	return "int"
}

func (p *Parser) parseCType() *ast.TypeAnnotation {
	if !p.expectPeek(token.Ident) {
		return nil
	}
	if ast.IsStructName(p.curToken.Literal) {
		return &ast.TypeAnnotation{Token: p.curToken, Name: p.curToken.Literal}
	}
	if !ast.CTypes[p.curToken.Literal] {
		hint := ""
		if c, ok := cTypeHints[p.curToken.Literal]; ok {
			hint = fmt.Sprintf(" (did you mean %q?)", c)
		}
		p.errors = append(p.errors, p.atCur(fmt.Sprintf("unknown C type %q%s", p.curToken.Literal, hint)))
		return nil
	}
	return &ast.TypeAnnotation{Token: p.curToken, Name: p.curToken.Literal}
}

// cTypeHints map C and Unda spellings to the extern type names
var cTypeHints = map[string]string{
	"int": "i32", "long": "i64", "short": "i16", "char": "i8", "double": "f64", "float": "f32",
	"size_t": "u64", "string": "cstring", "str": "cstring", "int32": "i32", "int64": "i64",
}

// reservedTypeNames are the runtime's own type names. a struct can't take
// one, since a struct value's type name is its struct's name
var reservedTypeNames = map[string]bool{
	"INTEGER": true, "FLOAT": true, "STRING": true, "BOOLEAN": true, "NIL": true, "ARRAY": true,
	"MAP": true, "FUNCTION": true, "BUILTIN": true, "MODULE": true, "RESULT": true, "STRUCT": true,
	"ERROR": true, "RETURN_VALUE": true, "BREAK": true, "CONTINUE": true,
}

// parseExpressionOrPathAssign parses an expression statement, or a path
// assignment like `p.x = 1` or `m[k] = v` when a chain of members and
// indexes on a name is followed by `=`
func (p *Parser) parseExpressionOrPathAssign() ast.Statement {
	stmt := p.parseExpressionStatement()
	if !p.peekTokenIs(token.Assign) {
		return stmt
	}
	var path []ast.PathStep
	root := stmt.Expression
	for {
		if m, ok := root.(*ast.MemberExpression); ok {
			path = append([]ast.PathStep{{Field: m.Property}}, path...)
			root = m.Object
		} else if ix, ok := root.(*ast.IndexExpression); ok {
			path = append([]ast.PathStep{{Index: ix.Index}}, path...)
			root = ix.Left
		} else {
			break
		}
	}
	if len(path) == 0 {
		return stmt
	}
	name, ok := root.(*ast.Identifier)
	if !ok {
		p.errors = append(p.errors, p.atCur("can only assign to fields and elements of a named value"))
		return nil
	}
	assign := &ast.AssignStatement{Token: p.peekToken, Name: name, Path: path}
	p.nextToken()
	p.nextToken()
	assign.Value = p.parseExpression(lowest)
	if p.peekTokenIs(token.Semicolon) {
		p.nextToken()
	}
	return assign
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
	if call, ok := expression.Right.(*ast.CallExpression); ok && call.ArgNames != nil {
		p.errors = append(p.errors, p.atCur("named arguments can't follow |>, the piped value has no name"))
		return nil
	}
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

	if !p.expectPeek(token.LParen) || !p.parseSignature(lit) {
		return nil
	}

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

// parseSignature parses the parameters, the optional `-> type` and the
// opening brace, starting at the `(`. it fills lit in and leaves the parser
// on the `{`
func (p *Parser) parseSignature(lit *ast.FunctionLiteral) bool {
	lit.Parameters = []*ast.Identifier{}
	lit.ParamTypes = []*ast.TypeAnnotation{}
	for !p.peekTokenIs(token.RParen) {
		if len(lit.Parameters) > 0 {
			if !p.peekTokenIs(token.Comma) {
				p.peekError(token.RParen)
				return false
			}
			p.nextToken()
		}
		p.nextToken()
		lit.Parameters = append(lit.Parameters, &ast.Identifier{Token: p.curToken, Value: p.curToken.Literal})
		var typ *ast.TypeAnnotation
		if p.peekTokenIs(token.Colon) {
			p.nextToken()
			if typ = p.parseTypeAnnotation(); typ == nil {
				return false
			}
		}
		lit.ParamTypes = append(lit.ParamTypes, typ)
	}
	p.nextToken()
	if p.peekTokenIs(token.Arrow) {
		p.nextToken()
		if lit.ReturnType = p.parseTypeAnnotation(); lit.ReturnType == nil {
			return false
		}
	}
	if !p.expectPeek(token.LBrace) {
		return false
	}
	lit.Body = p.parseBlockStatement()
	return true
}

// parseTypeAnnotation reads the type name after a `:` or `->`. `nil` and `fn`
// are keywords so they come in as their own tokens
func (p *Parser) parseTypeAnnotation() *ast.TypeAnnotation {
	if !p.peekTokenIs(token.Ident) && !p.peekTokenIs(token.Nil) && !p.peekTokenIs(token.Function) {
		p.peekError(token.Ident)
		return nil
	}
	p.nextToken()
	if _, ok := ast.TypeNames[p.curToken.Literal]; !ok && !ast.IsStructName(p.curToken.Literal) {
		hint := diag.DidYouMean(p.curToken.Literal, typeNames())
		if alias, ok := typeAliases[p.curToken.Literal]; ok {
			hint = fmt.Sprintf(" (did you mean %q?)", alias)
		}
		p.errors = append(p.errors, p.atCur(fmt.Sprintf("unknown type %q%s", p.curToken.Literal, hint)))
		return nil
	}
	typ := &ast.TypeAnnotation{Token: p.curToken, Name: p.curToken.Literal}
	if want := elementTypeCount[typ.Name]; want > 0 && p.peekTokenIs(token.LBracket) {
		p.nextToken()
		for len(typ.Params) < want {
			if len(typ.Params) > 0 && !p.expectPeek(token.Comma) {
				return nil
			}
			param := p.parseTypeAnnotation()
			if param == nil {
				return nil
			}
			typ.Params = append(typ.Params, param)
		}
		if !p.expectPeek(token.RBracket) {
			return nil
		}
		if typ.Name == "map" && !hashableTypes[typ.Params[0].Name] {
			p.errors = append(p.errors, p.atCur(fmt.Sprintf("map keys can't be %s, use int, string, bool, nil or any", typ.Params[0])))
			return nil
		}
	}
	return typ
}

// elementTypeCount says how many element types a collection type takes in
// brackets: array[int], map[string, int]
var elementTypeCount = map[string]int{"array": 1, "map": 2}

var hashableTypes = map[string]bool{"int": true, "string": true, "bool": true, "nil": true, "any": true}

func (p *Parser) parseCallExpression(function ast.Expression) ast.Expression {
	exp := &ast.CallExpression{Token: p.curToken, Function: function}
	if p.peekTokenIs(token.Ident) && p.peekAt(1).Type == token.Colon {
		return p.parseNamedArguments(exp)
	}
	exp.Arguments = p.parseExpressionList(token.RParen)
	return exp
}

// parseNamedArguments parses `(a: 1, b: 2)`. every argument needs a name once
// the first one has one
func (p *Parser) parseNamedArguments(exp *ast.CallExpression) ast.Expression {
	exp.Arguments = []ast.Expression{}
	exp.ArgNames = []*ast.Identifier{}
	for !p.peekTokenIs(token.RParen) {
		if len(exp.Arguments) > 0 && !p.expectPeek(token.Comma) {
			return nil
		}
		if !p.expectPeek(token.Ident) {
			return nil
		}
		name := &ast.Identifier{Token: p.curToken, Value: p.curToken.Literal}
		if !p.expectPeek(token.Colon) {
			return nil
		}
		p.nextToken()
		arg := p.parseExpression(lowest)
		if arg == nil {
			return nil
		}
		exp.ArgNames = append(exp.ArgNames, name)
		exp.Arguments = append(exp.Arguments, arg)
	}
	p.nextToken()
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
		arm.Pattern = p.parsePattern()
		if arm.Pattern == nil {
			return nil
		}
		if p.peekTokenIs(token.If) {
			p.nextToken()
			p.nextToken()
			arm.Guard = p.parseExpression(lowest)
			if arm.Guard == nil {
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

// parsePattern parses a match pattern starting at the current token. a bare
// identifier binds, anything else gets compared by value
func (p *Parser) parsePattern() ast.Pattern {
	switch {
	case p.curTokenIs(token.Ident) && p.curToken.Literal == "_":
		return &ast.WildcardPattern{Token: p.curToken}
	case p.curTokenIs(token.Ident) && (p.curToken.Literal == "ok" || p.curToken.Literal == "err") && p.peekTokenIs(token.LParen):
		return p.parseResultPattern()
	case p.curTokenIs(token.Ident) && p.patternEndsAtPeek():
		return &ast.BindingPattern{Token: p.curToken, Name: &ast.Identifier{Token: p.curToken, Value: p.curToken.Literal}}
	case p.curTokenIs(token.LBracket):
		return p.parseArrayPattern()
	case p.curTokenIs(token.LBrace):
		return p.parseMapPattern()
	}
	tok := p.curToken
	value := p.parseExpression(lowest)
	if value == nil {
		return nil
	}
	return &ast.ValuePattern{Token: tok, Value: value}
}

func (p *Parser) patternEndsAtPeek() bool {
	switch p.peekToken.Type {
	case token.Arrow, token.If, token.Comma, token.RBracket, token.RBrace, token.RParen:
		return true
	}
	return false
}

func (p *Parser) parseResultPattern() ast.Pattern {
	pat := &ast.ResultPattern{Token: p.curToken, Ok: p.curToken.Literal == "ok"}
	p.nextToken()
	p.nextToken()
	if pat.Inner = p.parsePattern(); pat.Inner == nil || !p.expectPeek(token.RParen) {
		return nil
	}
	return pat
}

func (p *Parser) parseArrayPattern() ast.Pattern {
	pat := &ast.ArrayPattern{Token: p.curToken}
	for !p.peekTokenIs(token.RBracket) {
		p.nextToken()
		if p.curTokenIs(token.DotDot) {
			pat.HasRest = true
			if p.peekTokenIs(token.Ident) {
				p.nextToken()
				pat.Rest = &ast.Identifier{Token: p.curToken, Value: p.curToken.Literal}
			}
			if !p.peekTokenIs(token.RBracket) {
				p.errors = append(p.errors, p.atCur("rest pattern must be last in an array pattern"))
				return nil
			}
			break
		}
		el := p.parsePattern()
		if el == nil {
			return nil
		}
		pat.Elements = append(pat.Elements, el)
		if !p.peekTokenIs(token.RBracket) && !p.expectPeek(token.Comma) {
			return nil
		}
	}
	if !p.expectPeek(token.RBracket) {
		return nil
	}
	return pat
}

func (p *Parser) parseMapPattern() ast.Pattern {
	pat := &ast.MapPattern{Token: p.curToken}
	for !p.peekTokenIs(token.RBrace) {
		p.nextToken()
		key := p.parseExpression(lowest)
		if key == nil || !p.expectPeek(token.Colon) {
			return nil
		}
		p.nextToken()
		value := p.parsePattern()
		if value == nil {
			return nil
		}
		pat.Keys = append(pat.Keys, key)
		pat.Values = append(pat.Values, value)
		if !p.peekTokenIs(token.RBrace) && !p.expectPeek(token.Comma) {
			return nil
		}
	}
	if !p.expectPeek(token.RBrace) {
		return nil
	}
	return pat
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

// typeAliases are type names people bring from other languages
var typeAliases = map[string]string{
	"integer": "int", "i64": "int", "int64": "int", "double": "float", "f64": "float",
	"str": "string", "boolean": "bool", "list": "array", "dict": "map",
	"func": "fn", "function": "fn", "none": "nil", "null": "nil",
}

func typeNames() []string {
	names := make([]string, 0, len(ast.TypeNames))
	for name := range ast.TypeNames {
		names = append(names, name)
	}
	return names
}

func (p *Parser) nextToken() {
	p.curToken = p.peekToken
	if len(p.ahead) > 0 {
		p.peekToken = p.ahead[0]
		p.ahead = p.ahead[1:]
		return
	}
	p.peekToken = p.l.NextToken()
}

// peekAt returns the token n places after peekToken without moving
func (p *Parser) peekAt(n int) token.Token {
	for len(p.ahead) < n {
		p.ahead = append(p.ahead, p.l.NextToken())
	}
	return p.ahead[n-1]
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
	var msg string
	switch {
	case tok.Type == token.Illegal && strings.HasPrefix(tok.Literal, `"`):
		msg = "unterminated string literal"
	case tok.Type == token.Illegal:
		msg = fmt.Sprintf("unexpected character %q", tok.Literal)
	case tok.Type == token.EOF:
		msg = "unexpected end of input"
	default:
		msg = fmt.Sprintf("unexpected %q", tok.Literal)
	}
	p.errors = append(p.errors, fmt.Sprintf("line %d:%d %s", tok.Line, tok.Column, msg))
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
