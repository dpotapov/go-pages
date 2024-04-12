package chtml

import (
	"fmt"

	"golang.org/x/net/html"
)

type env map[string]any

func (e env) HtmlPlusAny(a *html.Node, b any) *html.Node {
	switch v := b.(type) {
	case *html.Node:
		return e.HtmlPlusHtml(a, v)
	default:
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
	n := &html.Node{
		Type: html.DocumentNode,
	}
	n.AppendChild(&html.Node{
		Type: html.TextNode,
		Data: a,
	})
	n.AppendChild(b)
	return n
}

func (e env) HtmlPlusText(a *html.Node, b string) *html.Node {
	n := &html.Node{
		Type: html.DocumentNode,
	}
	n.AppendChild(a)
	n.AppendChild(&html.Node{
		Type: html.TextNode,
		Data: b,
	})
	return n
}

func (e env) HtmlPlusHtml(a, b *html.Node) *html.Node {
	var n *html.Node
	if a.Type == html.DocumentNode {
		n = a
	} else {
		n = &html.Node{
			Type: html.DocumentNode,
		}
		n.AppendChild(a)
	}
	n.AppendChild(b)
	return n
}

func AnyPlusAny(a any, b any) any {
	if va, ok := a.(*html.Node); ok {
		return env{}.HtmlPlusAny(va, b)
	}
	if vb, ok := b.(*html.Node); ok {
		return env{}.AnyPlusHtml(a, vb)
	}

	if a == nil {
		return b
	}
	if b == nil {
		return a
	}

	s := ""
	if a != nil {
		s += fmt.Sprint(a)
	}
	if b != nil {
		s += fmt.Sprint(b)
	}
	return s
}
