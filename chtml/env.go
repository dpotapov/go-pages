package chtml

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"golang.org/x/net/html"
)

type env map[string]any

func (e env) HtmlPlusAny(a *html.Node, b any) *html.Node {
	switch v := b.(type) {
	case *html.Node:
		return e.HtmlPlusHtml(a, v)
	default:
		if b == nil {
			return a
		}
		return e.HtmlPlusText(a, fmt.Sprint(v))
	}
}

func (e env) AnyPlusHtml(a any, b *html.Node) *html.Node {
	switch v := a.(type) {
	case *html.Node:
		return e.HtmlPlusHtml(v, b)
	default:
		if a == nil {
			return b
		}
		return e.TextPlusHtml(fmt.Sprint(v), b)
	}
}

func (e env) TextPlusHtml(a string, b *html.Node) *html.Node {
	if b.Type == html.TextNode {
		b.Data = a + b.Data
		return b
	}
	n := &html.Node{
		Type: html.DocumentNode,
	}
	n.AppendChild(&html.Node{
		Type: html.TextNode,
		Data: a,
	})
	appendChild(n, b)
	return n
}

func (e env) HtmlPlusText(a *html.Node, b string) *html.Node {
	if a.Type == html.TextNode {
		a.Data += b
		return a
	}
	n := &html.Node{
		Type: html.DocumentNode,
	}
	appendChild(n, a)
	n.AppendChild(&html.Node{
		Type: html.TextNode,
		Data: b,
	})
	return n
}

func (e env) HtmlPlusHtml(a, b *html.Node) *html.Node {
	if a.Type == html.TextNode && b.Type == html.TextNode {
		a.Data += b.Data
		return a
	}

	var n *html.Node
	if a.Type == html.DocumentNode {
		n = a
	} else {
		n = &html.Node{
			Type: html.DocumentNode,
		}
		appendChild(n, a)
	}
	appendChild(n, b)
	return n
}

func AnyPlusAny(a any, b any) any {
	if va, ok := a.(*html.Node); ok {
		return env{}.HtmlPlusAny(va, b)
	}
	if vb, ok := b.(*html.Node); ok {
		return env{}.AnyPlusHtml(a, vb)
	}

	if isEquivalentToNewAny(a) {
		return b
	}
	if isEquivalentToNewAny(b) {
		return a
	}

	if sa, ok := a.(string); ok {
		if sb, ok := b.(string); ok {
			return sa + sb // if both are strings, concatenate them
		} else if strings.TrimSpace(sa) == "" {
			return b // leave "b" if "a" is whitespace
		}
	}

	if sb, ok := b.(string); ok {
		if strings.TrimSpace(sb) == "" {
			return a // leave "a" if "b" is whitespace
		}
	}

	return fmt.Sprint(a) + fmt.Sprint(b)
}

func isEquivalentToNewAny(v any) bool {
	// Create a new any using new
	newAny := new(any) // newAny is of type *any and points to nil

	// If v is nil, compare directly to the dereferenced newAny
	if v == nil {
		return true
	}

	// Get the reflect.Value of v
	vValue := reflect.ValueOf(v)

	// If v is a pointer, dereference it
	if vValue.Kind() == reflect.Ptr {
		vValue = vValue.Elem()
	}

	// Compare the underlying values using reflect.DeepEqual
	return reflect.DeepEqual(vValue.Interface(), *newAny)
}

func AnyToHtml(a any) *html.Node {
	if a == nil {
		return nil
	}
	if v, ok := a.(*html.Node); ok {
		return v
	}

	var repr string

	switch v := reflect.TypeOf(a); v.Kind() {
	case reflect.Slice:
		n := &html.Node{
			Type: html.DocumentNode,
		}
		for _, child := range a.([]any) {
			if nn := AnyToHtml(child); nn != nil {
				n.AppendChild(nn)
			}
		}
		return n
	case reflect.Map, reflect.Struct:
		// check if implements Stringer
		if s, ok := a.(fmt.Stringer); ok {
			repr = s.String()
			break
		}
		// convert to json
		b, err := json.Marshal(a)
		if err != nil {
			repr = fmt.Sprint(a)
			break
		}
		repr = string(b)
	default:
		repr = fmt.Sprint(a)
	}

	return &html.Node{
		Type: html.TextNode,
		Data: repr,
	}
}

func appendChild(p, c *html.Node) {
	if c.Parent != nil {
		c = cloneHtmlTree(c)
	}
	p.AppendChild(c)
}

func cloneHtmlNode(n *html.Node) *html.Node {
	clone := &html.Node{
		Type:     n.Type,
		DataAtom: n.DataAtom,
		Data:     n.Data,
	}
	if n.Attr != nil {
		clone.Attr = make([]html.Attribute, len(n.Attr))
		copy(clone.Attr, n.Attr)
	}
	return clone
}

func cloneHtmlTree(src *html.Node) *html.Node {
	clone := cloneHtmlNode(src)
	for child := src.FirstChild; child != nil; child = child.NextSibling {
		c := cloneHtmlTree(child)
		clone.AppendChild(c)
	}
	return clone
}
