package chtml

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/net/html"
)

var (
	// ErrComponentNotFound is returned by Importer implementations when a component is not found.
	ErrComponentNotFound = errors.New("component not found")

	// ErrImportNotAllowed is returned when an Importer is not set for the component.
	ErrImportNotAllowed = errors.New("imports are not allowed")
)

type UnrecognizedArgumentError struct {
	Name string
}

func (e *UnrecognizedArgumentError) Error() string {
	return fmt.Sprintf("unrecognized argument %s", e.Name)
}

func (e *UnrecognizedArgumentError) Is(target error) bool {
	var ua *UnrecognizedArgumentError
	if errors.As(target, &ua) {
		return e.Name == ua.Name
	}
	return false
}

type ComponentError struct {
	name string
	err  error
	path string
	html *html.Node
}

func newComponentError(compName string, n *Node, err error) *ComponentError {
	return &ComponentError{
		name: compName,
		err:  err,
		path: "",
		html: buildErrorContext(n),
	}
}

func (e *ComponentError) Error() string {
	if e.name == "" {
		return e.path + ": " + e.err.Error()
	}
	return e.name + " " + e.path + ": " + e.err.Error()
}

func (e *ComponentError) Unwrap() error {
	return e.err
}

func (e *ComponentError) HTMLContext() string {
	var buf strings.Builder
	_ = html.Render(&buf, e.html)

	return buf.String()
}

// errorContextBuilder is a type to organize helper functions for building error context trees.
type errorContextBuilder struct {
	html *html.Node
}

func (b errorContextBuilder) addPrevSiblings(src *Node) {
	var nodesToAdd []*Node
	for s, c := src.PrevSibling, 0; s != nil && c < 2; s = s.PrevSibling {
		nodesToAdd = append(nodesToAdd, s)
		if !s.IsWhitespace() {
			c++
		}
		if c == 2 && s.PrevSibling != nil {
			nodesToAdd = append(nodesToAdd, &Node{Type: html.TextNode, Data: NewExprRaw("...")})
		}
	}
	for i := len(nodesToAdd) - 1; i >= 0; i-- {
		b.addNode(nodesToAdd[i], true)
	}
}

func (b errorContextBuilder) addNextSiblings(src *Node) {
	for s, c := src.NextSibling, 0; s != nil && c < 2; s = s.NextSibling {
		if !s.IsWhitespace() {
			c++
		}
		b.addNode(s, true)
		if c == 2 && s.NextSibling != nil {
			b.html.AppendChild(&html.Node{Type: html.TextNode, Data: "..."})
		}
	}
}

// converts the given Node into html.Node and adds it to the HTML error context tree.
// If the Node has child nodes, the placeholder "..." is added to the tree.
func (b errorContextBuilder) addNode(src *Node, children bool) {
	n := &html.Node{
		Type:      src.Type,
		DataAtom:  src.DataAtom,
		Data:      src.Data.RawString(),
		Namespace: src.Namespace,
	}
	if len(src.Attr) > 0 {
		n.Attr = make([]html.Attribute, len(src.Attr))
		for i, a := range src.Attr {
			n.Attr[i] = html.Attribute{Key: a.Key, Val: a.Val.RawString()}
		}
	}

	if children && src.FirstChild != nil {
		var nonTextNode *Node
		for c := src.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.TextNode {
				n.AppendChild(&html.Node{Type: html.TextNode, Data: c.Data.RawString()})
			} else {
				n.AppendChild(&html.Node{Type: html.TextNode, Data: "..."})
				nonTextNode = c
				break
			}
		}
		for c := src.LastChild; nonTextNode != nil && c != nonTextNode; c = c.PrevSibling {
			if c.Type == html.TextNode {
				n.AppendChild(&html.Node{Type: html.TextNode, Data: c.Data.RawString()})
			} else {
				break
			}
		}
	}
	b.html.AppendChild(n)
}

func (b errorContextBuilder) wrapParent(src *Node) {
	if src.Parent == nil || src.Parent.Type != html.ElementNode {
		return
	}
	b.addNode(src.Parent, false)

	// reparent nodes
	newParent := b.html.LastChild
	for {
		child := b.html.FirstChild
		if child == newParent {
			break
		}
		b.html.RemoveChild(child)
		newParent.AppendChild(child)
	}
}

// remove first and last whitespace nodes from the children of the given node.
func (b errorContextBuilder) stripWhitespace() {
	if b.html.FirstChild == nil {
		return
	}
	if b.html.FirstChild.Type == html.TextNode && strings.TrimSpace(b.html.FirstChild.Data) == "" {
		b.html.RemoveChild(b.html.FirstChild)
	}
	if b.html.LastChild == nil {
		return
	}
	if b.html.LastChild.Type == html.TextNode && strings.TrimSpace(b.html.LastChild.Data) == "" {
		b.html.RemoveChild(b.html.LastChild)
	}
}

// buildErrorContext creates an XML tree around the token t to provide context for an error.
func buildErrorContext(n *Node) *html.Node {
	b := errorContextBuilder{
		html: &html.Node{Type: html.DocumentNode},
	}
	b.addPrevSiblings(n)
	b.addNode(n, true)
	b.addNextSiblings(n)
	b.wrapParent(n)
	b.stripWhitespace()
	return b.html
}
