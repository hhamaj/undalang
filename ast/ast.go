package ast

import (
	"bytes"
	"strings"

	"undalang/token"
)

type Node interface {
	TokenLiteral() string
	String() string
}

type Statement interface {
	Node
	statementNode()
}

type Expression interface {
	Node
	expressionNode()
}

type Program struct {
	Statements []Statement
}

func (p *Program) TokenLiteral() string {
	if len(p.Statements) > 0 {
		return p.Statements[0].TokenLiteral()
	}
	return ""
}

func (p *Program) String() string {
	var out bytes.Buffer
	for _, s := range p.Statements {
		out.WriteString(s.String())
	}
	return out.String()
}

type LetStatement struct {
	Token   token.Token
	Name    *Identifier
	Type    *TypeAnnotation
	Value   Expression
	Mutable bool
}

func (ls *LetStatement) statementNode()       {}
func (ls *LetStatement) TokenLiteral() string { return ls.Token.Literal }
func (ls *LetStatement) String() string {
	var out bytes.Buffer
	out.WriteString(ls.TokenLiteral() + " ")
	if ls.Mutable {
		out.WriteString("mut ")
	}
	out.WriteString(ls.Name.String())
	if ls.Type != nil {
		out.WriteString(": " + ls.Type.String())
	}
	out.WriteString(" = ")
	if ls.Value != nil {
		out.WriteString(ls.Value.String())
	}
	return out.String()
}

type AssignStatement struct {
	Token token.Token
	Name  *Identifier
	Value Expression
}

func (as *AssignStatement) statementNode()       {}
func (as *AssignStatement) TokenLiteral() string { return as.Token.Literal }
func (as *AssignStatement) String() string {
	var out bytes.Buffer
	out.WriteString(as.Name.String())
	out.WriteString(" = ")
	if as.Value != nil {
		out.WriteString(as.Value.String())
	}
	return out.String()
}

type ReturnStatement struct {
	Token       token.Token
	ReturnValue Expression
}

func (rs *ReturnStatement) statementNode()       {}
func (rs *ReturnStatement) TokenLiteral() string { return rs.Token.Literal }
func (rs *ReturnStatement) String() string {
	var out bytes.Buffer
	out.WriteString(rs.TokenLiteral() + " ")
	if rs.ReturnValue != nil {
		out.WriteString(rs.ReturnValue.String())
	}
	return out.String()
}

type ImportStatement struct {
	Token token.Token
	Path  *StringLiteral
	Alias *Identifier
}

func (is *ImportStatement) statementNode()       {}
func (is *ImportStatement) TokenLiteral() string { return is.Token.Literal }
func (is *ImportStatement) String() string {
	var out bytes.Buffer
	out.WriteString("import ")
	if is.Path != nil {
		out.WriteString(is.Path.String())
	}
	if is.Alias != nil {
		out.WriteString(" as ")
		out.WriteString(is.Alias.String())
	}
	return out.String()
}

type WhileStatement struct {
	Token     token.Token
	Condition Expression
	Body      *BlockStatement
}

func (ws *WhileStatement) statementNode()       {}
func (ws *WhileStatement) TokenLiteral() string { return ws.Token.Literal }
func (ws *WhileStatement) String() string {
	return "while " + ws.Condition.String() + " { " + ws.Body.String() + " }"
}

// ForStatement loops over an array, map or string. with one variable you get
// the elements, the keys or the characters, with two you get index/key and
// value
type ForStatement struct {
	Token    token.Token
	Vars     []*Identifier
	Iterable Expression
	Body     *BlockStatement
}

func (fs *ForStatement) statementNode()       {}
func (fs *ForStatement) TokenLiteral() string { return fs.Token.Literal }
func (fs *ForStatement) String() string {
	names := make([]string, 0, len(fs.Vars))
	for _, v := range fs.Vars {
		names = append(names, v.String())
	}
	return "for " + strings.Join(names, ", ") + " in " + fs.Iterable.String() + " { " + fs.Body.String() + " }"
}

type BreakStatement struct {
	Token token.Token
}

func (bs *BreakStatement) statementNode()       {}
func (bs *BreakStatement) TokenLiteral() string { return bs.Token.Literal }
func (bs *BreakStatement) String() string       { return "break" }

type ContinueStatement struct {
	Token token.Token
}

func (cs *ContinueStatement) statementNode()       {}
func (cs *ContinueStatement) TokenLiteral() string { return cs.Token.Literal }
func (cs *ContinueStatement) String() string       { return "continue" }

type ExpressionStatement struct {
	Token      token.Token
	Expression Expression
}

func (es *ExpressionStatement) statementNode()       {}
func (es *ExpressionStatement) TokenLiteral() string { return es.Token.Literal }
func (es *ExpressionStatement) String() string {
	if es.Expression != nil {
		return es.Expression.String()
	}
	return ""
}

type BlockStatement struct {
	Token      token.Token
	Statements []Statement
}

func (bs *BlockStatement) statementNode()       {}
func (bs *BlockStatement) TokenLiteral() string { return bs.Token.Literal }
func (bs *BlockStatement) String() string {
	var out bytes.Buffer
	for _, s := range bs.Statements {
		out.WriteString(s.String())
	}
	return out.String()
}

type Identifier struct {
	Token token.Token
	Value string
}

func (i *Identifier) expressionNode()      {}
func (i *Identifier) TokenLiteral() string { return i.Token.Literal }
func (i *Identifier) String() string       { return i.Value }

type IntegerLiteral struct {
	Token token.Token
	Value int64
}

func (il *IntegerLiteral) expressionNode()      {}
func (il *IntegerLiteral) TokenLiteral() string { return il.Token.Literal }
func (il *IntegerLiteral) String() string       { return il.Token.Literal }

type FloatLiteral struct {
	Token token.Token
	Value float64
}

func (fl *FloatLiteral) expressionNode()      {}
func (fl *FloatLiteral) TokenLiteral() string { return fl.Token.Literal }
func (fl *FloatLiteral) String() string       { return fl.Token.Literal }

type StringLiteral struct {
	Token token.Token
	Value string
}

func (sl *StringLiteral) expressionNode()      {}
func (sl *StringLiteral) TokenLiteral() string { return sl.Token.Literal }
func (sl *StringLiteral) String() string       { return `"` + sl.Value + `"` }

type Boolean struct {
	Token token.Token
	Value bool
}

func (b *Boolean) expressionNode()      {}
func (b *Boolean) TokenLiteral() string { return b.Token.Literal }
func (b *Boolean) String() string       { return b.Token.Literal }

type NilLiteral struct {
	Token token.Token
}

func (nl *NilLiteral) expressionNode()      {}
func (nl *NilLiteral) TokenLiteral() string { return nl.Token.Literal }
func (nl *NilLiteral) String() string       { return "nil" }

type PrefixExpression struct {
	Token    token.Token
	Operator string
	Right    Expression
}

func (pe *PrefixExpression) expressionNode()      {}
func (pe *PrefixExpression) TokenLiteral() string { return pe.Token.Literal }
func (pe *PrefixExpression) String() string {
	var out bytes.Buffer
	out.WriteString("(")
	out.WriteString(pe.Operator)
	out.WriteString(pe.Right.String())
	out.WriteString(")")
	return out.String()
}

type InfixExpression struct {
	Token    token.Token
	Left     Expression
	Operator string
	Right    Expression
}

type PipelineExpression struct {
	Token token.Token
	Left  Expression
	Right Expression
}

func (pe *PipelineExpression) expressionNode()      {}
func (pe *PipelineExpression) TokenLiteral() string { return pe.Token.Literal }
func (pe *PipelineExpression) String() string {
	var out bytes.Buffer
	out.WriteString("(")
	out.WriteString(pe.Left.String())
	out.WriteString(" |> ")
	out.WriteString(pe.Right.String())
	out.WriteString(")")
	return out.String()
}

func (ie *InfixExpression) expressionNode()      {}
func (ie *InfixExpression) TokenLiteral() string { return ie.Token.Literal }
func (ie *InfixExpression) String() string {
	var out bytes.Buffer
	out.WriteString("(")
	out.WriteString(ie.Left.String())
	out.WriteString(" " + ie.Operator + " ")
	out.WriteString(ie.Right.String())
	out.WriteString(")")
	return out.String()
}

type IfExpression struct {
	Token       token.Token
	Condition   Expression
	Consequence *BlockStatement
	Alternative *BlockStatement
}

func (ie *IfExpression) expressionNode()      {}
func (ie *IfExpression) TokenLiteral() string { return ie.Token.Literal }
func (ie *IfExpression) String() string {
	var out bytes.Buffer
	out.WriteString("if")
	out.WriteString(ie.Condition.String())
	out.WriteString(" ")
	out.WriteString(ie.Consequence.String())
	if ie.Alternative != nil {
		out.WriteString("else ")
		out.WriteString(ie.Alternative.String())
	}
	return out.String()
}

type FunctionLiteral struct {
	Token      token.Token
	Parameters []*Identifier
	// ParamTypes lines up with Parameters, nil where a parameter has no type
	ParamTypes []*TypeAnnotation
	ReturnType *TypeAnnotation
	Body       *BlockStatement
	Name       string
}

// TypeAnnotation is a type written after a name (`x: int`) or after a
// parameter list (`-> int`)
type TypeAnnotation struct {
	Token token.Token
	Name  string
}

func (ta *TypeAnnotation) TokenLiteral() string { return ta.Token.Literal }
func (ta *TypeAnnotation) String() string       { return ta.Name }

// TypeNames maps every type you can write to the runtime type names it
// accepts. the checker and both backends build their own tables from this
var TypeNames = map[string][]string{
	"int":    {"INTEGER"},
	"float":  {"FLOAT"},
	"number": {"INTEGER", "FLOAT"},
	"string": {"STRING"},
	"bool":   {"BOOLEAN"},
	"nil":    {"NIL"},
	"array":  {"ARRAY"},
	"map":    {"MAP"},
	"fn":     {"FUNCTION", "BUILTIN"},
	"module": {"MODULE"},
	"result": {"RESULT"},
	"any":    {"INTEGER", "FLOAT", "STRING", "BOOLEAN", "NIL", "ARRAY", "MAP", "FUNCTION", "BUILTIN", "MODULE", "RESULT"},
}

// Accepts reports whether a value with the given runtime type name fits
func (ta *TypeAnnotation) Accepts(runtimeType string) bool {
	for _, name := range TypeNames[ta.Name] {
		if name == runtimeType {
			return true
		}
	}
	return false
}

// Signature renders a parameter list with its types and the return type,
// like `(a: int, b) -> int`
func Signature(params []*Identifier, types []*TypeAnnotation, ret *TypeAnnotation) string {
	parts := make([]string, 0, len(params))
	for i, p := range params {
		part := p.String()
		if i < len(types) && types[i] != nil {
			part += ": " + types[i].String()
		}
		parts = append(parts, part)
	}
	out := "(" + strings.Join(parts, ", ") + ")"
	if ret != nil {
		out += " -> " + ret.String()
	}
	return out
}

type ArrayLiteral struct {
	Token    token.Token
	Elements []Expression
}

func (al *ArrayLiteral) expressionNode()      {}
func (al *ArrayLiteral) TokenLiteral() string { return al.Token.Literal }
func (al *ArrayLiteral) String() string {
	var out bytes.Buffer
	elements := []string{}
	for _, e := range al.Elements {
		elements = append(elements, e.String())
	}
	out.WriteString("[")
	out.WriteString(strings.Join(elements, ", "))
	out.WriteString("]")
	return out.String()
}

type MapPair struct {
	Key   Expression
	Value Expression
}

type MapLiteral struct {
	Token token.Token
	Pairs []MapPair
}

func (ml *MapLiteral) expressionNode()      {}
func (ml *MapLiteral) TokenLiteral() string { return ml.Token.Literal }
func (ml *MapLiteral) String() string {
	var out bytes.Buffer
	pairs := []string{}
	for _, p := range ml.Pairs {
		pairs = append(pairs, p.Key.String()+": "+p.Value.String())
	}
	out.WriteString("{")
	out.WriteString(strings.Join(pairs, ", "))
	out.WriteString("}")
	return out.String()
}

type IndexExpression struct {
	Token token.Token
	Left  Expression
	Index Expression
}

func (ie *IndexExpression) expressionNode()      {}
func (ie *IndexExpression) TokenLiteral() string { return ie.Token.Literal }
func (ie *IndexExpression) String() string {
	return "(" + ie.Left.String() + "[" + ie.Index.String() + "])"
}

type MemberExpression struct {
	Token    token.Token
	Object   Expression
	Property *Identifier
}

func (me *MemberExpression) expressionNode()      {}
func (me *MemberExpression) TokenLiteral() string { return me.Token.Literal }
func (me *MemberExpression) String() string {
	return me.Object.String() + "." + me.Property.String()
}

// Pattern is the left side of a match arm
type Pattern interface {
	Node
	patternNode()
}

// WildcardPattern is `_`, it matches anything and binds nothing
type WildcardPattern struct {
	Token token.Token
}

func (wp *WildcardPattern) patternNode()         {}
func (wp *WildcardPattern) TokenLiteral() string { return wp.Token.Literal }
func (wp *WildcardPattern) String() string       { return "_" }

// BindingPattern matches anything and puts it in Name
type BindingPattern struct {
	Token token.Token
	Name  *Identifier
}

func (bp *BindingPattern) patternNode()         {}
func (bp *BindingPattern) TokenLiteral() string { return bp.Token.Literal }
func (bp *BindingPattern) String() string       { return bp.Name.String() }

// ValuePattern matches when the value equals an expression, usually a literal
type ValuePattern struct {
	Token token.Token
	Value Expression
}

func (vp *ValuePattern) patternNode()         {}
func (vp *ValuePattern) TokenLiteral() string { return vp.Token.Literal }
func (vp *ValuePattern) String() string       { return vp.Value.String() }

// ArrayPattern matches arrays element by element. with a rest marker (`..` or
// `..name`) the array just has to be at least as long as Elements and the
// leftovers can be bound to a name
type ArrayPattern struct {
	Token    token.Token
	Elements []Pattern
	HasRest  bool
	Rest     *Identifier
}

func (ap *ArrayPattern) patternNode()         {}
func (ap *ArrayPattern) TokenLiteral() string { return ap.Token.Literal }
func (ap *ArrayPattern) String() string {
	parts := make([]string, 0, len(ap.Elements)+1)
	for _, el := range ap.Elements {
		parts = append(parts, el.String())
	}
	if ap.HasRest {
		rest := ".."
		if ap.Rest != nil {
			rest += ap.Rest.String()
		}
		parts = append(parts, rest)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// MapPattern matches maps that have every key with a matching value. extra
// keys are fine
type MapPattern struct {
	Token  token.Token
	Keys   []Expression
	Values []Pattern
}

func (mp *MapPattern) patternNode()         {}
func (mp *MapPattern) TokenLiteral() string { return mp.Token.Literal }
func (mp *MapPattern) String() string {
	parts := make([]string, 0, len(mp.Keys))
	for i := range mp.Keys {
		parts = append(parts, mp.Keys[i].String()+": "+mp.Values[i].String())
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

type MatchArm struct {
	Pattern Pattern
	// Guard has to be truthy for the arm to be picked, if there is one
	Guard Expression
	Value Expression
}

type MatchExpression struct {
	Token token.Token
	Value Expression
	Arms  []MatchArm
}

func (me *MatchExpression) expressionNode()      {}
func (me *MatchExpression) TokenLiteral() string { return me.Token.Literal }
func (me *MatchExpression) String() string {
	var out bytes.Buffer
	arms := []string{}
	for _, a := range me.Arms {
		pattern := a.Pattern.String()
		if a.Guard != nil {
			pattern += " if " + a.Guard.String()
		}
		arms = append(arms, pattern+" -> "+a.Value.String())
	}
	out.WriteString("match ")
	out.WriteString(me.Value.String())
	out.WriteString(" { ")
	out.WriteString(strings.Join(arms, ", "))
	out.WriteString(" }")
	return out.String()
}

func (fl *FunctionLiteral) expressionNode()      {}
func (fl *FunctionLiteral) TokenLiteral() string { return fl.Token.Literal }
func (fl *FunctionLiteral) String() string {
	var out bytes.Buffer
	out.WriteString(fl.TokenLiteral())
	if fl.Name != "" {
		out.WriteString(" " + fl.Name)
	}
	out.WriteString(Signature(fl.Parameters, fl.ParamTypes, fl.ReturnType))
	out.WriteString(" ")
	out.WriteString(fl.Body.String())
	return out.String()
}

// TryExpression is `value?`. an ok result gives back what it wraps, an err
// result returns from the enclosing function right away
type TryExpression struct {
	Token token.Token
	Value Expression
}

func (te *TryExpression) expressionNode()      {}
func (te *TryExpression) TokenLiteral() string { return te.Token.Literal }
func (te *TryExpression) String() string       { return "(" + te.Value.String() + "?)" }

// ResultPattern matches `ok(p)` or `err(p)` and runs p on the wrapped value
type ResultPattern struct {
	Token token.Token
	Ok    bool
	Inner Pattern
}

func (rp *ResultPattern) patternNode()         {}
func (rp *ResultPattern) TokenLiteral() string { return rp.Token.Literal }
func (rp *ResultPattern) String() string       { return rp.Token.Literal + "(" + rp.Inner.String() + ")" }

type CallExpression struct {
	Token     token.Token
	Function  Expression
	Arguments []Expression
}

func (ce *CallExpression) expressionNode()      {}
func (ce *CallExpression) TokenLiteral() string { return ce.Token.Literal }
func (ce *CallExpression) String() string {
	var out bytes.Buffer
	args := []string{}
	for _, a := range ce.Arguments {
		args = append(args, a.String())
	}
	out.WriteString(ce.Function.String())
	out.WriteString("(")
	out.WriteString(strings.Join(args, ", "))
	out.WriteString(")")
	return out.String()
}

// Position gives the source location we use for diagnostics and runtime
// errors. calls point at the callee so the caret lands on the function name
// and not the opening paren
func Position(node Node) (line, column int) {
	var tok token.Token
	switch n := node.(type) {
	case *CallExpression:
		if member, ok := n.Function.(*MemberExpression); ok && member.Property != nil {
			return member.Property.Token.Line, member.Property.Token.Column
		}
		return Position(n.Function)
	case *AssignStatement:
		tok = n.Name.Token
	case *LetStatement:
		tok = n.Token
	case *ReturnStatement:
		tok = n.Token
	case *ImportStatement:
		tok = n.Token
	case *WhileStatement:
		tok = n.Token
	case *ForStatement:
		// errors from the loop itself are about the iterable
		return Position(n.Iterable)
	case *BreakStatement:
		tok = n.Token
	case *ContinueStatement:
		tok = n.Token
	case *ExpressionStatement:
		if n.Expression != nil {
			return Position(n.Expression)
		}
		tok = n.Token
	case *BlockStatement:
		tok = n.Token
	case *Identifier:
		tok = n.Token
	case *IntegerLiteral:
		tok = n.Token
	case *FloatLiteral:
		tok = n.Token
	case *StringLiteral:
		tok = n.Token
	case *Boolean:
		tok = n.Token
	case *NilLiteral:
		tok = n.Token
	case *PrefixExpression:
		tok = n.Token
	case *InfixExpression:
		tok = n.Token
	case *PipelineExpression:
		tok = n.Token
	case *IfExpression:
		tok = n.Token
	case *FunctionLiteral:
		tok = n.Token
	case *ArrayLiteral:
		tok = n.Token
	case *MapLiteral:
		tok = n.Token
	case *IndexExpression:
		tok = n.Token
	case *MemberExpression:
		tok = n.Token
	case *MatchExpression:
		tok = n.Token
	case *TryExpression:
		tok = n.Token
	case *ResultPattern:
		tok = n.Token
	case *TypeAnnotation:
		tok = n.Token
	case *WildcardPattern:
		tok = n.Token
	case *BindingPattern:
		tok = n.Token
	case *ValuePattern:
		return Position(n.Value)
	case *ArrayPattern:
		tok = n.Token
	case *MapPattern:
		tok = n.Token
	default:
		return 0, 0
	}
	return tok.Line, tok.Column
}
