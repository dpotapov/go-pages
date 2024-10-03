package chtml

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestRenderSimpleHTML(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "empty",
			text: "",
			want: "",
		},
		{
			name: "simple",
			text: "Hello World",
			want: "Hello World",
		},
		{
			name: "header auto close",
			text: "<h1>Lorem ipsum<h2>dolor sit amet",
			want: "<h1>Lorem ipsum</h1><h2>dolor sit amet</h2>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := testRenderCase(tt.text, tt.want, nil, nil); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestRenderCHTML(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
		vars map[string]any
	}{
		{
			name: "eval basic data type - string",
			text: `${ "abc" }`,
			want: "abc",
		},
		{
			name: "eval basic data type - int",
			text: `${ 123 }`,
			want: "123",
		},
		{
			name: "eval basic data type - bool true",
			text: `${ true }`,
			want: "true",
		},
		{
			name: "eval basic data type - bool false",
			text: `${ false }`,
			want: "false",
		},
		{
			name: "eval basic data type - float",
			text: `${ 3.14 }`,
			want: "3.14",
		},
		{
			name: "eval basic data type - slice of strings",
			text: `${ ["a", "b", "c"] }`,
			want: `["a","b","c"]`,
		},
		{
			name: "eval basic data type - slice of numbers",
			text: `${ [1, 2, 3] }`,
			want: "[1,2,3]",
		},
		{
			name: "text node expansion",
			text: `<c:attr name="greeting">Hello</c:attr><p>${ greeting }</p>`,
			want: "<p>Hello</p>",
		},
		{
			name: "text node expansion",
			text: `<c:attr name="greeting">Hello</c:attr><p>${ greeting }</p>`,
			want: "<p>Hi</p>",
			vars: map[string]any{"greeting": "Hi"},
		},
		{
			name: "attr expansion",
			text: `<c:attr name="foo">bar</c:attr><a href="${foo}">Link</a>`,
			want: `<a href="bar">Link</a>`,
		},

		// Testing conditionals (c:if, c:else-if, c:else)
		{
			name: "render c:if - true",
			text: `<p c:if="true">foobar</p>`,
			want: "<p>foobar</p>",
		},
		{
			name: "render c:if - false",
			text: `<p c:if="false">foobar</p>`,
			want: "",
		},
		{
			name: "render c:if - empty",
			text: `<p c:if="">foobar</p>`,
			want: "<p>foobar</p>",
		},
		{
			name: "render c:if-else",
			text: `<p c:if="true">OK</p><p c:else>NOTOK</p>`,
			want: `<p>OK</p>`,
		},
		{
			name: "render c:if-else",
			text: `<p c:if="false">NOTOK</p><p c:else>OK</p>`,
			want: `<p>OK</p>`,
		},
		{
			name: "render if-if-else",
			text: `<p c:if="true">OK</p><p c:if="false">NOTOK</p><p c:else>OK</p>`,
			want: `<p>OK</p><p>OK</p>`,
		},
		{
			name: "render if-if-else",
			text: `<p c:if="false">NOTOK1</p><p c:if="true">OK</p><p c:else>NOTOK2</p>`,
			want: `<p>OK</p>`,
		},
		{
			name: "render if-if-else",
			text: `<p c:if="false">NOTOK</p><p c:if="false">NOTOK</p><p c:else>OK</p>`,
			want: `<p>OK</p>`,
		},
		{
			name: "render if-elif-else",
			text: `<p c:if="true">OK</p><p c:else-if="false">NOTOK</p><p c:else>NOTOK</p>`,
			want: `<p>OK</p>`,
		},
		{
			name: "render if-elif-else",
			text: `<p c:if="false">NOTOK</p><p c:else-if="true">OK</p><p c:else>NOTOK</p>`,
			want: `<p>OK</p>`,
		},
		{
			name: "render if-elif-else",
			text: `<p c:if="false">NOTOK</p><p c:else-if="false">NOTOK</p><p c:else>OK</p>`,
			want: `<p>OK</p>`,
		},


		// Testing loops (c:for)
		{
			name: "render c:for - empty",
			text: `<p c:for="x in []">Hello, ${x}!</p>`,
		},
		{
			name: "render c:for",
			text: `<ul><li c:for="item in ['a', 'b', 'c']">${ item }</li></ul>`,
			want: "<ul><li>a</li><li>b</li><li>c</li></ul>",
		},
		{
			name: "render c:for with words var",
			text: `<c:attr name="words">${['foo', 'bar', 'baz']}</c:attr><ul><li c:for="w in words">${w}</li></ul>`,
			want: `<ul><li>foo</li><li>bar</li><li>baz</li></ul>`,
		},
		{
			name: "render c:for with numbers var",
			text: `<c:attr name="numbers">${[1,2,3]}</c:attr><p c:for="i in numbers">${i}</p>`,
			want: `<p>1</p><p>2</p><p>3</p>`,
		},
		{
			name: "render nested c:for",
			text: `<ul c:for="i in [1, 2]"><li c:for="j in [3, 4]">${ i }-${ j }</li></ul>`,
			want: "<ul><li>1-3</li><li>1-4</li></ul><ul><li>2-3</li><li>2-4</li></ul>",
		},
		{
			name: "render c:for with c:if",
			text: `<p c:for="x in ['foo']" c:if="false">${x}</p>`,
		},
		{
			name: "render c:for with c:if",
			text: `<p c:for="x in ['foo']" c:if="true">${x}</p>`,
			want: `<p>foo</p>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := testRenderCase(tt.text, tt.want, tt.vars, nil); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestRenderCHTMLImports(t *testing.T) {
	imp := &testImporter{}
	imp.init()

	tests := []struct {
		name    string
		text    string
		want    string
		wantErr error
	}{
		{
			name:    "unknown import",
			text:    `<c:wrong-name />`,
			want:    "",
			wantErr: ErrComponentNotFound,
		},
		{
			name: "simple import",
			text: `<c:comp1 />`,
			want: "<p>comp1</p>",
		},
		{
			name:    "bad arg",
			text:    `<c:comp1 text="Hi" />`,
			wantErr: &UnrecognizedArgumentError{Name: "text"},
		},
		{
			name: "import with arg",
			text: `<c:comp2 text="Hi" />`,
			want: `<p>Hi</p>`,
		},
		{
			name: "define simple attr",
			text: `<c:attr name="text">Hi</c:attr>${text}`,
			want: `Hi`,
		},
		{
			name: "import with nested attr",
			text: `<c:comp2><c:attr name="text">Hi</c:attr></c:comp2>`,
			want: `<p>Hi</p>`,
		},
		{
			name: "import with default attr",
			text: `<c:simple-page><h1>Header</h1></c:simple-page>`,
			want: `<html><head><title>Website</title></head><body><h1>Header</h1></body></html>`,
		},
		{
			name: "import with multiple attrs",
			text: `<c:attr name="page_title">GoPages</c:attr>` +
				`<c:attr name="page_content"><p>Lorem ipsum</p></c:attr>` +
				`<c:simple-page title="${page_title}"><div>${page_content}</div></c:simple-page>`,
			want: `<html><head><title>GoPages</title></head><body><div><p>Lorem ipsum</p></div></body></html>`,
		},
		{
			name: "re-use html attr",
			text: `<c:attr name="content"><p>Lorem ipsum</p></c:attr>${content}${content}`,
			want: `<p>Lorem ipsum</p><p>Lorem ipsum</p>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := testRenderCase(tt.text, tt.want, nil, &ComponentOptions{
				Importer:       imp,
				RenderComments: false,
			}); err != nil {
				if tt.wantErr != nil {
					if !errors.Is(err, tt.wantErr) {
						t.Errorf("got %q want %q", err.Error(), tt.wantErr)
					}
				} else {
					t.Error(err)
				}
			}
		})
	}
}

func testRenderCase(text, want string, vars map[string]any, opts *ComponentOptions) (err error) {
	var imp Importer
	if opts != nil {
		imp = opts.Importer
	}

	doc, err := Parse(strings.NewReader(text), imp)
	if err != nil {
		return fmt.Errorf("parse error: %w", err)
	}

	comp := NewComponent(doc, opts)

	if vars == nil {
		vars = make(map[string]any)
	}
	s := NewBaseScope(vars)

	rr, err := comp.Render(s)
	if err != nil {
		return fmt.Errorf("render error: %w", err)
	}

	var buf strings.Builder
	switch rr := rr.(type) {
	case *html.Node:
		if err := html.Render(&buf, rr); err != nil {
			return fmt.Errorf("html render error: %w", err)
		}
	case string:
		buf.WriteString(rr)
	default:
		if err := json.NewEncoder(&buf).Encode(rr); err != nil {
			return fmt.Errorf("json encode error: %w", err)
		}
	}

	// Compare the parsed tree to the #document section.
	got := buf.String()
	if got != want {
		return fmt.Errorf("got vs want:\n----\n%s\n----\n%s\n----", got, want)
	}

	return nil
}

type testImporter struct {
	parsedComps map[string]*Node
}

var _ Importer = (*testImporter)(nil)

func (t *testImporter) init() {
	if t.parsedComps != nil {
		return
	}

	comps := map[string]string{
		"comp1": `<p>comp1</p>`,
		"comp2": `<c:attr name="text">Hello</c:attr><p>${text}</p>`,
		"simple-page": `<c:attr name="title">Website</c:attr>` +
			`<html><head><title>${title}</title></head><body>${_}</body></html>`,
	}

	t.parsedComps = make(map[string]*Node)
	for name, text := range comps {
		doc, err := Parse(strings.NewReader(text), nil)
		if err != nil {
			panic(err)
		}
		t.parsedComps[name] = doc
	}

}

func (t *testImporter) Import(name string) (Component, error) {
	if doc, ok := t.parsedComps[name]; ok {
		return NewComponent(doc, nil), nil
	}

	return nil, ErrComponentNotFound
}
