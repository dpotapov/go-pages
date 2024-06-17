package chtml

import (
	"strings"

	"github.com/beevik/etree"
	"golang.org/x/net/html"
)

type ComponentError struct {
	name string
	err  error
	path string
	doc  *etree.Element
}

func newComponentError(compName string, t etree.Token, err error) *ComponentError {
	path := ""
	if el, ok := t.(*etree.Element); ok {
		path = el.GetPath()
	} else {
		path = t.Parent().GetPath()
	}
	return &ComponentError{
		name: compName,
		err:  err,
		path: path,
		doc:  buildErrorContext(t),
	}
}

func (e *ComponentError) Error() string {
	return e.path + ": " + e.err.Error()
}

func (e *ComponentError) Unwrap() error {
	return e.err
}

func (e *ComponentError) HTMLContext() string {
	return renderErrorContext(e.doc)
}

// errorContextBuilder is a type to organize helper functions for building error context trees.
type errorContextBuilder struct{}

func (b errorContextBuilder) addPrevSiblings(doc *etree.Element, t etree.Token) {
	if t.Parent() == nil {
		return
	}

	siblings, i := t.Parent().Child, t.Index()

	for j, c := i-1, 0; j >= 0; j-- {
		// skip non-white space text nodes
		if cd, ok := siblings[j].(*etree.CharData); ok && cd.IsWhitespace() {
			continue
		}
		if c == 2 {
			doc.AddChild(etree.NewText("..."))
			break
		} else {
			b.addToken(doc, siblings[j])
			c++
		}
	}
}

func (b errorContextBuilder) addNextSiblings(doc *etree.Element, t etree.Token) {
	if t.Parent() == nil {
		return
	}

	siblings, i := t.Parent().Child, t.Index()

	for j, c := i+1, 0; j < len(siblings); j++ {
		// skip non-white space text nodes
		if cd, ok := siblings[j].(*etree.CharData); ok && cd.IsWhitespace() {
			continue
		}
		if c == 2 {
			doc.AddChild(etree.NewText("..."))
			break
		} else {
			b.addToken(doc, siblings[j])
			c++
		}
	}
}

func (b errorContextBuilder) addToken(doc *etree.Element, t etree.Token) {
	switch el := t.(type) {
	case *etree.Element:
		clone := etree.NewElement(el.FullTag())
		clone.Attr = make([]etree.Attr, len(el.Attr))
		copy(clone.Attr, el.Attr)
		if el.ChildElements() != nil {
			clone.AddChild(etree.NewText("..."))
		} else {
			clone.SetText(el.Text())
		}
		doc.AddChild(clone)
	case *etree.CharData:
		if !el.IsWhitespace() {
			doc.AddChild(etree.NewText(el.Data))
		}
	default:
		doc.AddChild(t)
	}
}

func (b errorContextBuilder) wrapParent(doc *etree.Element, t etree.Token) *etree.Element {
	parent := t.Parent()
	if parent == nil || parent.Tag == "" {
		return doc // do not wrap the root element
	}

	doc.Space = parent.Space
	doc.Tag = parent.Tag
	doc.Attr = make([]etree.Attr, len(parent.Attr))
	copy(doc.Attr, parent.Attr)

	wrapper := &etree.Element{}
	wrapper.AddChild(doc)

	return wrapper
}

// buildErrorContext creates an XML tree around the token t to provide context for an error.
func buildErrorContext(t etree.Token) *etree.Element {
	doc := &etree.Element{}
	b := errorContextBuilder{}
	b.addPrevSiblings(doc, t)
	b.addToken(doc, t)
	b.addNextSiblings(doc, t)
	doc = b.wrapParent(doc, t)
	return doc
}

func renderErrorContext(doc *etree.Element) string {
	dst := &html.Node{Type: html.DocumentNode}

	// traverse the etree.Element and build the html.Node
	var render func(*html.Node, *etree.Element)
	render = func(dst *html.Node, src *etree.Element) {
		for _, c := range src.Child {
			switch t := c.(type) {
			case *etree.Element:
				n := &html.Node{Type: html.ElementNode, Data: t.FullTag()}
				for _, a := range t.Attr {
					n.Attr = append(n.Attr, html.Attribute{Key: a.Key, Val: a.Value})
				}
				dst.AppendChild(n)
				render(n, t)
			case *etree.CharData:
				dst.AppendChild(&html.Node{Type: html.TextNode, Data: t.Data})
			}
		}
	}

	render(dst, doc)

	var buf strings.Builder
	_ = html.Render(&buf, dst)

	return buf.String()
}
