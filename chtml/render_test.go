package chtml

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"
)

func TestRenderSimpleHTML(t *testing.T) {
	tests := []struct {
		name string
		text string
		want any
	}{
		{
			name: "empty",
			text: "",
			want: nil,
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
		want any
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
			want: 123,
		},
		{
			name: "eval basic data type - bool true",
			text: `${ true }`,
			want: true,
		},
		{
			name: "eval basic data type - bool false",
			text: `${ false }`,
			want: false,
		},
		{
			name: "eval basic data type - float",
			text: `${ 3.14 }`,
			want: 3.14,
		},
		{
			name: "eval basic data type - slice of strings",
			text: `${ ["a", "b", "c"] }`,
			want: []any{"a", "b", "c"},
		},
		{
			name: "eval basic data type - slice of numbers",
			text: `${ [1, 2, 3] }`,
			want: []any{1, 2, 3},
		},
		{
			name: "eval simple object",
			text: `${ {"a": 123, "b": true, "c": "str"} }`,
			want: map[string]any{"a": 123, "b": true, "c": "str"},
		},
		{
			name: "eval int within whitespace",
			text: `  ${ 123 }   `,
			want: 123,
		},
		{
			name: "eval bool within whitespace",
			text: `  ${ true }   `,
			want: true,
		},
		{
			name: "eval object within whitespace",
			text: `  ${ { "a": 123 } }   `,
			want: map[string]any{"a": 123},
		},
		{
			name: "eval string within whitespace",
			text: `  ${ "abc" }   `,
			want: "  abc   ",
		},
		{
			name: "text node expansion",
			text: `<c var="greeting">Hello</c><p>${ greeting }</p>`,
			want: "<p>Hello</p>",
		},
		{
			name: "text node expansion with external var",
			text: `<c var="greeting">Hello</c><p>${ greeting }</p>`,
			want: "<p>Hi</p>",
			vars: map[string]any{"greeting": "Hi"},
		},
		{
			name: "attr expansion",
			text: `<a><c:attr name="href">bar</c:attr>Link</a>`,
			want: `<a href="bar">Link</a>`,
		},
		{
			name: "c:attr root usage error",
			text: `<c:attr name="foo">bar</c:attr><p>ok</p>`,
			want: `<p>ok</p>`,
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
			want: (*html.Node)(nil),
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
			want: (*html.Node)(nil),
		},
		{
			name: "render c:for",
			text: `<ul><li c:for="item in ['a', 'b', 'c']">${ item }</li></ul>`,
			want: "<ul><li>a</li><li>b</li><li>c</li></ul>",
		},
		{
			name: "render c:for with words var",
			text: `<c var="words">${['foo', 'bar', 'baz']}</c><ul><li c:for="w in words">${w}</li></ul>`,
			want: `<ul><li>foo</li><li>bar</li><li>baz</li></ul>`,
		},
		{
			name: "render c:for with numbers var",
			text: `<c var="numbers">${[1,2,3]}</c><p c:for="i in numbers">${i}</p>`,
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
			want: (*html.Node)(nil),
		},
		{
			name: "render c:for with c:if",
			text: `<p c:for="x in ['foo']" c:if="true">${x}</p>`,
			want: `<p>foo</p>`,
		},
		{
			name: "render c:for with variable on the same element",
			text: `<div c:for="n in [1, 2, 3]" id="block-${n}"><p>${n}</p></div>`,
			want: `<div id="block-1"><p>1</p></div><div id="block-2"><p>2</p></div><div id="block-3"><p>3</p></div>`,
		},
		{
			name: "for loop over map (string keys, string values)",
			text: `<c var="my_map">${{}}</c><ol><li c:for="val, key in my_map">${key}: ${val}</li></ol>`,
			vars: map[string]any{"my_map": map[string]string{"a": "apple", "b": "banana"}},
			want: `<ol><li>a: apple</li><li>b: banana</li></ol>`,
		},
		{
			name: "for loop over map (string keys, mixed values)",
			text: `<c var="data">${{}}</c><li c:for="v, k in data">${k}: ${v}</li>`,
			vars: map[string]any{"data": map[string]any{"name": "Go", "version": 1.22, "stable": true}},
			want: `<li>name: Go</li><li>stable: true</li><li>version: 1.22</li>`,
		},
		{
			name: "for loop over empty map",
			text: `<c var="empty_map">${{}}</c><p c:for="v, k in empty_map">${k}-${v}</p>`,
			vars: map[string]any{"empty_map": map[string]int{}},
			want: (*html.Node)(nil),
		},
		{
			name: "for loop over nil map",
			text: `<c var="nil_map">${nil}</c><span c:for="v, k in nil_map">${k}=${v}</span>`,
			want: (*html.Node)(nil),
		},

		// Testing rendering <input checked> and <option selected>
		{
			name: "render input checked with bool",
			text: `<input type="checkbox" checked="${true}"><input type="checkbox" checked="${false}">`,
			want: `<input type="checkbox" checked="true"/><input type="checkbox"/>`,
		},
		{
			name: "render input checked with string",
			text: `<input type="checkbox" checked="${'checked'}"><input type="checkbox" checked="${''}">`,
			want: `<input type="checkbox" checked="checked"/><input type="checkbox" checked=""/>`,
		},
		{
			name: "render input checked with int",
			text: `<input type="checkbox" checked="${999}"><input type="checkbox" checked="${0}">`,
			want: `<input type="checkbox" checked="999"/><input type="checkbox"/>`,
		},
		{
			name: "render option selected with bool",
			text: `<option selected="${true}"/><option selected="${false}"/>`,
			want: `<option selected="true"></option><option></option>`,
		},
		{
			name: "render option selected with string",
			text: `<option selected="${'selected'}"/><option selected="${''}"/>`,
			want: `<option selected="selected"></option><option selected=""></option>`,
		},
		{
			name: "render option selected with int",
			text: `<option selected="${999}"/><option selected="${0}"/>`,
			want: `<option selected="999"></option><option></option>`,
		},
		{
			name: "render checked and selected on non-input/option element",
			text: `<div checked="${true}" selected="${true}"/>`,
			want: `<div checked="true" selected="true"></div>`,
		},
		{
			name: "render checked and selected on non-input/option element",
			text: `<div checked="${false}" selected="${false}"/>`,
			want: `<div checked="false" selected="false"></div>`, // expect no special handling
		},
		{
			name: "render disabled attribute for button",
			text: `<button disabled="${true}">A</button><button disabled="${false}">B</button><button disabled="${0}">C</button><button disabled="${1}">D</button>`,
			want: `<button disabled="true">A</button><button>B</button><button>C</button><button disabled="1">D</button>`,
		},
		{
			name: "render disabled attribute for fieldset",
			text: `<fieldset disabled="${true}">A</fieldset><fieldset disabled="${false}">B</fieldset>`,
			want: `<fieldset disabled="true">A</fieldset><fieldset>B</fieldset>`,
		},
		{
			name: "render disabled attribute for optgroup",
			text: `<optgroup disabled="${true}"></optgroup><optgroup disabled="${false}"></optgroup>`,
			want: `<optgroup disabled="true"></optgroup><optgroup></optgroup>`,
		},
		{
			name: "render disabled attribute for option",
			text: `<option disabled="${true}"></option><option disabled="${false}"></option>`,
			want: `<option disabled="true"></option><option></option>`,
		},
		{
			name: "render disabled attribute for select",
			text: `<select disabled="${true}"></select><select disabled="${false}"></select>`,
			want: `<select disabled="true"></select><select></select>`,
		},
		{
			name: "render disabled attribute for textarea",
			text: `<textarea disabled="${true}"></textarea><textarea disabled="${false}"></textarea>`,
			want: `<textarea disabled="true"></textarea><textarea></textarea>`,
		},
		{
			name: "render disabled attribute for input",
			text: `<input disabled="${true}"/><input disabled="${false}"/>`,
			want: `<input disabled="true"/><input/>`,
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

func TestRenderCElement(t *testing.T) {
	tests := []struct {
		name string
		text string
		want any
		vars map[string]any
	}{
		// Passthrough mode tests
		{
			name: "c passthrough with single element",
			text: `<c><p>Hello</p></c>`,
			want: `<p>Hello</p>`,
		},
		{
			name: "c passthrough with multiple elements",
			text: `<c><h1>Title</h1><p>Content</p></c>`,
			want: `<h1>Title</h1><p>Content</p>`,
		},
		{
			name: "c passthrough with text content",
			text: `<c>Plain text</c>`,
			want: `Plain text`,
		},
		{
			name: "c passthrough with mixed content",
			text: `<c><strong>Bold</strong> and plain text</c>`,
			want: `<strong>Bold</strong> and plain text`,
		},
		{
			name: "empty c element",
			text: `<c></c>`,
			want: nil,
		},
		{
			name: "nested c elements",
			text: `<c><c><span>Nested</span></c></c>`,
			want: `<span>Nested</span>`,
		},

		// Variable binding tests
		{
			name: "c var binding basic",
			text: `<c var="test"><p>Hello</p></c><div>${test}</div>`,
			want: `<div><p>Hello</p></div>`,
		},
		{
			name: "c var binding with text",
			text: `<c var="greeting">Hello World</c><p>${greeting}</p>`,
			want: `<p>Hello World</p>`,
		},
		{
			name: "c var binding with multiple elements",
			text: `<c var="content"><h1>Title</h1><p>Text</p></c><div>${content}</div>`,
			want: `<div><h1>Title</h1><p>Text</p></div>`,
		},
		{
			name: "c var binding with expression",
			text: `<c var="result">${123 + 456}</c><p>${result}</p>`,
			want: `<p>579</p>`,
		},
		{
			name: "c var reuse",
			text: `<c var="item"><li>Item</li></c><ul>${item}${item}</ul>`,
			want: `<ul><li>Item</li><li>Item</li></ul>`,
		},

		// Loop tests
		{
			name: "c for loop basic",
			text: `<c for="i in [1,2,3]"><p>${i}</p></c>`,
			want: `<p>1</p><p>2</p><p>3</p>`,
		},
		{
			name: "c for loop with var",
			text: `<c var="items" for="i in [1,2]"><li>${i}</li></c><ul>${items}</ul>`,
			want: `<ul><li>1</li><li>2</li></ul>`,
		},
		{
			name: "c for loop with index",
			text: `<c for="val, idx in ['a','b']"><p>${idx}: ${val}</p></c>`,
			want: `<p>0: a</p><p>1: b</p>`,
		},
		{
			name: "c for loop over empty array",
			text: `<c for="i in []"><p>${i}</p></c>`,
			want: (*html.Node)(nil),
		},
		{
			name: "c for loop with external variable",
			text: `<c for="i in cast(items, [string])"><span>${i}</span></c>`,
			want: `<span>x</span><span>y</span><span>z</span>`,
			vars: map[string]any{"items": []string{"x", "y", "z"}},
		},

		// Conditional tests
		{
			name: "c if true",
			text: `<c if="true"><p>Shown</p></c>`,
			want: `<p>Shown</p>`,
		},
		{
			name: "c if false",
			text: `<c if="false"><p>Hidden</p></c>`,
			want: (*html.Node)(nil),
		},
		{
			name: "c if-else true",
			text: `<c if="true"><p>True</p></c><c else><p>False</p></c>`,
			want: `<p>True</p>`,
		},
		{
			name: "c if-else false",
			text: `<c if="false"><p>True</p></c><c else><p>False</p></c>`,
			want: `<p>False</p>`,
		},
		{
			name: "c if-else-if chain",
			text: `<c if="false"><p>A</p></c><c else-if="true"><p>B</p></c><c else><p>C</p></c>`,
			want: `<p>B</p>`,
		},
		{
			name: "c if-else-if all false",
			text: `<c if="false"><p>A</p></c><c else-if="false"><p>B</p></c><c else><p>C</p></c>`,
			want: `<p>C</p>`,
		},
		{
			name: "c conditional with var",
			text: `<c var="result" if="condition"><p>Conditional content</p></c><div>${result}</div>`,
			want: `<div><p>Conditional content</p></div>`,
			vars: map[string]any{"condition": true},
		},
		{
			name: "c conditional with var false",
			text: `<c var="result" if="false"><p>Hidden</p></c><div>${result}</div>`,
			want: `<div></div>`,
		},

		// Complex combinations
		{
			name: "nested c with var and loops",
			text: `<c var="list"><c for="i in [1,2]"><li>${i}</li></c></c><ul>${list}</ul>`,
			want: `<ul><li>1</li><li>2</li></ul>`,
		},
		{
			name: "c with dynamic content",
			text: `<c var="greeting">Hello ${name}!</c><p>${greeting}</p>`,
			want: `<p>Hello World!</p>`,
			vars: map[string]any{"name": "World"},
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
		want    any
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
			name:    "import with arg of wrong type",
			text:    `<c:comp2 text="${true}" />`,
			want:    `<p>true</p>`,
			wantErr: nil, // bool coerces to string as per ShapeString
		},
		{
			name: "import with default arg",
			text: `<c:simple-page><h1>Header</h1></c:simple-page>`,
			want: `<html><head><title>Website</title></head><body><h1>Header</h1></body></html>`,
		},
		{
			name: "import with custom arg",
			text: `<c:simple-page title="GoPages"><div><p>Lorem ipsum</p></div></c:simple-page>`,
			want: `<html><head><title>GoPages</title></head><body><div><p>Lorem ipsum</p></div></body></html>`,
		},
		{
			name: "bool kebab-flag attr - unset",
			text: `<c:comp3 />`,
			want: `false`,
		},
		{
			name: "bool kebab-flag attr with implied true value",
			text: `<c:comp3 with-flag />`,
			want: `true`,
		},
		{
			name: "bool kebab-flag attr with false value",
			text: `<c:comp3 with-flag="${false}" />`,
			want: `false`,
		},
		{
			name: "bool kebab-flag attr with true value",
			text: `<c:comp3 with-flag="${true}" />`,
			want: `true`,
		},
		{
			name:    "bool kebab-flag attr with string value",
			text:    `<c:comp3 with-flag="true" />`,
			wantErr: &DecodeError{Key: "with_flag"},
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

func testRenderCase(text string, want any, vars map[string]any, opts *ComponentOptions) (err error) {
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

	if ht, ok := rr.(*html.Node); ok && ht != nil {
		var buf strings.Builder
		if err := html.Render(&buf, ht); err != nil {
			return fmt.Errorf("html render error: %w", err)
		}
		rr = buf.String()
	}

	if diff := cmp.Diff(rr, want); diff != "" {
		return fmt.Errorf("got vs want:\n%s", diff)
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
		"comp1": `<p>comp1</p>`,                          // produces html, accepts no args
		"comp2": `<c var="text">Hello</c><p>${text}</p>`, // accepts text arg of type string
		"simple-page": `<c var="title">Website</c>` +
			`<html><head><title>${title}</title></head><body>${_}</body></html>`,
		"comp3": `<c var="with_flag">${false}</c>${with_flag ? "true" : "false"}`,
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

// MyString is a custom string type for testing convertibility.
type MyString string

type testStruct struct {
	Name string
	Age  int
}

func TestConvertToRenderShape(t *testing.T) {
	tests := []struct {
		name        string
		value       any
		shape       *Shape
		wantValue   any
		wantErr     bool
		errContains string
	}{
		// 1. Shape is nil (No changes needed here)
		{
			name:      "shape is nil, value is string",
			value:     "hello",
			shape:     nil,
			wantValue: "hello",
			wantErr:   false,
		},

		// 2. Value is nil (No changes needed for non-*html.Node shapes)
		{
			name:      "value is nil, shape is html",
			value:     nil,
			shape:     Html,
			wantValue: (*html.Node)(nil),
			wantErr:   false,
		},
		{
			name:      "value is nil, shape is string (non-nillable)",
			value:     nil,
			shape:     String,
			wantValue: "",
			wantErr:   false,
		},

		// 3. Types are the same
		{
			name:      "string to string",
			value:     "hello",
			shape:     String,
			wantValue: "hello",
			wantErr:   false,
		},
		{
			name:      "*html.Node to html shape",
			value:     &html.Node{Type: html.CommentNode, Data: "comment"},
			shape:     Html,
			wantValue: &html.Node{Type: html.CommentNode, Data: "comment"},
			wantErr:   false,
		},

		// 4. Convertible types (standardized for ShapeNumber)
		{
			name:      "int to number",
			value:     123,
			shape:     Number,
			wantValue: 123,
			wantErr:   false,
		},

		// 5. Target is html
		{
			name:  "string to html",
			value: "text content",
			shape: Html,
			wantValue: &html.Node{
				Type: html.TextNode,
				Data: "text content",
			},
			wantErr: false,
		},
		{
			name:  "bool (true) to html",
			value: true,
			shape: Html,
			wantValue: &html.Node{
				Type: html.TextNode,
				Data: "true",
			},
			wantErr: false,
		},
		{
			name:  "int to html",
			value: 456,
			shape: Html,
			wantValue: &html.Node{
				Type: html.TextNode,
				Data: "456",
			},
			wantErr: false,
		},
		{
			name:  "float64 to html",
			value: 7.89,
			shape: Html,
			wantValue: &html.Node{
				Type: html.TextNode,
				Data: "7.89",
			},
			wantErr: false,
		},
		{
			name:  "[]byte to html",
			value: []byte("byte slice content"),
			shape: Html,
			wantValue: &html.Node{
				Type: html.TextNode,
				Data: "byte slice content",
			},
			wantErr: false,
		},
		{
			name:  "map[string]any to html",
			value: map[string]any{"key": "val", "num": 123.0, "active": true},
			shape: Html,
			wantValue: &html.Node{
				Type: html.TextNode,
				Data: `{"active":true,"key":"val","num":123}`,
			},
			wantErr: false,
		},
		{
			name:      "nil []byte to html",
			value:     ([]byte)(nil),
			shape:     Html,
			wantValue: (*html.Node)(nil),
			wantErr:   false,
		},
		{
			name:  "struct to html",
			value: testStruct{Name: "Go", Age: 15},
			shape: Html,
			wantValue: &html.Node{
				Type: html.TextNode,
				Data: "{\"Name\":\"Go\",\"Age\":15}",
			},
			wantErr: false,
		},
		{
			name:  "slice of ints to html",
			value: []int{10, 20, 30},
			shape: Html,
			wantValue: &html.Node{
				Type: html.TextNode,
				Data: "[10,20,30]",
			},
			wantErr: false,
		},
		{
			name:      "nil slice of ints to html",
			value:     ([]int)(nil),
			shape:     Html,
			wantValue: (*html.Node)(nil),
			wantErr:   false,
		},

		// 6. Non-convertible types with ShapeNumber/String
		{
			name:        "string to number (error)",
			value:       "not-a-number",
			shape:       Number,
			wantValue:   nil,
			wantErr:     true,
			errContains: `cannot convert string "not-a-number" to number`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotValue, err := convertToRenderShape(tt.value, tt.shape)

			if (err != nil) != tt.wantErr {
				t.Errorf("convertToRenderShape() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("convertToRenderShape() error = %q, want error containing %q", err.Error(), tt.errContains)
				}
				if err != nil {
					return
				}
			}

			if err != nil {
				t.Errorf("convertToRenderShape() unexpected error = %v", err)
				return
			}

			if !reflect.DeepEqual(gotValue, tt.wantValue) {
				t.Errorf("DeepEqual mismatch.\nGot:      %#v (type %T)\nWant:     %#v (type %T)", gotValue, gotValue, tt.wantValue, tt.wantValue)
			}
		})
	}
}

func TestCElementTypeCasting(t *testing.T) {
	tests := []struct {
		name     string
		template string
		vars     map[string]any
		want     string
		wantErr  bool
		errMsg   string
	}{
		// Basic type casting
		{
			name:     "cast string to number",
			template: `<c var="num number">123</c><p>${num}</p>`,
			vars:     nil,
			want:     `<p>123</p>`,
		},
		{
			name:     "cast expression to string",
			template: `<c var="text string">${123 + 456}</c><p>${text}</p>`,
			vars:     nil,
			want:     `<p>579</p>`,
		},
		{
			name:     "cast boolean expression",
			template: `<c var="flag bool">${true}</c><p>${flag ? "yes" : "no"}</p>`,
			vars:     nil,
			want:     `<p>yes</p>`,
		},

		// Object type casting
		{
			name:     "cast to object type",
			template: `<c var="obj {name: string, age: number}">${{name: "John", age: 30}}</c><p>${obj.name}</p>`,
			vars:     nil,
			want:     `<p>John</p>`,
		},
		{
			name:     "cast nil to object type",
			template: `<c var="obj {name: string, age: number}">${nil}</c><p>${obj.name}</p>`,
			vars:     nil,
			want:     `<p></p>`,
		},
		{
			name:     "typed var defaults when nested conditional false",
			template: `<c var="myvar {param1: string, param2: bool}"><c if="false">${{param1: "abc", param2: true}}</c></c><p>${myvar.param1}|${myvar.param2}</p>`,
			vars:     nil,
			want:     `<p>|false</p>`,
		},
		{
			name:     "typed var uses data when nested conditional true",
			template: `<c var="myvar {param1: string, param2: bool}"><c if="true">${{param1: "abc", param2: true}}</c></c><p>${myvar.param1}|${myvar.param2}</p>`,
			vars:     nil,
			want:     `<p>abc|true</p>`,
		},

		// Array type casting
		{
			name:     "cast to array type",
			template: `<c var="nums [number]">${[1, 2, 3]}</c><p c:for="n in nums">${n}</p>`,
			vars:     nil,
			want:     `<p>1</p><p>2</p><p>3</p>`,
		},

		// Backward compatibility - existing syntax should work
		{
			name:     "backward compatible - no type",
			template: `<c var="greeting">Hello</c><p>${greeting}</p>`,
			vars:     nil,
			want:     `<p>Hello</p>`,
		},

		// Error cases
		{
			name:     "incompatible type conversion",
			template: `<c var="num number">not_a_number</c><p>${num}</p>`,
			vars:     nil,
			wantErr:  true,
			errMsg:   "cannot convert",
		},
		{
			name:     "invalid type syntax",
			template: `<c var="invalid invalid_type">123</c>`,
			vars:     nil,
			wantErr:  true,
			errMsg:   "invalid type literal",
		},
		{
			name:     "empty var name",
			template: `<c var="">123</c>`,
			vars:     nil,
			wantErr:  true,
			errMsg:   "var attribute cannot be empty",
		},
		{
			name:     "invalid var name",
			template: `<c var="123invalid">123</c>`,
			vars:     nil,
			wantErr:  true,
			errMsg:   "var name must be a valid identifier",
		},

		// Complex type casting scenarios
		{
			name:     "nested object type",
			template: `<c var="user {profile: {name: string, age: number}}">${{profile: {name: "Alice", age: 25}}}</c><p>${user.profile.name}</p>`,
			vars:     nil,
			want:     `<p>Alice</p>`,
		},
		{
			name:     "array of objects",
			template: `<c var="users [{name: string}]">${[{name: "John"}, {name: "Jane"}]}</c><p c:for="u in users">${u.name}</p>`,
			vars:     nil,
			want:     `<p>John</p><p>Jane</p>`,
		},
		{
			name:     "html content type",
			template: `<c var="content html"><span>Text</span>${123}</c><div>${content}</div>`,
			vars:     nil,
			want:     `<div><span>Text</span>123</div>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := Parse(strings.NewReader(tt.template), nil)

			if tt.wantErr {
				if err == nil {
					comp := NewComponent(doc, nil)
					scope := NewBaseScope(tt.vars)
					_, err = comp.Render(scope)
				}
				if err == nil {
					t.Errorf("expected error but got none")
					return
				}
				if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("error = %q, want error containing %q", err.Error(), tt.errMsg)
				}
				return
			}

			if err != nil {
				t.Errorf("parse error: %v", err)
				return
			}

			comp := NewComponent(doc, nil)
			scope := NewBaseScope(tt.vars)
			result, err := comp.Render(scope)
			if err != nil {
				t.Errorf("render error: %v", err)
				return
			}

			var got string
			switch r := result.(type) {
			case string:
				got = r
			case *html.Node:
				buf := &bytes.Buffer{}
				err := html.Render(buf, r)
				if err != nil {
					t.Errorf("html render error: %v", err)
					return
				}
				got = buf.String()
			default:
				t.Errorf("unexpected result type: %T", result)
				return
			}

			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
