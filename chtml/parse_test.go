// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package chtml

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/expr-lang/expr/file"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

func dumpIndent(w io.Writer, level int) {
	_, _ = io.WriteString(w, "| ")
	for i := 0; i < level; i++ {
		_, _ = io.WriteString(w, "  ")
	}
}

type sortedAttributes []Attribute

func (a sortedAttributes) Len() int {
	return len(a)
}

func (a sortedAttributes) Less(i, j int) bool {
	if a[i].Namespace != a[j].Namespace {
		return a[i].Namespace < a[j].Namespace
	}
	return a[i].Key < a[j].Key
}

func (a sortedAttributes) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

func dumpLevel(w io.Writer, n *Node, level int) error {
	dumpIndent(w, level)
	level++
	switch n.Type {
	case html.ErrorNode:
		return errors.New("unexpected ErrorNode")
	case html.DocumentNode:
		return errors.New("unexpected DocumentNode")
	case html.ElementNode, importNode:
		if n.Namespace != "" {
			fmt.Fprintf(w, "<%s %s>", n.Namespace, n.Data.RawString())
		} else {
			fmt.Fprintf(w, "<%s>", n.Data.RawString())
		}
		if !n.Cond.IsEmpty() {
			_, _ = io.WriteString(w, "\n")
			dumpIndent(w, level)
			if n.PrevCond == nil {
				_, _ = fmt.Fprintf(w, `c:if="%s"`, n.Cond.RawString())
			} else {
				_, _ = fmt.Fprintf(w, `c:else-if="%s"`, n.Cond.RawString())
			}
		}
		if !n.Loop.IsEmpty() {
			_, _ = io.WriteString(w, "\n")
			dumpIndent(w, level)
			loopVars := []string{"_", "_"}
			if n.LoopVar != "" {
				loopVars[0] = n.LoopVar
			}
			if n.LoopIdx != "" {
				loopVars[1] = n.LoopIdx
			}
			_, _ = fmt.Fprintf(w, `c:for="%s in %s"`, strings.Join(loopVars, ", "), n.Loop.RawString())
		}
		attr := sortedAttributes(n.Attr)
		sort.Sort(attr)
		for _, a := range attr {
			_, _ = io.WriteString(w, "\n")
			dumpIndent(w, level)
			if a.Namespace != "" {
				_, _ = fmt.Fprintf(w, `%s %s="%s"`, a.Namespace, a.Key, a.Val.RawString())
			} else {
				_, _ = fmt.Fprintf(w, `%s="%s"`, a.Key, a.Val.RawString())
			}
		}
		if n.Namespace == "" && n.DataAtom == atom.Template {
			_, _ = io.WriteString(w, "\n")
			dumpIndent(w, level)
			level++
			_, _ = io.WriteString(w, "content")
		}
	case html.TextNode:
		fmt.Fprintf(w, `"%s"`, n.Data.RawString())
	case html.CommentNode:
		fmt.Fprintf(w, "<!-- %s -->", n.Data.RawString())
	case html.DoctypeNode:
		fmt.Fprintf(w, "<!DOCTYPE %s", n.Data.RawString())
		if n.Attr != nil {
			var p, s string
			for _, a := range n.Attr {
				switch a.Key {
				case "public":
					p = a.Val.RawString()
				case "system":
					s = a.Val.RawString()
				}
			}
			if p != "" || s != "" {
				_, _ = fmt.Fprintf(w, ` "%s"`, p)
				_, _ = fmt.Fprintf(w, ` "%s"`, s)
			}
		}
		_, _ = io.WriteString(w, ">")
	default:
		return errors.New("unknown node type")
	}
	_, _ = io.WriteString(w, "\n")
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if err := dumpLevel(w, c, level); err != nil {
			return err
		}
	}
	return nil
}

func dump(n *Node) (string, error) {
	if n == nil || n.FirstChild == nil {
		return "", nil
	}
	var b bytes.Buffer
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if err := dumpLevel(&b, c, 0); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

func TestParserHTML(t *testing.T) {
	tests := []struct {
		name, text, want string
		errs             []string
	}{
		{
			name: "empty",
			text: "",
			want: "",
		},
		{
			name: "simple text",
			text: "Test",
			want: `
			| "Test"
			`,
		},
		{
			name: "simple element",
			text: "<p>Test</p>",
			want: `
			| <p>
			|   "Test"
			`,
		},
		{
			name: "simple element with attribute",
			text: `<p class="bold">Test</p>`,
			want: `
			| <p>
			|   class="bold"
			|   "Test"
			`,
		},
		{
			name: "li auto-closed",
			text: "<ul><li>ABC<li>DEF</ul>",
			want: `
			| <ul>
			|   <li>
			|     "ABC"
			|   <li>
			|     "DEF"
			`,
		},
		{
			name: "br auto-closed",
			text: "Test<br>tesT",
			want: `
			| "Test"
			| <br>
			| "tesT"
			`,
		},
		{
			name: "headers auto-closed",
			text: "<h1>Lorem ipsum<h2>dolor sit amet",
			want: `
			| <h1>
			|   "Lorem ipsum"
			| <h2>
			|   "dolor sit amet"
			`,
		},
		{
			name: "parse head elements",
			text: `<head>` +
				`<title>Test</title>` +
				`<meta charset="utf-8" />` +
				`<link rel="stylesheet" href="style.css">` +
				`<script src="script.js"></script>` +
				`</head>`,
			want: `
			| <head>
			|   <title>
			|     "Test"
			|   <meta>
			|     charset="utf-8"
			|   <link>
			|     href="style.css"
			|     rel="stylesheet"
			|   <script>
			|     src="script.js"
			`,
		},
		{
			name: "implicit li tag closure",
			text: `<ul><li>Item 1<li>Item 2<li>Item 3</ul>`,
			want: `
			| <ul>
			|   <li>
			|     "Item 1"
			|   <li>
			|     "Item 2"
			|   <li>
			|     "Item 3"
			`,
		},
		{
			name: "parse a element",
			text: `<a href="https://url">https://url</a>`,
			want: `
			| <a>
			|   href="https://url"
			|   "https://url"
			`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.want = removeIndent(tt.want)
			if err := testParseCase(tt.text, tt.want, tt.errs); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestParserCHTML(t *testing.T) {
	tests := []struct {
		name, text, want string
		errs             []string
	}{
		{
			name: "basic condition",
			text: `<p c:if="true">Test</p>`,
			want: `
			| <p>
			|   c:if="true"
			|   "Test"
			`,
		},
		{
			name: "basic loop",
			text: `<p c:for="n in [1, 2, 3]">Test</p>`,
			want: `
			| <p>
			|   c:for="n, _ in [1, 2, 3]"
			|   "Test"
			`,
		},
		{
			name: "element with attribute and loop",
			text: `<p class="bold" c:for="n, i in [1, 2, 3]">Test</p>`,
			want: `
			| <p>
			|   c:for="n, i in [1, 2, 3]"
			|   class="bold"
			|   "Test"
			`,
		},
		{
			name: "loop with variable outside the scope",
			text: `<div c:for="n in [1, 2, 3]"><p>${n}</p></div><p>${n}</p>`,
			errs: []string{"unknown name n"},
			want: `
			| <div>
			|   c:for="n, _ in [1, 2, 3]"
			|   <p>
			|     "${n}"
			| <p>
			|   "${n}"
			`,
		},
		{
			name: "nested loop",
			text: `<div c:for="n in [1, 2]"><div c:for="n in ['a', 'b']"><p>${n}</p></div><p>${n}</p></div><p>${n}</p>`,
			errs: []string{"unknown name n"},
			want: `
			| <div>
			|   c:for="n, _ in [1, 2]"
			|   <div>
			|     c:for="n, _ in ['a', 'b']"
			|     <p>
			|       "${n}"
			|   <p>
			|     "${n}"
			| <p>
			|   "${n}"
			`,
		},
		{
			name: "complex conditions",
			text: `<c:attr name="cond1">${false}</c:attr>` +
				`<c:attr name="cond2">${true}</c:attr>` +
				`<div c:if="cond1">` +
				`` + `<div c:if="cond2">` +
				`` + `` + `<p>Inner True</p>` +
				`` + `</div>` +
				`` + `<div c:else>` +
				`` + `` + `<p>Inner False</p>` +
				`` + `</div>` +
				`</div>` +
				`<div c:else>` +
				`` + `<p>Outer False</p>` +
				`</div>`,
			want: `
			| <c:attr>
			|   name="cond1"
			|   "${false}"
			| <c:attr>
			|   name="cond2"
			|   "${true}"
			| <div>
			|   c:if="cond1"
			|   <div>
			|     c:if="cond2"
			|     <p>
			|       "Inner True"
			|   <div>
			|     c:else-if="true"
			|     <p>
			|       "Inner False"
			| <div>
			|   c:else-if="true"
			|   <p>
			|     "Outer False"
			`,
		},
		{
			name: "attr",
			text: `<c:attr name="text">Hello</c:attr><p>${text}</p>`,
			want: `
			| <c:attr>
			|   name="text"
			|   "Hello"
			| <p>
			|   "${text}"
			`,
		},
		{
			name: "element with attr",
			text: `<p><c:attr name="class">myclass1</c:attr></p>`,
			want: `
			| <p>
			|   <c:attr>
			|     name="class"
			|     "myclass1"
			`,
		},
		{
			name: "component with attr",
			text: `<c:subcomp1 arg1="val1"><c:attr name="arg2">Lorem</c:attr>ipsum</c:subcomp1>`,
			want: `
			| <c:subcomp1>
			|   arg1="val1"
			|   <c:attr>
			|     name="arg2"
			|     "Lorem"
			|   "ipsum"
			`,
		},

		/*
			#data
			<c:attr name="text">Hello</c:attr><p>${text}</p>
			#document


		*/
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.want = removeIndent(tt.want)
			if err := testParseCase(tt.text, tt.want, tt.errs); err != nil {
				t.Error(err)
			}
		})
	}
}

// removeIndent measures the indentation of the first line and removes that
// amount of leading whitespace from all lines.
// The very first \n is also removed.
func removeIndent(s string) string {
	s = strings.TrimLeft(s, "\n") // ignore leading newline

	// find first non-whitespace character
	i := strings.IndexFunc(s, func(r rune) bool {
		return r != ' ' && r != '\t'
	})
	if i == -1 {
		return s
	}

	// remove that amount of leading whitespace from all lines
	lines := strings.Split(s, "\n")
	for j, line := range lines {
		lines[j] = line[i:]
	}
	return strings.Join(lines, "\n")
}

// testParseCase tests one test case from the test files. If the test does not
// pass, it returns an error that explains the failure.
// text is the HTML to be parsed, want is a dump of the correct Parse tree,
// and context is the name of the context node, if any.
func testParseCase(text, want string, expectedErrs []string) (err error) {
	doc, err := Parse(strings.NewReader(text), anyImporter{})
	if err := checkErrors(err, expectedErrs); err != nil {
		return err
	}

	if err := checkTreeConsistency(doc); err != nil {
		return err
	}

	got, err := dump(doc)
	if err != nil {
		return err
	}

	// Compare the parsed tree to the #document section.
	if got != want {
		return fmt.Errorf("got vs want:\n----\n%s----\n%s----", got, want)
	}

	var renderHtmlNode func(n *Node) *html.Node
	renderHtmlNode = func(n *Node) *html.Node {
		nn := &html.Node{
			Type:      n.Type,
			DataAtom:  n.DataAtom,
			Data:      n.Data.RawString(),
			Namespace: n.Namespace,
			Attr:      nil,
		}
		if nn.Type == importNode {
			nn.Type = html.ElementNode // treat imports as elements for testing
		}
		attrCount := len(n.Attr)
		if !n.Cond.IsEmpty() {
			attrCount++
		}
		if !n.Loop.IsEmpty() {
			attrCount++
		}
		if attrCount > 0 {
			nn.Attr = make([]html.Attribute, attrCount)
			j := 0
			if !n.Cond.IsEmpty() {
				if n.PrevCond == nil {
					nn.Attr[j] = html.Attribute{
						Key: "c:if",
						Val: n.Cond.RawString(),
					}
				} else {
					nn.Attr[j] = html.Attribute{
						Key: "c:else-if",
						Val: n.Cond.RawString(),
					}
				}
				j++
			}
			if !n.Loop.IsEmpty() {
				loopVars := ""
				switch {
				case n.LoopVar != "" && n.LoopIdx != "":
					loopVars = n.LoopVar + ", " + n.LoopIdx
				case n.LoopVar != "":
					loopVars = n.LoopVar
				case n.LoopIdx != "":
					loopVars = "_, " + n.LoopIdx
				default:
					loopVars = "_"
				}
				nn.Attr[j] = html.Attribute{
					Key: "c:for",
					Val: loopVars + " in " + n.Loop.RawString(),
				}
				j++
			}
			for i, a := range n.Attr {
				nn.Attr[i+j] = html.Attribute{
					Namespace: a.Namespace,
					Key:       a.Key,
					Val:       a.Val.RawString(),
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			nn.AppendChild(renderHtmlNode(c))
		}
		return nn
	}

	// Check that rendering and re-parsing results in an identical tree.
	pr, pw := io.Pipe()
	go func() {
		ndoc := renderHtmlNode(doc)
		pw.CloseWithError(html.Render(pw, ndoc))
	}()
	doc1, err := Parse(pr, anyImporter{})
	if err := checkErrors(err, expectedErrs); err != nil {
		return err
	}
	got1, err := dump(doc1)
	if err != nil {
		return err
	}
	if got != got1 {
		return fmt.Errorf("got vs got1:\n----\n%s----\n%s----", got, got1)
	}

	return nil
}

func simplifiedErrMsg(err error) string {
	var fileErr *file.Error
	if errors.As(err, &fileErr) {
		return fileErr.Message
	}
	return err.Error()
}

func checkErrors(err error, expected []string) error {
	if err == nil {
		return nil
	}
	errSlice := []error{err}
	if m, ok := err.(interface{ Unwrap() []error }); ok {
		errSlice = m.Unwrap()
	}
	// remove errors from the slice that are expected (per errs list)
	for _, expectedErr := range expected {
		i := slices.IndexFunc(errSlice, func(e error) bool {
			return simplifiedErrMsg(e) == expectedErr
		})
		if i >= 0 {
			errSlice = append(errSlice[:i], errSlice[i+1:]...)
		} else {
			gotErr := simplifiedErrMsg(errSlice[0])
			return fmt.Errorf("got error: %q, want %q", gotErr, expectedErr)
		}
	}
	if len(errSlice) > 0 {
		return fmt.Errorf("unexpected errors: %v", errSlice)
	}
	return nil
}

func TestParseForeignContentTemplates(t *testing.T) {
	srcs := []string{
		"<math><html><template><mn><template></template></template>",
		"<math><math><head><mi><template>",
	}
	for _, src := range srcs {
		// The next line shouldn't infinite-loop.
		_, _ = Parse(strings.NewReader(src), nil)
	}
}

func BenchmarkParser(b *testing.B) {
	buf, err := os.ReadFile("testdata/go1.html")
	if err != nil {
		b.Fatalf("could not read testdata/go1.html: %v", err)
	}
	b.SetBytes(int64(len(buf)))
	runtime.GC()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := Parse(bytes.NewBuffer(buf), nil)
		if err != nil {
			b.Fatalf("Parse: %v", err)
		}
	}
}

type anyImporter struct{}

func (anyImporter) Import(name string) (Component, error) {
	return cnil{}, nil
}

type cnil struct{}

func (cnil) Render(s Scope) (any, error) {
	return nil, nil
}
