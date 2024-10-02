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
			if err := testRenderCase(tt.text, tt.want, nil); err != nil {
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
			name: "render c:for",
			text: `<ul><li c:for="item in ['a', 'b', 'c']">${ item }</li></ul>`,
			want: "<ul><li>a</li><li>b</li><li>c</li></ul>",
		},
		{
			name: "render nested c:for",
			text: `<ul c:for="i in [1, 2]"><li c:for="j in [3, 4]">${ i }-${ j }</li></ul>`,
			want: "<ul><li>1-3</li><li>1-4</li></ul><ul><li>2-3</li><li>2-4</li></ul>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := testRenderCase(tt.text, tt.want, nil); err != nil {
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
			name: "import with arg - another way",
			text: `<c:comp2><c:attr name="text">Hi</c:attr></c:comp2>`,
			want: `<p>Hi</p>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := testRenderCase(tt.text, tt.want, &ComponentOptions{
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

func testRenderCase(text, want string, opts *ComponentOptions) (err error) {
	var imp Importer
	if opts != nil {
		imp = opts.Importer
	}

	doc, err := Parse(strings.NewReader(text), imp)
	if err != nil {
		return err
	}

	comp := NewComponent(doc, opts)
	rr, err := comp.Render(NewBaseScope(map[string]any{}))
	if err != nil {
		return err
	}

	var buf strings.Builder
	switch rr := rr.(type) {
	case *html.Node:
		if err := html.Render(&buf, rr); err != nil {
			return err
		}
	case string:
		buf.WriteString(rr)
	default:
		if err := json.NewEncoder(&buf).Encode(rr); err != nil {
			return err
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
