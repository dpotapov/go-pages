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
	raw   string
	expr  *vm.Program
	shape *Shape
}

func NewExpr(s string, syms Symbols) (Expr, error) {
	if s == "" {
		return Expr{}, nil
	}
	x := Expr{raw: s}
	// Parse with type-check disabled, then run our checker, then keep the compiled program.
	prog, err := expr.Compile(s,
		expr.DisableBuiltin("type"),
		expr.DisableBuiltin("duration"),
		expr.Function("cast", CastFunction),
		expr.Function("type", TypeFunction),
		expr.Function("duration", DurationFunction))
	if err != nil {
		return x, err
	}
	x.expr = prog
	shape, err := Check(prog.Node(), syms)
	if err != nil {
		return x, err
	}
	x.shape = shape
	return x, nil
}

func NewExprInterpol(s string, syms Symbols) (Expr, error) {
	if s == "" {
		return Expr{}, nil
	}
	prog, err := interpol(s)
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
	return Expr{raw: s, expr: prog, shape: shp}, nil
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

// interpol converts a string with ${}-style placeholders to meta program.
// If the string is a simple text with no interpolation, it returns (nil, nil).
// If args is not nil, the expression engine will do type checking.
func interpol(s string) (*vm.Program, error) {
	l := &exprLexer{
		input: s,
		items: make([]item, 0),
	}

	for state := lexText; state != nil; {
		state = state(l)
	}

	if l.items[0].typ == itemError {
		return nil, fmt.Errorf("%s", l.items[0].val)
	} else if l.items[0].typ == itemText && len(l.items) == 1 {
		return nil, nil
	}

	in := make([]ast.Node, 0, len(l.items))

loop:
	for _, item := range l.items {
		switch item.typ {
		case itemError:
			return nil, fmt.Errorf("%s", item.val)
		case itemEOF:
			break loop
		case itemText:
			in = append(in, &ast.StringNode{Value: item.val})
		case itemExpr:
			p, err := expr.Compile(item.val,
				expr.DisableBuiltin("type"),
				expr.DisableBuiltin("duration"),
				expr.Function("cast", CastFunction),
				expr.Function("type", TypeFunction),
				expr.Function("duration", DurationFunction))
			if err != nil {
				return nil, err
			}
			in = append(in, p.Node())
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
	opts := []expr.Option{
		expr.DisableBuiltin("type"),
		expr.DisableBuiltin("duration"),
		expr.Function("cast", CastFunction),
		expr.Function("type", TypeFunction),
		expr.Function("duration", DurationFunction),
		expr.Function("combine", func(args ...any) (any, error) {
			var acc any
			for _, arg := range args {
				acc = AnyPlusAny(acc, arg)
			}
			return acc, nil
		}),
	}
	for _, opt := range opts {
		opt(c)
	}
	return compiler.Compile(tree, c)

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
	l.items = append(l.items, item{t, l.input[l.start:l.pos]})
	l.start = l.pos
	return nil
}

// error returns an error token and terminates the scan
// by passing back a nil pointer that will be the next
// state, terminating l.run.
func (l *exprLexer) errorf(format string, args ...interface{}) stateFn {
	l.items = append(l.items, item{
		itemError,
		fmt.Sprintf(format, args...),
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
	switch r := l.next(); {
	case r == eof:
		return l.errorf("unclosed action")
	case r == '\'' || r == '"':
		l.scanString(r)
	case r == '{':
		l.bracesDepth++
	case r == '}':
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
	typ itemType
	val string
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
