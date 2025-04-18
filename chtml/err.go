package chtml

import (
	"errors"
	"fmt"
	"runtime/debug"
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

type DecodeError struct {
	Key string
	Err error
}

func (e *DecodeError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("could not decode %s", e.Key)
	}
	return fmt.Sprintf("could not decode %s: %s", e.Key, e.Err.Error())
}

func (e *DecodeError) Unwrap() error {
	return e.Err
}

func (e *DecodeError) Is(target error) bool {
	var de *DecodeError
	if errors.As(target, &de) {
		return e.Key == de.Key
	}
	return false
}

type ComponentError struct {
	err   error
	path  string
	html  *html.Node
	stack []byte
}

func newComponentError(n *Node, err error) *ComponentError {
	return &ComponentError{
		err:   err,
		path:  buildErrorPath(n),
		html:  buildErrorContext(n),
		stack: debug.Stack(),
	}
}

func (e *ComponentError) Error() string {
	if e.path == "" {
		return e.err.Error()
	}
	return e.path + ": " + e.err.Error()
}

func (e *ComponentError) Unwrap() error {
	return e.err
}

func (e *ComponentError) HTMLContext() string {
	var buf strings.Builder
	_ = html.Render(&buf, e.html)

	return buf.String()
}

// StackTrace returns the captured stack trace from when the error was created
func (e *ComponentError) StackTrace() string {
	return string(e.stack)
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
	if n.Type == importNode {
		n.Type = html.ElementNode
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

func buildErrorPath(n *Node) string {
	// recursively build path to the node n from the root
	var path []string
	for n != nil {
		if n.Type == html.ElementNode {
			path = append(path, n.Data.RawString())
		}
		n = n.Parent
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return strings.Join(path, "/")
}
