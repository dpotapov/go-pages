package chtml

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/compiler"
	"github.com/expr-lang/expr/conf"
	expr_parser "github.com/expr-lang/expr/parser"
	"github.com/expr-lang/expr/vm"
)

const (
	eof        rune = -1
	leftDelim       = "${"
	rightDelim      = "}"
)

// Expr is a struct to hold interpolated string data for the CHTML nodes.
type Expr struct {
	raw       string
	expr      *vm.Program
	shape     *Shape
	exprSpans []ExprSpan // Spans of ${...} expressions within the raw string

	// isMatchCond indicates this is a type-matching condition ("EXPR is SHAPE").
	// When true, the condition evaluates to true only if the value matches shape.
	isMatchCond bool

	// bindVar is the variable name to bind the matched value to (for "as IDENT" syntax).
	// Empty string means no variable binding.
	bindVar string
}

// ExprSpan captures the location of an expression within a string
type ExprSpan struct {
	Start  int // Start offset within the parent string
	Length int // Length of the expression (excluding ${})
}

// exprOptions returns the standard options for compiling expressions.
func exprOptions() []expr.Option {
	return []expr.Option{
		expr.DisableBuiltin("type"),
		expr.DisableBuiltin("duration"),
		expr.Function("cast", CastFunction),
		expr.Function("type", TypeFunction),
		expr.Function("duration", DurationFunction),
		expr.Function("formatDuration", FormatDurationFunction),
	}
}

func NewExpr(s string, syms Symbols) (Expr, error) {
	if s == "" {
		return Expr{}, nil
	}
	x := Expr{raw: s}

	// Parse the expression
	tree, err := expr_parser.Parse(s)
	if err != nil {
		return x, err
	}

	// Transform cast() shape literals to *Shape constants for runtime validation
	transformCastShapes(tree.Node)

	// Compile the transformed AST
	prog, err := compileTransformed(tree.Node, exprOptions()...)
	if err != nil {
		return x, err
	}
	x.expr = prog

	// Type check the transformed AST
	shape, err := Check(tree.Node, syms)
	if err != nil {
		return x, err
	}
	x.shape = shape
	return x, nil
}

// transformCastShapes mutates the AST, replacing shape literals in cast() calls
// with *Shape constants so they're available at runtime for validation.
func transformCastShapes(node ast.Node) {
	switch n := node.(type) {
	case *ast.CallNode:
		for _, arg := range n.Arguments {
			transformCastShapes(arg)
		}
		if id, ok := n.Callee.(*ast.IdentifierNode); ok && id.Value == "cast" && len(n.Arguments) >= 2 {
			if shape, ok := shapeLiteralFromAST(n.Arguments[1]); ok {
				n.Arguments[1] = &ast.ConstantNode{Value: shape}
			}
		}
	case *ast.ArrayNode:
		for _, elem := range n.Nodes {
			transformCastShapes(elem)
		}
	case *ast.MapNode:
		for _, pair := range n.Pairs {
			if p, ok := pair.(*ast.PairNode); ok {
				transformCastShapes(p.Key)
				transformCastShapes(p.Value)
			}
		}
	case *ast.BinaryNode:
		transformCastShapes(n.Left)
		transformCastShapes(n.Right)
	case *ast.UnaryNode:
		transformCastShapes(n.Node)
	case *ast.ConditionalNode:
		transformCastShapes(n.Cond)
		transformCastShapes(n.Exp1)
		transformCastShapes(n.Exp2)
	case *ast.MemberNode:
		transformCastShapes(n.Node)
	case *ast.SliceNode:
		transformCastShapes(n.Node)
		if n.From != nil {
			transformCastShapes(n.From)
		}
		if n.To != nil {
			transformCastShapes(n.To)
		}
	}
}

// compileTransformed compiles a transformed AST node.
func compileTransformed(node ast.Node, opts ...expr.Option) (*vm.Program, error) {
	tree := &expr_parser.Tree{Node: node}
	c := conf.CreateNew()
	for _, opt := range opts {
		opt(c)
	}
	return compiler.Compile(tree, c)
}

func NewExprInterpol(s string, syms Symbols) (Expr, error) {
	if s == "" {
		return Expr{}, nil
	}
	prog, spans, err := interpolWithSpans(s)
	if err != nil {
		return Expr{}, err
	}
	var shp *Shape
	if prog != nil {
		if shp, err = Check(prog.Node(), syms); err != nil {
			return Expr{}, err
		}
	} else {
		// No interpolation → plain text
		shp = String
	}
	return Expr{raw: s, expr: prog, shape: shp, exprSpans: spans}, nil
}

// NewExprRaw creates an Expr with a raw string, no interpolation.
func NewExprRaw(s string) Expr {
	return Expr{
		raw:   s,
		shape: String,
	}
}

func NewExprConst(v any) Expr {
	return Expr{
		raw: fmt.Sprintf("%v", v),
		expr: &vm.Program{
			Constants: []any{v},
			Bytecode:  []vm.Opcode{vm.OpPush},
			Arguments: []int{0},
		},
		shape: ShapeFrom(v),
	}
}

func (e Expr) Value(vm *vm.VM, env any) (any, error) {
	if e.expr != nil {
		return vm.Run(e.expr, env)
	}
	return e.raw, nil
}

func (e Expr) RawString() string {
	return e.raw
}

func (e Expr) IsEmpty() bool {
	return e.expr == nil && e.raw == ""
}

// Shape returns the statically-derived output shape for this expression.
func (e Expr) Shape() *Shape { return e.shape }

// interpolWithSpans converts a string with ${}-style placeholders to meta program,
// also returning the spans of each expression within the string.
func interpolWithSpans(s string) (*vm.Program, []ExprSpan, error) {
	l := &exprLexer{
		input: s,
		items: make([]item, 0),
	}

	for state := lexText; state != nil; {
		state = state(l)
	}

	if len(l.items) > 0 && l.items[0].typ == itemError {
		return nil, nil, fmt.Errorf("%s", l.items[0].val)
	} else if len(l.items) == 1 && l.items[0].typ == itemText {
		return nil, nil, nil
	}

	in := make([]ast.Node, 0, len(l.items))
	spans := make([]ExprSpan, 0)

loop:
	for _, item := range l.items {
		switch item.typ {
		case itemError:
			return nil, nil, fmt.Errorf("%s", item.val)
		case itemEOF:
			break loop
		case itemText:
			in = append(in, &ast.StringNode{Value: item.val})
		case itemExpr:
			// Capture the span of this expression
			spans = append(spans, ExprSpan{
				Start:  item.start,
				Length: item.length,
			})

			// Parse the expression and transform cast() calls
			tree, err := expr_parser.Parse(item.val)
			if err != nil {
				return nil, nil, err
			}
			transformCastShapes(tree.Node)
			in = append(in, tree.Node)
		}
	}

	tree := &expr_parser.Tree{
		Node: &ast.CallNode{
			Callee: &ast.IdentifierNode{
				Value: "combine",
			},
			Arguments: in,
		},
	}

	c := conf.CreateNew()
	opts := append(exprOptions(),
		expr.Function("combine", func(args ...any) (any, error) {
			var acc any
			for _, arg := range args {
				acc = AnyPlusAny(acc, arg)
			}
			return acc, nil
		}),
	)
	for _, opt := range opts {
		opt(c)
	}

	prog, err := compiler.Compile(tree, c)
	return prog, spans, err
}

// interpol converts a string with ${}-style placeholders to meta program.
// If the string is a simple text with no interpolation, it returns (nil, nil).
// If args is not nil, the expression engine will do type checking.
func interpol(s string) (*vm.Program, error) {
	prog, _, err := interpolWithSpans(s)
	return prog, err
}

// NewExprCond parses a condition expression that may contain "is SHAPE as IDENT" syntax.
// This extends normal expression parsing to support type matching for conditionals.
// If the condition doesn't use the "is" syntax, it behaves like NewExpr.
func NewExprCond(s string, syms Symbols) (Expr, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Expr{}, nil
	}

	// Look for " is " keyword (with spaces to avoid matching inside identifiers like "isActive")
	exprPart, matchShape, bindVar, err := splitCondMatchParts(s)
	if err != nil {
		return Expr{}, err
	}

	// Parse the expression part using standard NewExpr
	x, err := NewExpr(exprPart, syms)
	if err != nil {
		return Expr{}, err
	}

	// For type matching, override the shape with the match shape
	if matchShape != nil {
		x.shape = matchShape
		x.isMatchCond = true
	}
	x.bindVar = bindVar

	// Keep the original raw string for debugging/display
	x.raw = s

	return x, nil
}

// IsMatchCond returns true if this expression has type matching ("is SHAPE" syntax).
func (e Expr) IsMatchCond() bool {
	return e.isMatchCond
}

// BindVar returns the variable name to bind the matched value to (for "as IDENT" syntax).
func (e Expr) BindVar() string {
	return e.bindVar
}

// splitCondMatchParts splits a condition string into expression, shape, and bind variable.
// Returns: (exprPart, matchShape, bindVar, error)
// If no "is" syntax is found, shape and bindVar will be nil/empty.
func splitCondMatchParts(s string) (exprPart string, matchShape *Shape, bindVar string, err error) {
	// We need to find " is " that's not inside a string literal or parentheses
	isIdx := findCondMatchKeyword(s, " is ")
	if isIdx == -1 {
		// No "is" keyword, just a regular expression
		return s, nil, "", nil
	}

	exprPart = strings.TrimSpace(s[:isIdx])
	remainder := strings.TrimSpace(s[isIdx+4:]) // Skip " is "

	// Now look for " as " in the remainder
	asIdx := findCondMatchKeyword(remainder, " as ")
	var shapePart string
	if asIdx != -1 {
		shapePart = strings.TrimSpace(remainder[:asIdx])
		bindVar = strings.TrimSpace(remainder[asIdx+4:]) // Skip " as "

		// Validate bindVar is a valid identifier
		if !isValidCondMatchIdent(bindVar) {
			return "", nil, "", fmt.Errorf("invalid identifier after 'as': %q", bindVar)
		}
	} else {
		// No explicit "as IDENT", use expression as variable name if it's a simple identifier
		shapePart = remainder
		if isValidCondMatchIdent(exprPart) {
			bindVar = exprPart
		}
	}

	// Parse the shape
	shape, err := parseShapeLiteral(shapePart)
	if err != nil {
		return "", nil, "", fmt.Errorf("invalid shape in condition: %w", err)
	}

	return exprPart, shape, bindVar, nil
}

// findCondMatchKeyword finds the position of a keyword in a string, respecting
// string literals and nested braces/parentheses.
func findCondMatchKeyword(s, keyword string) int {
	depth := 0
	inString := false
	var stringChar rune

	for i := 0; i < len(s); i++ {
		r := rune(s[i])

		if inString {
			if r == '\\' && i+1 < len(s) {
				i++ // Skip escaped character
				continue
			}
			if r == stringChar {
				inString = false
			}
			continue
		}

		switch r {
		case '"', '\'', '`':
			inString = true
			stringChar = r
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		default:
			// Only match keyword at depth 0 and outside strings
			if depth == 0 && strings.HasPrefix(s[i:], keyword) {
				// Make sure we're at a word boundary (spaces around the keyword)
				return i
			}
		}
	}
	return -1
}

// isValidCondMatchIdent checks if a string is a valid identifier for binding.
func isValidCondMatchIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if !isLetter(r) && r != '_' {
				return false
			}
		} else {
			if !isLetter(r) && !isDigit(r) && r != '_' {
				return false
			}
		}
	}
	return true
}

// parseShapeLiteral parses a shape literal string using the expr-lang parser.
func parseShapeLiteral(s string) (*Shape, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty shape")
	}

	tree, err := expr_parser.Parse(s)
	if err != nil {
		return nil, fmt.Errorf("parse shape: %w", err)
	}

	shape, ok := shapeLiteralFromAST(tree.Node)
	if !ok {
		return nil, fmt.Errorf("invalid shape literal: %s", s)
	}

	return shape, nil
}

func parseLoopExpr(s string) (v, k, expr string, err error) {
	l := &exprLexer{
		input: s,
		items: make([]item, 0),
	}

	for state := lexLoop; state != nil; {
		state = state(l)
	}

	idents := make([]string, 0, 3)

	for _, item := range l.items {
		if item.typ == itemError {
			return "", "", "", errors.New(item.val)
		}
		if item.typ == itemEOF {
			break
		}
		if item.typ == itemLoopIdent {
			idents = append(idents, item.val)
		}
		if item.typ == itemExpr {
			expr = item.val
		}
	}

	switch len(idents) {
	case 0:
		return "", "", "", errors.New("missing loop variable")
	case 1:
		return idents[0], "", expr, nil
	case 2:
		return idents[0], idents[1], expr, nil
	default:
		return "", "", "", errors.New("too many loop variables")
	}
}

// Implementation of the lexer & interpolator based on https://go.dev/talks/2011/lex.slide

// exprLexer holds the state of the scanner.
type exprLexer struct {
	input       string // the string being scanned
	start       int    // start position of this item.
	pos         int    // current position in the input.
	width       int    // width of last rune read from input.
	bracesDepth int    // nesting depth of braces {}
	items       []item
}

// emit passes an item back to the client.
func (l *exprLexer) emit(t itemType) stateFn {
	l.items = append(l.items, item{
		typ:    t,
		val:    l.input[l.start:l.pos],
		start:  l.start,
		length: l.pos - l.start,
	})
	l.start = l.pos
	return nil
}

// error returns an error token and terminates the scan
// by passing back a nil pointer that will be the next
// state, terminating l.run.
func (l *exprLexer) errorf(format string, args ...interface{}) stateFn {
	l.items = append(l.items, item{
		typ:    itemError,
		val:    fmt.Sprintf(format, args...),
		start:  l.start,
		length: l.pos - l.start,
	})
	return nil
}

func (l *exprLexer) scanString(quote rune) {
	for ch := l.next(); ch != quote; ch = l.next() {
		if ch == '\n' || ch == eof {
			l.errorf("unterminated string")
			return
		}
		if ch == '\\' {
			l.next()
		}
	}
}

// next returns the next rune in the input.
func (l *exprLexer) next() (rune rune) {
	if l.pos >= len(l.input) {
		l.width = 0
		return eof
	}
	rune, l.width = utf8.DecodeRuneInString(l.input[l.pos:])
	l.pos += l.width
	return rune
}

// backup steps back one rune. Can be called only once per call of next.
func (l *exprLexer) backup() {
	l.pos -= l.width
}

// ignore skips over the pending input before this point.
func (l *exprLexer) ignore() {
	l.start = l.pos
}

// atRightDelim reports whether the exprLexer is at a right delimiter
func (l *exprLexer) atRightDelim() bool {
	return l.bracesDepth == 0 && strings.HasPrefix(l.input[l.pos:], rightDelim)
}

func lexText(l *exprLexer) stateFn {
	if x := strings.Index(l.input[l.pos:], leftDelim); x >= 0 {
		if x > 0 {
			l.pos += x
			l.emit(itemText)
		}
		return lexLeftDelim
	}
	l.pos = len(l.input)
	// Correctly reached EOF.
	if l.pos > l.start {
		l.emit(itemText)
	}
	return l.emit(itemEOF)
}

func lexLeftDelim(l *exprLexer) stateFn {
	l.pos += len(leftDelim)
	l.ignore()
	return lexExpr // Now inside ${ }.
}

func lexRightDelim(l *exprLexer) stateFn {
	l.pos += len(rightDelim)
	l.ignore()
	return lexText
}

func lexExpr(l *exprLexer) stateFn {
	if l.atRightDelim() {
		l.emit(itemExpr)
		return lexRightDelim
	}
	switch r := l.next(); r {
	case eof:
		return l.errorf("unclosed action")
	case '\'', '"':
		l.scanString(r)
	case '{':
		l.bracesDepth++
	case '}':
		l.bracesDepth--
	}
	return lexExpr
}

// /////////////
// for-loop parsing states

func lexLoop(l *exprLexer) stateFn {
	for {
		switch r := l.next(); {
		case r == eof:
			return l.errorf("missing loop body")
		case isSpace(r): // ignore spaces
			l.ignore()
		case isAlphaNumeric(r):
			l.backup()
			return lexLoopIdent
		case r == ',':
			l.ignore()
		default:
			return l.errorf("bad character %#U", r)
		}
	}
}

func lexLoopIdent(l *exprLexer) stateFn {
	for {
		switch r := l.next(); {
		case isAlphaNumeric(r):
			// absorb
		default:
			l.backup()
			word := l.input[l.start:l.pos]
			if word == "in" {
				l.ignore()
				return lexLoopExpr
			}
			l.emit(itemLoopIdent)
			return lexLoop
		}
	}
}

func lexLoopExpr(l *exprLexer) stateFn {
	for r := l.next(); isSpace(r); r = l.next() {
		l.ignore()
	}
	l.backup()
	l.pos = len(l.input)
	if l.pos > l.start {
		l.emit(itemExpr)
	}
	return l.emit(itemEOF)
}

type itemType int

const (
	itemError itemType = iota
	itemEOF
	itemText
	itemExpr

	// special identifiers that can be emitted in loop parsing mode:
	itemLoopIdent
)

type item struct {
	typ    itemType
	val    string
	start  int // Byte offset where this item starts
	length int // Length in bytes
}

// stateFn represents the state of the scanner
// as a function that returns the next state.
type stateFn func(*exprLexer) stateFn

// isSpace reports whether r is a space character.
func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\r' || r == '\n'
}

// isAlphaNumeric reports whether r is an alphabetic, digit, or underscore.
func isAlphaNumeric(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
