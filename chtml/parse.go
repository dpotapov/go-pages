// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// Modifications:
// Copyright 2024 Daniel Potapov
//  - Removed context-aware HTML parsing. The goal is to produce the Node tree as close to the
//    original source as possible, but honor some of the HTML5 parsing rules (e.g. self-closing
//    tags).
//  - Parse expressions in the HTML attributes and text nodes.

package chtml

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/expr-lang/expr/vm"
	"golang.org/x/net/html"
	a "golang.org/x/net/html/atom"
)

// A chtmlParser parses a CHTML document and builds a Node tree. It uses the tokenizer from the
// golang.org/x/net/html package to tokenize the input. There is no goal to parse in a way like the
// browser does, but to produce a Node tree as close to the original source as possible.
type chtmlParser struct {
	// tokenizer provides the tokens for the chtmlParser.
	tokenizer *html.Tokenizer
	// tok is the most recently read token.
	tok html.Token
	// Self-closing tags like <hr/> are treated as start tags, except that
	// hasSelfClosingToken is set while they are being processed.
	hasSelfClosingToken bool
	// doc is the document root element.
	doc *Node
	// env is the environment for evaluating expressions in the attributes and text nodes.
	env map[string]any
	// shadowed is the stack of variables shadowed by the elements that introduce new scopes.
	// When new variables are introduced (like in loops), the old values are preserved in the stack.
	shadowed []map[string]any
	// The stack of open elements (section 12.2.4.2).
	oe nodeStack
	// im is the current insertion mode.
	im insertionMode
	// originalIM is the insertion mode to go back to after completing a text
	// or inTableText insertion mode.
	originalIM insertionMode
	// importer resolves component imports in <c:IMPORT ...> tags.
	importer Importer
	// vm is the virtual machine for evaluating expressions.
	vm vm.VM
	// errs captures all errors encountered during parsing.
	errs []error
}

func (p *chtmlParser) top() *Node {
	if n := p.oe.top(); n != nil {
		return n
	}
	return p.doc
}

// Stop tags for use in popUntil. These come from section 12.2.4.2.
var (
	defaultScopeStopTags = map[string][]a.Atom{
		"":     {a.Applet, a.Caption, a.Html, a.Table, a.Td, a.Th, a.Marquee, a.Object, a.Template},
		"math": {a.AnnotationXml, a.Mi, a.Mn, a.Mo, a.Ms, a.Mtext},
		"svg":  {a.Desc, a.ForeignObject, a.Title},
	}
)

type envNoValueType int

const envNoValue envNoValueType = 0

type scope int

const (
	defaultScope scope = iota
	listItemScope
	buttonScope
	tableScope
	tableRowScope
	tableBodyScope
	selectScope
)

// popUntil pops the stack of open elements at the highest element whose tag
// is in matchTags, provided there is no higher element in the scope's stop
// tags (as defined in section 12.2.4.2). It returns whether or not there was
// such an element. If there was not, popUntil leaves the stack unchanged.
//
// For example, the set of stop tags for table scope is: "html", "table". If
// the stack was:
// ["html", "body", "font", "table", "b", "i", "u"]
// then popUntil(tableScope, "font") would return false, but
// popUntil(tableScope, "i") would return true and the stack would become:
// ["html", "body", "font", "table", "b"]
//
// If an element's tag is in both the stop tags and matchTags, then the stack
// will be popped and the function returns true (provided, of course, there was
// no higher element in the stack that was also in the stop tags). For example,
// popUntil(tableScope, "table") returns true and leaves:
// ["html", "body", "font"]
func (p *chtmlParser) popUntil(s scope, matchTags ...a.Atom) bool {
	if i := p.indexOfElementInScope(s, matchTags...); i != -1 {
		p.popElement()
		return true
	}
	return false
}

// indexOfElementInScope returns the index in p.oe of the highest element whose
// tag is in matchTags that is in scope. If no matching element is in scope, it
// returns -1.
func (p *chtmlParser) indexOfElementInScope(s scope, matchTags ...a.Atom) int {
	for i := len(p.oe) - 1; i >= 0; i-- {
		tagAtom := p.oe[i].DataAtom
		if p.oe[i].Namespace == "" {
			for _, t := range matchTags {
				if t == tagAtom {
					return i
				}
			}
			switch s {
			case defaultScope:
				// No-op.
			case listItemScope:
				if tagAtom == a.Ol || tagAtom == a.Ul {
					return -1
				}
			case buttonScope:
				if tagAtom == a.Button {
					return -1
				}
			case tableScope:
				if tagAtom == a.Html || tagAtom == a.Table || tagAtom == a.Template {
					return -1
				}
			case selectScope:
				if tagAtom != a.Optgroup && tagAtom != a.Option {
					return -1
				}
			default:
				panic("unreachable")
			}
		}
		switch s {
		case defaultScope, listItemScope, buttonScope:
			for _, t := range defaultScopeStopTags[p.oe[i].Namespace] {
				if t == tagAtom {
					return -1
				}
			}
		}
	}
	return -1
}

// elementInScope is like popUntil, except that it doesn't modify the stack of
// open elements.
func (p *chtmlParser) elementInScope(s scope, matchTags ...a.Atom) bool {
	return p.indexOfElementInScope(s, matchTags...) != -1
}

// parseGenericRawTextElement implements the generic raw text element parsing
// algorithm defined in 12.2.6.2.
// https://html.spec.whatwg.org/multipage/parsing.html#parsing-elements-that-contain-only-text
// TODO: Since both RAWTEXT and RCDATA states are treated as tokenizer's part
// officially, need to make tokenizer consider both states.
func (p *chtmlParser) parseGenericRawTextElement() {
	p.addElement()
	p.originalIM = p.im
	p.im = textIM
}

// generateImpliedEndTags pops nodes off the stack of open elements as long as
// the top node has a tag name of dd, dt, li, optgroup, option, p, rb, rp, rt or rtc.
// If exceptions are specified, nodes with that name will not be popped off.
func (p *chtmlParser) generateImpliedEndTags(exceptions ...string) {
	var i int
loop:
	for i = len(p.oe) - 1; i >= 0; i-- {
		n := p.oe[i]
		if n.Type != html.ElementNode {
			break
		}
		switch n.DataAtom {
		case a.Dd, a.Dt, a.Li, a.Optgroup, a.Option, a.P, a.Rb, a.Rp, a.Rt, a.Rtc:
			for _, except := range exceptions {
				if n.Data.RawString() == except {
					break loop
				}
			}
			continue
		}
		break
	}

	p.oe = p.oe[:i+1]
}

// addChild adds a child node n to the top element, and pushes n onto the stack
// of open elements if it is an element node.
func (p *chtmlParser) addChild(n *Node) {
	p.top().AppendChild(n)

	if n.Type == html.ElementNode || n.Type == importNode {
		p.oe = append(p.oe, n)
	}
}

// addText adds text to the preceding node if it is a text node, or else it
// calls addChild with a new text node.
func (p *chtmlParser) addText(text string) {
	if text == "" {
		return
	}

	t := p.top()
	if n := t.LastChild; n != nil && n.Type == html.TextNode {
		expr, err := NewExprInterpol(n.Data.RawString()+text, p.env)
		if err != nil {
			p.error(t, err)
		}
		n.Data = expr

		// Try to evaluate the expression to determine RenderShape
		if result, evalErr := expr.Value(&p.vm, env(p.env)); evalErr == nil {
			n.RenderShape = result
		}

		return
	}

	expr, err := NewExprInterpol(text, p.env)
	if err != nil {
		p.error(t, err)
	}

	textNode := &Node{
		Type: html.TextNode,
		Data: expr,
	}

	// Try to evaluate the expression to determine RenderShape
	if result, evalErr := expr.Value(&p.vm, env(p.env)); evalErr == nil {
		textNode.RenderShape = result
	}

	p.addChild(textNode)
}

// addElement adds a child element based on the current token.
func (p *chtmlParser) addElement() {
	n := &Node{
		Type:     html.ElementNode,
		DataAtom: p.tok.DataAtom,
		Data:     NewExprRaw(p.tok.Data),
		Attr:     make([]Attribute, 0, len(p.tok.Attr)),
	}
	n.RenderShape = n

	if strings.HasPrefix(strings.ToLower(p.tok.Data), "c:") {
		n.Type = importNode
	}

	var regularAttrs []html.Attribute
	for _, t := range p.tok.Attr {
		if ok := p.parseSpecialAttrs(n, &t); !ok {
			// Collect regular attributes to process later
			regularAttrs = append(regularAttrs, t)
		}
	}

	// Handle c:for variables *before* processing regular attributes
	if !n.Loop.IsEmpty() {
		introducedVars := make(map[string]any)
		if n.LoopVar != "" {
			introducedVars[n.LoopVar] = new(any) // TODO: infer type
		}
		if n.LoopIdx != "" {
			introducedVars[n.LoopIdx] = new(any) // TODO: infer type
		}
		// Push the new variables into the environment
		p.pushEnv(introducedVars)
	}

	// Now process regular attributes, loop variables are in env
	for _, t := range regularAttrs {
		expr, err := NewExprInterpol(t.Val, p.env)
		if err != nil {
			p.error(n, err)
			continue
		}

		n.Attr = append(n.Attr, Attribute{
			Namespace: t.Namespace,
			Key:       t.Key,
			Val:       expr,
		})
	}

	p.addChild(n)
}

// popElement will panic if the stack is empty.
func (p *chtmlParser) popElement() *Node {
	n := p.oe.pop()
	// If the element introduced variables, pop the environment
	if n.Type == html.ElementNode && !n.Loop.IsEmpty() {
		p.popEnv()
	}
	if n.Type == importNode {
		p.parseImportElement(n)
	}
	return n
}

func (p *chtmlParser) parseImportElement(n *Node) {
	compName := n.Data.RawString()[2:]
	if compName == "" {
		return
	}

	imp := p.importer

	if compName == "attr" {
		imp = &builtinImporter{}
	}

	if imp == nil {
		p.error(n, ErrImportNotAllowed)
		return
	}

	comp, err := imp.Import(compName)
	if err != nil {
		p.error(n, fmt.Errorf("import %q: %w", n.Data.RawString(), err))
		return
	}
	defer func() {
		if d, ok := comp.(Disposable); ok {
			if err := d.Dispose(); err != nil {
				p.error(n, fmt.Errorf("dispose import %s: %w", compName, err))
			}
		}
	}()

	// convert n.Attr to a map for the scope
	vars := make(map[string]any, len(n.Attr))
	for _, attr := range n.Attr {
		v, err := attr.Val.Value(&p.vm, env(p.env))
		if err != nil {
			p.error(n, fmt.Errorf("eval attr %q: %w", attr.Key, err))
			return
		}
		snake := toSnakeCase(attr.Key)
		vars[snake] = v
	}

	// Render the child content of the import element
	// Purpose: Renders child content and passes it as the "_" variable to the component.
	//
	// Example: <c:layout><p>This content</p></c:layout>
	//          The "<p>This content</p>" is rendered and passed to the layout component.
	if n.FirstChild != nil {
		c := &chtmlComponent{
			doc: &Node{
				Type:       html.DocumentNode,
				FirstChild: n.FirstChild,
			},
			env:            p.env,
			renderComments: true,
			importer:       p.importer,
			hidden:         make(map[*Node]struct{}),
			children:       make(map[*Node][]Component),
		}
		s := NewDryRunScope(nil)
		rr, err := c.Render(s)
		if err != nil {
			p.error(n, fmt.Errorf("render import %s: %w", compName, err))
			return
		}

		vars["_"] = rr
	}

	// Create a dry run scope for validation and rendering
	s := NewDryRunScope(vars)
	rr, err := comp.Render(s)
	if err != nil {
		p.error(n, fmt.Errorf("eval import %s: %w", compName, err))
		return
	}

	// Store the dry run result as the node's RenderShape for future reference
	n.RenderShape = rr

	if attr, ok := rr.(Attribute); ok && n.Parent != nil {
		// TODO: should we allow changing the attrribute of any element?
		if n.Parent == p.doc {
			n.Parent.Attr = append(n.Parent.Attr, attr)

			v, err := attr.Val.Value(&p.vm, env(p.env))
			if err != nil {
				p.error(n, fmt.Errorf("eval attr %q: %w", attr.Key, err))
				return
			}

			snake := toSnakeCase(attr.Key)
			p.env[snake] = v
			n.RenderShape = nil // the attribute does not render to anything
		}
	}
}

func (p *chtmlParser) parseSpecialAttrs(n *Node, t *html.Attribute) bool {
	switch fk := strings.ToLower(t.Key); fk {
	case "c:if", "c:else", "c:else-if":
		scond := t.Val
		if fk == "c:else" {
			scond = "true"
		}
		cond, err := NewExpr(scond, p.env)
		if err != nil {
			p.error(n, fmt.Errorf("parse condition: %w", err))
			n.Cond = NewExprConst(false) // fallback to false to prevent further errors
			return true
		}
		if fk != "c:if" {
			if prev := p.findPrevCond(p.top().LastChild); prev != nil {
				n.PrevCond = prev
				prev.NextCond = n
			} else {
				p.error(n, fmt.Errorf("%s without c:if", fk))
				return true
			}
		}
		n.Cond = cond
		return true
	case "c:for":
		v, k, expr, err := parseLoopExpr(t.Val)
		if err != nil {
			p.error(n, fmt.Errorf("parse loop expression: %w", err))
			return true
		}
		loop, err := NewExpr(expr, p.env)
		if err != nil {
			p.error(n, fmt.Errorf("parse loop expression: %w", err))
			return true
		}
		n.Loop = loop
		n.LoopIdx = k
		n.LoopVar = v
		return true
	default:
		return false
	}
}

func (p *chtmlParser) findPrevCond(n *Node) *Node {
	for ; n != nil; n = n.PrevSibling {
		if !n.Cond.IsEmpty() {
			return n
		}
	}
	return nil
}

// Section 12.2.5.
func (p *chtmlParser) acknowledgeSelfClosingTag() {
	p.hasSelfClosingToken = false
}

// An insertion mode (section 12.2.4.1) is the state transition function from
// a particular state in the HTML5 parser's state machine. It updates the
// parser's fields depending on chtmlParser.tok (where ErrorToken means EOF).
// It returns whether the token was consumed.
type insertionMode func(*chtmlParser) bool

// setOriginalIM sets the insertion mode to return to after completing a text or
// inTableText insertion mode.
// Section 12.2.4.1, "using the rules for".
func (p *chtmlParser) setOriginalIM() {
	if p.originalIM != nil {
		panic("html: bad parser state: originalIM was set twice")
	}
	p.originalIM = p.im
}

const whitespace = " \t\r\n\f"

func inBodyIM(p *chtmlParser) bool {
	switch p.tok.Type {
	case html.DoctypeToken:
		n := parseDoctype(p.tok.Data)
		p.addChild(n)
	case html.TextToken:
		d := p.tok.Data
		switch n := p.top(); n.DataAtom {
		case a.Pre, a.Listing:
			if n.FirstChild == nil {
				// Ignore a newline at the start of a <pre> block.
				if d != "" && d[0] == '\r' {
					d = d[1:]
				}
				if d != "" && d[0] == '\n' {
					d = d[1:]
				}
			}
		}
		d = strings.Replace(d, "\x00", "", -1)
		if d == "" {
			return true
		}
		p.addText(d)
	case html.StartTagToken:
		switch p.tok.DataAtom {
		case a.Base, a.Basefont, a.Bgsound, a.Link, a.Meta:
			p.addElement()
			p.popElement()
			p.acknowledgeSelfClosingTag()
			return true
		case a.Address, a.Article, a.Aside, a.Blockquote, a.Center, a.Details, a.Dialog, a.Dir, a.Div, a.Dl, a.Fieldset, a.Figcaption, a.Figure, a.Footer, a.Header, a.Hgroup, a.Main, a.Menu, a.Nav, a.Ol, a.P, a.Section, a.Summary, a.Ul:
			p.popUntil(buttonScope, a.P)
			p.addElement()
		case a.H1, a.H2, a.H3, a.H4, a.H5, a.H6:
			p.popUntil(buttonScope, a.P)
			switch n := p.top(); n.DataAtom {
			case a.H1, a.H2, a.H3, a.H4, a.H5, a.H6:
				p.popElement()
			}
			p.addElement()
		case a.Pre, a.Listing:
			p.popUntil(buttonScope, a.P)
			p.addElement()
		case a.Form:
			p.popUntil(buttonScope, a.P)
			p.addElement()
		case a.Li:
			for i := len(p.oe) - 1; i >= 0; i-- {
				node := p.oe[i]
				switch node.DataAtom {
				case a.Li:
					p.popElement()
				case a.Address, a.Div, a.P:
					continue
				default:
					if !isSpecialElement(node) {
						continue
					}
				}
				break
			}
			p.popUntil(buttonScope, a.P)
			p.addElement()
		case a.Dd, a.Dt:
			for i := len(p.oe) - 1; i >= 0; i-- {
				node := p.oe[i]
				switch node.DataAtom {
				case a.Dd, a.Dt:
					p.popElement()
				case a.Address, a.Div, a.P:
					continue
				default:
					if !isSpecialElement(node) {
						continue
					}
				}
				break
			}
			p.popUntil(buttonScope, a.P)
			p.addElement()
		case a.Plaintext:
			p.popUntil(buttonScope, a.P)
			p.addElement()
		case a.Button:
			p.popUntil(defaultScope, a.Button)
			p.addElement()
		case a.A:
			p.addElement()
		case a.B, a.Big, a.Code, a.Em, a.Font, a.I, a.S, a.Small, a.Strike, a.Strong, a.Tt, a.U:
			p.addElement()
		case a.Nobr:
			p.addElement()
		case a.Applet, a.Marquee, a.Object:
			p.addElement()
		case a.Table:
			p.popUntil(buttonScope, a.P)
			p.addElement()
		case a.Area, a.Br, a.Embed, a.Img, a.Input, a.Keygen, a.Wbr:
			p.addElement()
			p.popElement()
			p.acknowledgeSelfClosingTag()
			if p.tok.DataAtom == a.Input {
				for _, t := range p.tok.Attr {
					if t.Key == "type" {
						if strings.ToLower(t.Val) == "hidden" {
							// Skip setting framesetOK = false
							return true
						}
					}
				}
			}
		case a.Param, a.Source, a.Track:
			p.addElement()
			p.popElement()
			p.acknowledgeSelfClosingTag()
		case a.Hr:
			p.popUntil(buttonScope, a.P)
			p.addElement()
			p.popElement()
			p.acknowledgeSelfClosingTag()
		case a.Image:
			p.tok.DataAtom = a.Img
			p.tok.Data = a.Img.String()
			return false
		case a.Textarea:
			p.addElement()
			p.setOriginalIM()
			p.im = textIM
		case a.Xmp:
			p.popUntil(buttonScope, a.P)
			p.parseGenericRawTextElement()
		case a.Iframe:
			p.parseGenericRawTextElement()
		case a.Noembed:
			p.parseGenericRawTextElement()
		case a.Noscript:
			p.addElement()
			// Don't let the tokenizer go into raw text mode when for <noscript> tag and Parse
			// its content as regular HTML.
			p.tokenizer.NextIsNotRawText()
		case a.Optgroup, a.Option:
			if p.top().DataAtom == a.Option {
				p.popElement()
			}
			p.addElement()
		case a.Rb, a.Rtc:
			if p.elementInScope(defaultScope, a.Ruby) {
				p.generateImpliedEndTags()
			}
			p.addElement()
		case a.Rp, a.Rt:
			if p.elementInScope(defaultScope, a.Ruby) {
				p.generateImpliedEndTags("rtc")
			}
			p.addElement()
		case a.Math, a.Svg:
			p.addElement()
			p.top().Namespace = p.tok.Data
			if p.hasSelfClosingToken {
				p.popElement()
				p.acknowledgeSelfClosingTag()
			}
			return true
		default:
			p.addElement()
			if p.hasSelfClosingToken {
				p.popElement()
				p.acknowledgeSelfClosingTag()
			}
		}
	case html.EndTagToken:
		switch p.tok.DataAtom {
		/*case a.Body:
			if p.elementInScope(defaultScope, a.Body) {
				p.im = afterBodyIM
			}
		 case a.Html:
		if p.elementInScope(defaultScope, a.Body) {
			p.parseImpliedToken(html.EndTagToken, a.Body, a.Body.String())
			return false
		}
		return true
		*/
		case a.Address, a.Article, a.Aside, a.Blockquote, a.Button, a.Center, a.Details, a.Dialog, a.Dir, a.Div, a.Dl, a.Fieldset, a.Figcaption, a.Figure, a.Footer, a.Header, a.Hgroup, a.Listing, a.Main, a.Menu, a.Nav, a.Ol, a.Pre, a.Section, a.Summary, a.Ul:
			p.popUntil(defaultScope, p.tok.DataAtom)
		case a.Form:
			i := p.indexOfElementInScope(defaultScope, a.Form)
			if i == -1 {
				// Ignore the token.
				return true
			}
			p.generateImpliedEndTags()
			if p.oe[i].DataAtom != a.Form {
				// Ignore the token.
				return true
			}
			p.popUntil(defaultScope, a.Form)
		case a.P:
			// if !p.elementInScope(buttonScope, a.P) {
			// 	p.parseImpliedToken(html.StartTagToken, a.P, a.P.String())
			// }
			p.popUntil(buttonScope, a.P)
		case a.Li:
			p.popUntil(listItemScope, a.Li)
		case a.Dd, a.Dt:
			p.popUntil(defaultScope, p.tok.DataAtom)
		case a.H1, a.H2, a.H3, a.H4, a.H5, a.H6:
			p.popUntil(defaultScope, a.H1, a.H2, a.H3, a.H4, a.H5, a.H6)
		// case a.A, a.B, a.Big, a.Code, a.Em, a.Font, a.I, a.Nobr, a.S, a.Small, a.Strike, a.Strong, a.Tt, a.U:
		//	p.inBodyEndTagFormatting(p.tok.DataAtom, p.tok.Data)
		case a.Applet, a.Marquee, a.Object:
			p.popUntil(defaultScope, p.tok.DataAtom)
		case a.Br:
			p.tok.Type = html.StartTagToken
			return false
		default:
			p.inBodyEndTagOther(p.tok.DataAtom, p.tok.Data)
		}
	case html.CommentToken:
		expr, err := NewExprInterpol(p.tok.Data, p.env)
		n := &Node{
			Type: html.CommentNode,
			Data: expr,
		}
		if err != nil {
			p.error(n, err)
		} else {
			p.addChild(n)
		}
	case html.ErrorToken:
		// TODO: remove this divergence from the HTML5 spec.
		return true
		/*
			if len(p.templateStack) > 0 {
				p.im = inTemplateIM
				return false
			}
			for _, e := range p.oe {
				switch e.DataAtom {
				case a.Dd, a.Dt, a.Li, a.Optgroup, a.Option, a.P, a.Rb, a.Rp, a.Rt, a.Rtc, a.Tbody, a.Td, a.Tfoot, a.Th,
					a.Thead, a.Tr, a.Body, a.Html:
				default:
					return true
				}
			}

		*/
	}

	return true
}

// inBodyEndTagOther performs the "any other end tag" algorithm for inBodyIM.
// "Any other end tag" handling from 12.2.6.5 The rules for parsing tokens in foreign content
// https://html.spec.whatwg.org/multipage/syntax.html#parsing-main-inforeign
func (p *chtmlParser) inBodyEndTagOther(tagAtom a.Atom, tagName string) {
	for i := len(p.oe) - 1; i >= 0; i-- {
		// Two element nodes have the same tag if they have the same Data (a
		// string-typed field). As an optimization, for common HTML tags, each
		// Data string is assigned a unique, non-zero DataAtom (a uint32-typed
		// field), since integer comparison is faster than string comparison.
		// Uncommon (custom) tags get a zero DataAtom.
		//
		// The if condition here is equivalent to (p.oe[i].Data == tagName).
		if (p.oe[i].DataAtom == tagAtom) &&
			((tagAtom != 0) || (p.oe[i].Data.RawString() == tagName)) {
			p.popElement()
			break
		}
		if isSpecialElement(p.oe[i]) {
			break
		}
	}
}

// Section 12.2.6.4.8.
func textIM(p *chtmlParser) bool {
	switch p.tok.Type {
	case html.ErrorToken:
		p.popElement()
	case html.TextToken:
		d := p.tok.Data
		if n := p.oe.top(); n.DataAtom == a.Textarea && n.FirstChild == nil {
			// Ignore a newline at the start of a <textarea> block.
			if d != "" && d[0] == '\r' {
				d = d[1:]
			}
			if d != "" && d[0] == '\n' {
				d = d[1:]
			}
		}
		if d == "" {
			return true
		}
		p.addText(d)
		return true
	case html.EndTagToken:
		p.popElement()
	}
	p.im = p.originalIM
	p.originalIM = nil
	return p.tok.Type == html.EndTagToken
}

// parseCurrentToken runs the current token through the parsing routines
// until it is consumed.
func (p *chtmlParser) parseCurrentToken() {
	if p.tok.Type == html.SelfClosingTagToken {
		p.hasSelfClosingToken = true
		p.tok.Type = html.StartTagToken
	}

	consumed := false
	for !consumed { // TODO: refactor to avoid the loop.
		consumed = p.im(p)
	}

	if p.hasSelfClosingToken {
		// This is a Parse error, but ignore it.
		p.hasSelfClosingToken = false
	}
}

func (p *chtmlParser) error(n *Node, err error) {
	p.errs = append(p.errs, newComponentError(n, err))
}

// pushEnv adds variables to the parsing env while preserving the previous values in the shadowed
// stack.
func (p *chtmlParser) pushEnv(vars map[string]any) {
	m := make(map[string]any)
	p.shadowed = append(p.shadowed, m)
	for k, v := range vars {
		if oldV, ok := p.env[k]; ok {
			m[k] = oldV
		} else {
			m[k] = envNoValue
		}

		p.env[k] = v
	}
}

// popEnv applies the shadowed variables from the top of the stack to the env.
func (p *chtmlParser) popEnv() {
	for k, v := range p.shadowed[len(p.shadowed)-1] {
		if v == envNoValue {
			delete(p.env, k)
		} else {
			p.env[k] = v
		}
	}
	p.shadowed = p.shadowed[:len(p.shadowed)-1]
}

func (p *chtmlParser) parse() error {
	// Iterate until EOF. Any other error will cause an early return.
	var err error
	for err != io.EOF {
		// CDATA sections are allowed only in foreign content.
		n := p.oe.top()
		p.tokenizer.AllowCDATA(n != nil && n.Namespace != "")
		// Read and parse the next token.
		p.tokenizer.Next()
		p.tok = p.tokenizer.Token()
		if p.tok.Type == html.ErrorToken {
			err = p.tokenizer.Err()
			if err != nil && err != io.EOF {
				return err
			}
		}
		p.parseCurrentToken()
	}

	return nil
}

// Parse returns the parsed *Node tree for the HTML from the given Reader.
// The input is assumed to be UTF-8 encoded.
func Parse(r io.Reader, imp Importer) (*Node, error) {
	p := &chtmlParser{
		tokenizer: html.NewTokenizer(r),
		doc: &Node{
			Type: html.DocumentNode,
		},
		env:      map[string]any{"_": new(any)},
		im:       inBodyIM,
		importer: imp,
	}

	if err := p.parse(); err != nil {
		return nil, err
	}
	return p.doc, errors.Join(p.errs...)
}
