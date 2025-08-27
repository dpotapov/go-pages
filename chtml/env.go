package chtml

import (
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"golang.org/x/net/html"
)

// AnyShape is a special value indicating that the variable represents any data and
// should be rendered appropriately.
// It is used by the parser to build RenderShape.
type anyShape struct{}

var AnyShape = anyShape{}

func HtmlPlusAny(a *html.Node, b any) *html.Node {
	switch v := b.(type) {
	case *html.Node:
		return HtmlPlusHtml(a, v)
	default:
		if b == nil {
			return a
		}
		return HtmlPlusText(a, fmt.Sprint(v))
	}
}

func AnyPlusHtml(a any, b *html.Node) *html.Node {
	switch v := a.(type) {
	case *html.Node:
		return HtmlPlusHtml(v, b)
	default:
		if a == nil {
			return b
		}
		return TextPlusHtml(fmt.Sprint(v), b)
	}
}

func TextPlusHtml(a string, b *html.Node) *html.Node {
	if b == nil {
		return &html.Node{
			Type: html.TextNode,
			Data: a,
		}
	}
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

func HtmlPlusText(a *html.Node, b string) *html.Node {
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

func HtmlPlusHtml(a, b *html.Node) *html.Node {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
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
		return HtmlPlusAny(va, b)
	}
	if vb, ok := b.(*html.Node); ok {
		if vb == nil {
			return a
		}
		return AnyPlusHtml(a, vb)
	}

	if isEquivalentToAny(a) {
		return b
	}
	if isEquivalentToAny(b) {
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

	return repr(a) + repr(b)
}

func repr(v any) string {
	// If nil, return empty string
	if v == nil {
		return ""
	}

	// If string, return it directly
	if s, ok := v.(string); ok {
		return s
	}

	// Handle numeric types with proper formatting
	switch n := v.(type) {
	case int:
		return fmt.Sprintf("%d", n)
	case int8:
		return fmt.Sprintf("%d", n)
	case int16:
		return fmt.Sprintf("%d", n)
	case int32:
		return fmt.Sprintf("%d", n)
	case int64:
		return fmt.Sprintf("%d", n)
	case uint:
		return fmt.Sprintf("%d", n)
	case uint8:
		return fmt.Sprintf("%d", n)
	case uint16:
		return fmt.Sprintf("%d", n)
	case uint32:
		return fmt.Sprintf("%d", n)
	case uint64:
		return fmt.Sprintf("%d", n)
	case float32:
		return fmt.Sprintf("%g", n)
	case float64:
		return fmt.Sprintf("%g", n)
	case complex64, complex128:
		return fmt.Sprintf("%g", n)
	}

	// Check for text marshaler interface
	if tm, ok := v.(encoding.TextMarshaler); ok {
		b, err := tm.MarshalText()
		if err == nil {
			return string(b)
		}
	}

	// If []byte, return as string
	if b, ok := v.([]byte); ok {
		return string(b)
	}

	// Try using JSON marshaling for complex types
	b, err := json.Marshal(v)
	if err == nil {
		return string(b)
	}

	// Fallback to fmt.Sprint
	return fmt.Sprint(v)
}

func isEquivalentToAny(v any) bool {
	// If v is nil, compare directly to the dereferenced newAny
	if v == nil {
		return true
	}

	// Use reflection to check for typed nil
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if rv.IsNil() {
			return true
		}
	}

	// Fallback: compare to a new any (should rarely be needed)
	newAny := new(any)
	vValue := rv
	if vValue.Kind() == reflect.Ptr {
		vValue = vValue.Elem()
	}
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
	if c == nil {
		return
	}
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
