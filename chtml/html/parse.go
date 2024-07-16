// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package html

import (
	"io"
	"strings"

	"golang.org/x/net/html"
	a "golang.org/x/net/html/atom"
)

// A parser implements the HTML5 parsing algorithm:
// https://html.spec.whatwg.org/multipage/syntax.html#tree-construction
type parser struct {
	// tokenizer provides the tokens for the parser.
	tokenizer *html.Tokenizer
	// tok is the most recently read token.
	tok html.Token
	// Self-closing tags like <hr/> are treated as start tags, except that
	// hasSelfClosingToken is set while they are being processed.
	hasSelfClosingToken bool
	// doc is the document root element.
	doc *html.Node
	// The stack of open elements (section 12.2.4.2) and active formatting
	// elements (section 12.2.4.3).
	oe, afe nodeStack
	// Element pointers (section 12.2.4.4).
	form *html.Node
	// im is the current insertion mode.
	im insertionMode
	// originalIM is the insertion mode to go back to after completing a text
	// or inTableText insertion mode.
	originalIM insertionMode
	// fosterParenting is whether new elements should be inserted according to
	// the foster parenting rules (section 12.2.6.1).
	fosterParenting bool
}

func (p *parser) top() *html.Node {
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
func (p *parser) popUntil(s scope, matchTags ...a.Atom) bool {
	if i := p.indexOfElementInScope(s, matchTags...); i != -1 {
		p.oe = p.oe[:i]
		return true
	}
	return false
}

// indexOfElementInScope returns the index in p.oe of the highest element whose
// tag is in matchTags that is in scope. If no matching element is in scope, it
// returns -1.
func (p *parser) indexOfElementInScope(s scope, matchTags ...a.Atom) int {
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
func (p *parser) elementInScope(s scope, matchTags ...a.Atom) bool {
	return p.indexOfElementInScope(s, matchTags...) != -1
}

// parseGenericRawTextElement implements the generic raw text element parsing
// algorithm defined in 12.2.6.2.
// https://html.spec.whatwg.org/multipage/parsing.html#parsing-elements-that-contain-only-text
// TODO: Since both RAWTEXT and RCDATA states are treated as tokenizer's part
// officially, need to make tokenizer consider both states.
func (p *parser) parseGenericRawTextElement() {
	p.addElement()
	p.originalIM = p.im
	p.im = textIM
}

// generateImpliedEndTags pops nodes off the stack of open elements as long as
// the top node has a tag name of dd, dt, li, optgroup, option, p, rb, rp, rt or rtc.
// If exceptions are specified, nodes with that name will not be popped off.
func (p *parser) generateImpliedEndTags(exceptions ...string) {
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
				if n.Data == except {
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
func (p *parser) addChild(n *html.Node) {
	if p.shouldFosterParent() {
		p.fosterParent(n)
	} else {
		p.top().AppendChild(n)
	}

	if n.Type == html.ElementNode {
		p.oe = append(p.oe, n)
	}
}

// shouldFosterParent returns whether the next node to be added should be
// foster parented.
func (p *parser) shouldFosterParent() bool {
	if p.fosterParenting {
		switch p.top().DataAtom {
		case a.Table, a.Tbody, a.Tfoot, a.Thead, a.Tr:
			return true
		}
	}
	return false
}

// fosterParent adds a child node according to the foster parenting rules.
// Section 12.2.6.1, "foster parenting".
func (p *parser) fosterParent(n *html.Node) {
	var table, parent, prev, template *html.Node
	var i int
	for i = len(p.oe) - 1; i >= 0; i-- {
		if p.oe[i].DataAtom == a.Table {
			table = p.oe[i]
			break
		}
	}

	var j int
	for j = len(p.oe) - 1; j >= 0; j-- {
		if p.oe[j].DataAtom == a.Template {
			template = p.oe[j]
			break
		}
	}

	if template != nil && (table == nil || j > i) {
		template.AppendChild(n)
		return
	}

	if table == nil {
		// The foster parent is the html element.
		parent = p.oe[0]
	} else {
		parent = table.Parent
	}
	if parent == nil {
		parent = p.oe[i-1]
	}

	if table != nil {
		prev = table.PrevSibling
	} else {
		prev = parent.LastChild
	}
	if prev != nil && prev.Type == html.TextNode && n.Type == html.TextNode {
		prev.Data += n.Data
		return
	}

	parent.InsertBefore(n, table)
}

// addText adds text to the preceding node if it is a text node, or else it
// calls addChild with a new text node.
func (p *parser) addText(text string) {
	if text == "" {
		return
	}

	if p.shouldFosterParent() {
		p.fosterParent(&html.Node{
			Type: html.TextNode,
			Data: text,
		})
		return
	}

	t := p.top()
	if n := t.LastChild; n != nil && n.Type == html.TextNode {
		n.Data += text
		return
	}
	p.addChild(&html.Node{
		Type: html.TextNode,
		Data: text,
	})
}

// addElement adds a child element based on the current token.
func (p *parser) addElement() {
	p.addChild(&html.Node{
		Type:     html.ElementNode,
		DataAtom: p.tok.DataAtom,
		Data:     p.tok.Data,
		Attr:     p.tok.Attr,
	})
}

// Section 12.2.4.3.
func (p *parser) addFormattingElement() {
	tagAtom, attr := p.tok.DataAtom, p.tok.Attr
	p.addElement()

	// Implement the Noah's Ark clause, but with three per family instead of two.
	identicalElements := 0
findIdenticalElements:
	for i := len(p.afe) - 1; i >= 0; i-- {
		n := p.afe[i]
		if n.Type == scopeMarkerNode {
			break
		}
		if n.Type != html.ElementNode {
			continue
		}
		if n.Namespace != "" {
			continue
		}
		if n.DataAtom != tagAtom {
			continue
		}
		if len(n.Attr) != len(attr) {
			continue
		}
	compareAttributes:
		for _, t0 := range n.Attr {
			for _, t1 := range attr {
				if t0.Key == t1.Key && t0.Namespace == t1.Namespace && t0.Val == t1.Val {
					// Found a match for this attribute, continue with the next attribute.
					continue compareAttributes
				}
			}
			// If we get here, there is no attribute that matches a.
			// Therefore the element is not identical to the new one.
			continue findIdenticalElements
		}

		identicalElements++
		if identicalElements >= 3 {
			p.afe.remove(n)
		}
	}

	p.afe = append(p.afe, p.top())
}

// Section 12.2.4.3.
func (p *parser) clearActiveFormattingElements() {
	for {
		if n := p.afe.pop(); len(p.afe) == 0 || n.Type == scopeMarkerNode {
			return
		}
	}
}

// Section 12.2.4.3.
func (p *parser) reconstructActiveFormattingElements() {
	n := p.afe.top()
	if n == nil {
		return
	}
	if n.Type == scopeMarkerNode || p.oe.index(n) != -1 {
		return
	}
	i := len(p.afe) - 1
	for n.Type != scopeMarkerNode && p.oe.index(n) == -1 {
		if i == 0 {
			i = -1
			break
		}
		i--
		n = p.afe[i]
	}
	for {
		i++
		clone := cloneNode(p.afe[i])
		p.addChild(clone)
		p.afe[i] = clone
		if i == len(p.afe)-1 {
			break
		}
	}
}

// Section 12.2.5.
func (p *parser) acknowledgeSelfClosingTag() {
	p.hasSelfClosingToken = false
}

// An insertion mode (section 12.2.4.1) is the state transition function from
// a particular state in the HTML5 parser's state machine. It updates the
// parser's fields depending on parser.tok (where ErrorToken means EOF).
// It returns whether the token was consumed.
type insertionMode func(*parser) bool

// setOriginalIM sets the insertion mode to return to after completing a text or
// inTableText insertion mode.
// Section 12.2.4.1, "using the rules for".
func (p *parser) setOriginalIM() {
	if p.originalIM != nil {
		panic("html: bad parser state: originalIM was set twice")
	}
	p.originalIM = p.im
}

const whitespace = " \t\r\n\f"

func inBodyIM(p *parser) bool {
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
		p.reconstructActiveFormattingElements()
		p.addText(d)
	case html.StartTagToken:
		switch p.tok.DataAtom {
		case a.Address, a.Article, a.Aside, a.Blockquote, a.Center, a.Details, a.Dialog, a.Dir, a.Div, a.Dl, a.Fieldset, a.Figcaption, a.Figure, a.Footer, a.Header, a.Hgroup, a.Main, a.Menu, a.Nav, a.Ol, a.P, a.Section, a.Summary, a.Ul:
			p.popUntil(buttonScope, a.P)
			p.addElement()
		case a.H1, a.H2, a.H3, a.H4, a.H5, a.H6:
			p.popUntil(buttonScope, a.P)
			switch n := p.top(); n.DataAtom {
			case a.H1, a.H2, a.H3, a.H4, a.H5, a.H6:
				p.oe.pop()
			}
			p.addElement()
		case a.Pre, a.Listing:
			p.popUntil(buttonScope, a.P)
			p.addElement()
		case a.Form:
			if p.form != nil && !p.oe.contains(a.Template) {
				// Ignore the token
				return true
			}
			p.popUntil(buttonScope, a.P)
			p.addElement()
			if !p.oe.contains(a.Template) {
				p.form = p.top()
			}
		case a.Li:
			for i := len(p.oe) - 1; i >= 0; i-- {
				node := p.oe[i]
				switch node.DataAtom {
				case a.Li:
					p.oe = p.oe[:i]
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
					p.oe = p.oe[:i]
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
			p.reconstructActiveFormattingElements()
			p.addElement()
		case a.A:
			for i := len(p.afe) - 1; i >= 0 && p.afe[i].Type != scopeMarkerNode; i-- {
				if n := p.afe[i]; n.Type == html.ElementNode && n.DataAtom == a.A {
					p.inBodyEndTagFormatting(a.A, "a")
					p.oe.remove(n)
					p.afe.remove(n)
					break
				}
			}
			p.reconstructActiveFormattingElements()
			p.addFormattingElement()
		case a.B, a.Big, a.Code, a.Em, a.Font, a.I, a.S, a.Small, a.Strike, a.Strong, a.Tt, a.U:
			p.reconstructActiveFormattingElements()
			p.addFormattingElement()
		case a.Nobr:
			p.reconstructActiveFormattingElements()
			if p.elementInScope(defaultScope, a.Nobr) {
				p.inBodyEndTagFormatting(a.Nobr, "nobr")
				p.reconstructActiveFormattingElements()
			}
			p.addFormattingElement()
		case a.Applet, a.Marquee, a.Object:
			p.reconstructActiveFormattingElements()
			p.addElement()
			p.afe = append(p.afe, &scopeMarker)
		case a.Table:
			p.popUntil(buttonScope, a.P)
			p.addElement()
		case a.Area, a.Br, a.Embed, a.Img, a.Input, a.Keygen, a.Wbr:
			p.reconstructActiveFormattingElements()
			p.addElement()
			p.oe.pop()
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
			p.oe.pop()
			p.acknowledgeSelfClosingTag()
		case a.Hr:
			p.popUntil(buttonScope, a.P)
			p.addElement()
			p.oe.pop()
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
			p.reconstructActiveFormattingElements()
			p.parseGenericRawTextElement()
		case a.Iframe:
			p.parseGenericRawTextElement()
		case a.Noembed:
			p.parseGenericRawTextElement()
		case a.Noscript:
			p.reconstructActiveFormattingElements()
			p.addElement()
			// Don't let the tokenizer go into raw text mode when for <noscript> tag and parse
			// its content as regular HTML.
			p.tokenizer.NextIsNotRawText()
		case a.Optgroup, a.Option:
			if p.top().DataAtom == a.Option {
				p.oe.pop()
			}
			p.reconstructActiveFormattingElements()
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
			p.reconstructActiveFormattingElements()
			if p.tok.DataAtom == a.Math {
				adjustAttributeNames(p.tok.Attr, mathMLAttributeAdjustments)
			} else {
				adjustAttributeNames(p.tok.Attr, svgAttributeAdjustments)
			}
			adjustForeignAttributes(p.tok.Attr)
			p.addElement()
			p.top().Namespace = p.tok.Data
			if p.hasSelfClosingToken {
				p.oe.pop()
				p.acknowledgeSelfClosingTag()
			}
			return true
		default:
			p.reconstructActiveFormattingElements()
			p.addElement()
		}
	case html.EndTagToken:
		switch p.tok.DataAtom {
		case a.Body:
			if p.elementInScope(defaultScope, a.Body) {
				p.im = afterBodyIM
			}
		case a.Html:
			if p.elementInScope(defaultScope, a.Body) {
				p.parseImpliedToken(html.EndTagToken, a.Body, a.Body.String())
				return false
			}
			return true
		case a.Address, a.Article, a.Aside, a.Blockquote, a.Button, a.Center, a.Details, a.Dialog, a.Dir, a.Div, a.Dl, a.Fieldset, a.Figcaption, a.Figure, a.Footer, a.Header, a.Hgroup, a.Listing, a.Main, a.Menu, a.Nav, a.Ol, a.Pre, a.Section, a.Summary, a.Ul:
			p.popUntil(defaultScope, p.tok.DataAtom)
		case a.Form:
			if p.oe.contains(a.Template) {
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
			} else {
				node := p.form
				p.form = nil
				i := p.indexOfElementInScope(defaultScope, a.Form)
				if node == nil || i == -1 || p.oe[i] != node {
					// Ignore the token.
					return true
				}
				p.generateImpliedEndTags()
				p.oe.remove(node)
			}
		case a.P:
			if !p.elementInScope(buttonScope, a.P) {
				p.parseImpliedToken(html.StartTagToken, a.P, a.P.String())
			}
			p.popUntil(buttonScope, a.P)
		case a.Li:
			p.popUntil(listItemScope, a.Li)
		case a.Dd, a.Dt:
			p.popUntil(defaultScope, p.tok.DataAtom)
		case a.H1, a.H2, a.H3, a.H4, a.H5, a.H6:
			p.popUntil(defaultScope, a.H1, a.H2, a.H3, a.H4, a.H5, a.H6)
		case a.A, a.B, a.Big, a.Code, a.Em, a.Font, a.I, a.Nobr, a.S, a.Small, a.Strike, a.Strong, a.Tt, a.U:
			p.inBodyEndTagFormatting(p.tok.DataAtom, p.tok.Data)
		case a.Applet, a.Marquee, a.Object:
			if p.popUntil(defaultScope, p.tok.DataAtom) {
				p.clearActiveFormattingElements()
			}
		case a.Br:
			p.tok.Type = html.StartTagToken
			return false
		default:
			p.inBodyEndTagOther(p.tok.DataAtom, p.tok.Data)
		}
	case html.CommentToken:
		p.addChild(&html.Node{
			Type: html.CommentNode,
			Data: p.tok.Data,
		})
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

func (p *parser) inBodyEndTagFormatting(tagAtom a.Atom, tagName string) {
	// This is the "adoption agency" algorithm, described at
	// https://html.spec.whatwg.org/multipage/syntax.html#adoptionAgency

	// TODO: this is a fairly literal line-by-line translation of that algorithm.
	// Once the code successfully parses the comprehensive test suite, we should
	// refactor this code to be more idiomatic.

	// Steps 1-2
	if current := p.top(); current.Data == tagName && p.afe.index(current) == -1 {
		p.oe.pop()
		return
	}

	// Steps 3-5. The outer loop.
	for i := 0; i < 8; i++ {
		// Step 6. Find the formatting element.
		var formattingElement *html.Node
		for j := len(p.afe) - 1; j >= 0; j-- {
			if p.afe[j].Type == scopeMarkerNode {
				break
			}
			if p.afe[j].DataAtom == tagAtom {
				formattingElement = p.afe[j]
				break
			}
		}
		if formattingElement == nil {
			p.inBodyEndTagOther(tagAtom, tagName)
			return
		}

		// Step 7. Ignore the tag if formatting element is not in the stack of open elements.
		feIndex := p.oe.index(formattingElement)
		if feIndex == -1 {
			p.afe.remove(formattingElement)
			return
		}
		// Step 8. Ignore the tag if formatting element is not in the scope.
		if !p.elementInScope(defaultScope, tagAtom) {
			// Ignore the tag.
			return
		}

		// Step 9. This step is omitted because it's just a parse error but no need to return.

		// Steps 10-11. Find the furthest block.
		var furthestBlock *html.Node
		for _, e := range p.oe[feIndex:] {
			if isSpecialElement(e) {
				furthestBlock = e
				break
			}
		}
		if furthestBlock == nil {
			e := p.oe.pop()
			for e != formattingElement {
				e = p.oe.pop()
			}
			p.afe.remove(e)
			return
		}

		// Steps 12-13. Find the common ancestor and bookmark node.
		commonAncestor := p.doc
		if feIndex > 0 {
			commonAncestor = p.oe[feIndex-1]
		}
		bookmark := p.afe.index(formattingElement)

		// Step 14. The inner loop. Find the lastNode to reparent.
		lastNode := furthestBlock
		node := furthestBlock
		x := p.oe.index(node)
		// Step 14.1.
		j := 0
		for {
			// Step 14.2.
			j++
			// Step. 14.3.
			x--
			node = p.oe[x]
			// Step 14.4. Go to the next step if node is formatting element.
			if node == formattingElement {
				break
			}
			// Step 14.5. Remove node from the list of active formatting elements if
			// inner loop counter is greater than three and node is in the list of
			// active formatting elements.
			if ni := p.afe.index(node); j > 3 && ni > -1 {
				p.afe.remove(node)
				// If any element of the list of active formatting elements is removed,
				// we need to take care whether bookmark should be decremented or not.
				// This is because the value of bookmark may exceed the size of the
				// list by removing elements from the list.
				if ni <= bookmark {
					bookmark--
				}
				continue
			}
			// Step 14.6. Continue the next inner loop if node is not in the list of
			// active formatting elements.
			if p.afe.index(node) == -1 {
				p.oe.remove(node)
				continue
			}
			// Step 14.7.
			clone := cloneNode(node)
			p.afe[p.afe.index(node)] = clone
			p.oe[p.oe.index(node)] = clone
			node = clone
			// Step 14.8.
			if lastNode == furthestBlock {
				bookmark = p.afe.index(node) + 1
			}
			// Step 14.9.
			if lastNode.Parent != nil {
				lastNode.Parent.RemoveChild(lastNode)
			}
			node.AppendChild(lastNode)
			// Step 14.10.
			lastNode = node
		}

		// Step 15. Reparent lastNode to the common ancestor,
		// or for misnested table nodes, to the foster parent.
		if lastNode.Parent != nil {
			lastNode.Parent.RemoveChild(lastNode)
		}
		switch commonAncestor.DataAtom {
		case a.Table, a.Tbody, a.Tfoot, a.Thead, a.Tr:
			p.fosterParent(lastNode)
		default:
			commonAncestor.AppendChild(lastNode)
		}

		// Steps 16-18. Reparent nodes from the furthest block's children
		// to a clone of the formatting element.
		clone := cloneNode(formattingElement)
		reparentChildren(clone, furthestBlock)
		furthestBlock.AppendChild(clone)

		// Step 19. Fix up the list of active formatting elements.
		if oldLoc := p.afe.index(formattingElement); oldLoc != -1 && oldLoc < bookmark {
			// Move the bookmark with the rest of the list.
			bookmark--
		}
		p.afe.remove(formattingElement)
		p.afe.insert(bookmark, clone)

		// Step 20. Fix up the stack of open elements.
		p.oe.remove(formattingElement)
		p.oe.insert(p.oe.index(furthestBlock)+1, clone)
	}
}

// inBodyEndTagOther performs the "any other end tag" algorithm for inBodyIM.
// "Any other end tag" handling from 12.2.6.5 The rules for parsing tokens in foreign content
// https://html.spec.whatwg.org/multipage/syntax.html#parsing-main-inforeign
func (p *parser) inBodyEndTagOther(tagAtom a.Atom, tagName string) {
	for i := len(p.oe) - 1; i >= 0; i-- {
		// Two element nodes have the same tag if they have the same Data (a
		// string-typed field). As an optimization, for common HTML tags, each
		// Data string is assigned a unique, non-zero DataAtom (a uint32-typed
		// field), since integer comparison is faster than string comparison.
		// Uncommon (custom) tags get a zero DataAtom.
		//
		// The if condition here is equivalent to (p.oe[i].Data == tagName).
		if (p.oe[i].DataAtom == tagAtom) &&
			((tagAtom != 0) || (p.oe[i].Data == tagName)) {
			p.oe = p.oe[:i]
			break
		}
		if isSpecialElement(p.oe[i]) {
			break
		}
	}
}

// Section 12.2.6.4.8.
func textIM(p *parser) bool {
	switch p.tok.Type {
	case html.ErrorToken:
		p.oe.pop()
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
		p.oe.pop()
	}
	p.im = p.originalIM
	p.originalIM = nil
	return p.tok.Type == html.EndTagToken
}

// Section 12.2.6.4.19.
func afterBodyIM(p *parser) bool {
	switch p.tok.Type {
	case html.ErrorToken:
		// Stop parsing.
		return true
	case html.TextToken:
		s := strings.TrimLeft(p.tok.Data, whitespace)
		if len(s) == 0 {
			// It was all whitespace.
			return inBodyIM(p)
		}
	case html.StartTagToken:
		if p.tok.DataAtom == a.Html {
			return inBodyIM(p)
		}
	case html.EndTagToken:
		if p.tok.DataAtom == a.Html {
			return true
		}
	case html.CommentToken:
		// The comment is attached to the <html> element.
		if len(p.oe) < 1 || p.oe[0].DataAtom != a.Html {
			panic("html: bad parser state: <html> element not found, in the after-body insertion mode")
		}
		p.oe[0].AppendChild(&html.Node{
			Type: html.CommentNode,
			Data: p.tok.Data,
		})
		return true
	}
	p.im = inBodyIM
	return false
}

// Section 12.2.6.5
func parseForeignContent(p *parser) bool {
	switch p.tok.Type {
	case html.TextToken:
		p.tok.Data = strings.Replace(p.tok.Data, "\x00", "\ufffd", -1)
		p.addText(p.tok.Data)
	case html.CommentToken:
		p.addChild(&html.Node{
			Type: html.CommentNode,
			Data: p.tok.Data,
		})
	case html.StartTagToken:
		current := p.oe.top()
		switch current.Namespace {
		case "math":
			adjustAttributeNames(p.tok.Attr, mathMLAttributeAdjustments)
		case "svg":
			// Adjust SVG tag names. The tokenizer lower-cases tag names, but
			// SVG wants e.g. "foreignObject" with a capital second "O".
			if x := svgTagNameAdjustments[p.tok.Data]; x != "" {
				p.tok.DataAtom = a.Lookup([]byte(x))
				p.tok.Data = x
			}
			adjustAttributeNames(p.tok.Attr, svgAttributeAdjustments)
		default:
			panic("html: bad parser state: unexpected namespace")
		}
		adjustForeignAttributes(p.tok.Attr)
		namespace := current.Namespace
		p.addElement()
		p.top().Namespace = namespace
		if namespace != "" {
			// Don't let the tokenizer go into raw text mode in foreign content
			// (e.g. in an SVG <title> tag).
			p.tokenizer.NextIsNotRawText()
		}
		if p.hasSelfClosingToken {
			p.oe.pop()
			p.acknowledgeSelfClosingTag()
		}
	case html.EndTagToken:
		for i := len(p.oe) - 1; i >= 0; i-- {
			if p.oe[i].Namespace == "" {
				return p.im(p)
			}
			if strings.EqualFold(p.oe[i].Data, p.tok.Data) {
				p.oe = p.oe[:i]
				break
			}
		}
		return true
	default:
		// Ignore the token.
	}
	return true
}

// Section 12.2.6.
func (p *parser) inForeignContent() bool {
	if len(p.oe) == 0 {
		return false
	}
	n := p.oe.top()
	if n.Namespace == "" {
		return false
	}
	if mathMLTextIntegrationPoint(n) {
		if p.tok.Type == html.StartTagToken && p.tok.DataAtom != a.Mglyph && p.tok.DataAtom != a.Malignmark {
			return false
		}
		if p.tok.Type == html.TextToken {
			return false
		}
	}
	if n.Namespace == "math" && n.DataAtom == a.AnnotationXml && p.tok.Type == html.StartTagToken && p.tok.DataAtom == a.Svg {
		return false
	}
	if htmlIntegrationPoint(n) && (p.tok.Type == html.StartTagToken || p.tok.Type == html.TextToken) {
		return false
	}
	if p.tok.Type == html.ErrorToken {
		return false
	}
	return true
}

// parseImpliedToken parses a token as though it had appeared in the parser's
// input.
func (p *parser) parseImpliedToken(t html.TokenType, dataAtom a.Atom, data string) {
	realToken, selfClosing := p.tok, p.hasSelfClosingToken
	p.tok = html.Token{
		Type:     t,
		DataAtom: dataAtom,
		Data:     data,
	}
	p.hasSelfClosingToken = false
	p.parseCurrentToken()
	p.tok, p.hasSelfClosingToken = realToken, selfClosing
}

// parseCurrentToken runs the current token through the parsing routines
// until it is consumed.
func (p *parser) parseCurrentToken() {
	if p.tok.Type == html.SelfClosingTagToken {
		p.hasSelfClosingToken = true
		p.tok.Type = html.StartTagToken
	}

	consumed := false
	for !consumed {
		if p.inForeignContent() {
			consumed = parseForeignContent(p)
		} else {
			consumed = p.im(p)
		}
	}

	if p.hasSelfClosingToken {
		// This is a parse error, but ignore it.
		p.hasSelfClosingToken = false
	}
}

func (p *parser) parse() error {
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

// Parse returns the parse tree for the HTML from the given Reader.
// The input is assumed to be UTF-8 encoded.
func Parse(r io.Reader) (*html.Node, error) {
	p := &parser{
		tokenizer: html.NewTokenizer(r),
		doc: &html.Node{
			Type: html.DocumentNode,
		},
		im: inBodyIM,
	}

	if err := p.parse(); err != nil {
		return nil, err
	}
	return p.doc, nil
}
