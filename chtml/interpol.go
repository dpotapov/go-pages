package chtml

import (
	"fmt"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/compiler"
	"github.com/expr-lang/expr/conf"
	"github.com/expr-lang/expr/parser"
	"github.com/expr-lang/expr/vm"
)

const (
	eof        rune = -1
	leftDelim       = "${"
	rightDelim      = "}"
)

// Interpol converts a string with ${}-style placeholders to meta program.
// If the string is a simple text with no interpolation, it returns (nil, nil).
// If args is not nil, the expression engine will do type checking.
func Interpol(s string, args map[string]any) (*vm.Program, error) {
	l := &lexer{
		input: s,
		items: make([]item, 0),
	}

	for state := lexText; state != nil; {
		state = state(l)
	}

	if l.items[0].typ == itemError {
		return nil, fmt.Errorf(l.items[0].val)
	} else if l.items[0].typ == itemText && len(l.items) == 1 {
		return nil, nil
	}

	in := make([]ast.Node, 0, len(l.items))

	t := reflect.TypeOf(env(args))
	fns := make([]string, t.NumMethod())
	for i := 0; i < t.NumMethod(); i++ {
		fns[i] = t.Method(i).Name
	}

loop:
	for _, item := range l.items {
		switch item.typ {
		case itemError:
			return nil, fmt.Errorf(item.val)
		case itemEOF:
			break loop
		case itemText:
			in = append(in, &ast.StringNode{Value: item.val})
		case itemExpr:
			p, err := expr.Compile(item.val,
				expr.Env(env(args)),
				expr.Operator("+", fns...))
			if err != nil {
				return nil, err
			}
			in = append(in, p.Node())
		}
	}

	tree := &parser.Tree{
		Node: &ast.CallNode{
			Callee: &ast.IdentifierNode{
				Value: "combine",
			},
			Arguments: in,
		},
	}

	c := conf.CreateNew()

	opts := []expr.Option{
		expr.Env(env(args)),
		expr.Operator("+", fns...),
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

// Implementation of the lexer & interpolator based on https://go.dev/talks/2011/lex.slide

// lexer holds the state of the scanner.
type lexer struct {
	input       string // the string being scanned
	start       int    // start position of this item.
	pos         int    // current position in the input.
	width       int    // width of last rune read from input.
	bracesDepth int    // nesting depth of braces {}
	items       []item
}

// emit passes an item back to the client.
func (l *lexer) emit(t itemType) stateFn {
	l.items = append(l.items, item{t, l.input[l.start:l.pos]})
	l.start = l.pos
	return nil
}

// error returns an error token and terminates the scan
// by passing back a nil pointer that will be the next
// state, terminating l.run.
func (l *lexer) errorf(format string, args ...interface{}) stateFn {
	l.items = append(l.items, item{
		itemError,
		fmt.Sprintf(format, args...),
	})
	return nil
}

func (l *lexer) scanString(quote rune) {
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
func (l *lexer) next() (rune rune) {
	if l.pos >= len(l.input) {
		l.width = 0
		return eof
	}
	rune, l.width = utf8.DecodeRuneInString(l.input[l.pos:])
	l.pos += l.width
	return rune
}

// backup steps back one rune. Can be called only once per call of next.
func (l *lexer) backup() {
	l.pos -= l.width
}

// ignore skips over the pending input before this point.
func (l *lexer) ignore() {
	l.start = l.pos
}

// atRightDelim reports whether the lexer is at a right delimiter
func (l *lexer) atRightDelim() bool {
	return l.bracesDepth == 0 && strings.HasPrefix(l.input[l.pos:], rightDelim)
}

func lexText(l *lexer) stateFn {
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

func lexLeftDelim(l *lexer) stateFn {
	l.pos += len(leftDelim)
	l.ignore()
	return lexExpr // Now inside ${ }.
}

func lexRightDelim(l *lexer) stateFn {
	l.pos += len(rightDelim)
	l.ignore()
	return lexText
}

func lexExpr(l *lexer) stateFn {
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

func lexLoop(l *lexer) stateFn {
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

func lexLoopIdent(l *lexer) stateFn {
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

func lexLoopExpr(l *lexer) stateFn {
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
type stateFn func(*lexer) stateFn

// isSpace reports whether r is a space character.
func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\r' || r == '\n'
}

// isAlphaNumeric reports whether r is an alphabetic, digit, or underscore.
func isAlphaNumeric(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
