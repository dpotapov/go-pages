package chtml

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
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
		{
			name: "attr manipulation",
			text: `<c:attr name="data">${ { num: 123 } }</c:attr>` +
				`<c:attr name="data2">${ data.num }</c:attr>` +
				`${data2 * 2}`,
			want: 246,
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
			text: `<c:attr name="my_map">${{}}</c:attr><ol><li c:for="val, key in my_map">${key}: ${val}</li></ol>`,
			vars: map[string]any{"my_map": map[string]string{"a": "apple", "b": "banana"}},
			want: `<ol><li>a: apple</li><li>b: banana</li></ol>`,
		},
		{
			name: "for loop over map (string keys, mixed values)",
			text: `<c:attr name="data">${{}}</c:attr><li c:for="v, k in data">${k}: ${v}</li>`,
			vars: map[string]any{"data": map[string]any{"name": "Go", "version": 1.22, "stable": true}},
			want: `<li>name: Go</li><li>stable: true</li><li>version: 1.22</li>`,
		},
		{
			name: "for loop over empty map",
			text: `<c:attr name="empty_map">${{}}</c:attr><p c:for="v, k in empty_map">${k}-${v}</p>`,
			vars: map[string]any{"empty_map": map[string]int{}},
			want: (*html.Node)(nil),
		},
		{
			name: "for loop over nil map",
			text: `<c:attr name="nil_map">${nil}</c:attr><span c:for="v, k in nil_map">${k}=${v}</span>`,
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

func TestCHTMLTypeMatching(t *testing.T) {
	err := testRenderCase(`<c:attr name="v">${true}</c:attr>${v ? 123 : 'text'}`, 123, map[string]any{"v": false}, &ComponentOptions{
		Importer:       nil,
		RenderComments: false,
	})
	require.ErrorContains(t, err, "cannot convert type string to type int")
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
		"comp1": `<p>comp1</p>`,
		"comp2": `<c:attr name="text">Hello</c:attr><p>${text}</p>`,
		"simple-page": `<c:attr name="title">Website</c:attr>` +
			`<html><head><title>${title}</title></head><body>${_}</body></html>`,
		"comp3": `<c:attr name="with-flag">${false}</c:attr>${with_flag ? "true" : "false"}`,
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
	htmlShape := (*html.Node)(nil)
	stringInstance := ""
	intInstance := 0
	int64Instance := int64(0)

	tests := []struct {
		name        string
		value       any
		shape       any
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
		{
			name:      "shape is untyped nil, value is string",
			value:     "hello",
			shape:     interface{}(nil),
			wantValue: "hello",
			wantErr:   false,
		},

		// 2. Value is nil (No changes needed for non-*html.Node shapes)
		{
			name:      "value is nil, shape is *html.Node", // Changed
			value:     nil,
			shape:     htmlShape,
			wantValue: (*html.Node)(nil), // nil converts to typed nil
			wantErr:   false,
		},
		{
			name:      "value is nil, shape is string (non-nillable)",
			value:     nil,
			shape:     stringInstance,
			wantValue: stringInstance,
			wantErr:   false,
		},

		// 3. Types are the same
		{
			name:      "string to string",
			value:     "hello",
			shape:     stringInstance,
			wantValue: "hello",
			wantErr:   false,
		},
		{
			name:      "*html.Node to *html.Node shape", // Changed
			value:     &html.Node{Type: html.CommentNode, Data: "comment"},
			shape:     htmlShape,
			wantValue: &html.Node{Type: html.CommentNode, Data: "comment"},
			wantErr:   false,
		},

		// 4. Convertible types (standard Go convertibility)
		{
			name:      "int to int64",
			value:     123,
			shape:     int64Instance,
			wantValue: int64(123),
			wantErr:   false,
		},
		{
			name:      "MyString to string",
			value:     MyString("hello"),
			shape:     stringInstance,
			wantValue: "hello",
			wantErr:   false,
		},

		// 5. Target is *html.Node
		{
			name:  "string to *html.Node",
			value: "text content",
			shape: htmlShape,
			wantValue: &html.Node{
				Type: html.TextNode,
				Data: "text content",
			},
			wantErr: false,
		},
		{
			name:  "bool (true) to *html.Node",
			value: true,
			shape: htmlShape,
			wantValue: &html.Node{
				Type: html.TextNode,
				Data: "true",
			},
			wantErr: false,
		},
		{
			name:  "int to *html.Node",
			value: 456,
			shape: htmlShape,
			wantValue: &html.Node{
				Type: html.TextNode,
				Data: "456",
			},
			wantErr: false,
		},
		{
			name:  "float64 to *html.Node",
			value: 7.89,
			shape: htmlShape,
			wantValue: &html.Node{
				Type: html.TextNode,
				Data: "7.89",
			},
			wantErr: false,
		},
		{
			name:  "[]byte to *html.Node",
			value: []byte("byte slice content"),
			shape: htmlShape,
			wantValue: &html.Node{
				Type: html.TextNode,
				Data: "byte slice content",
			},
			wantErr: false,
		},
		{
			name:      "nil []byte to *html.Node",
			value:     ([]byte)(nil),
			shape:     htmlShape,
			wantValue: (*html.Node)(nil),
			wantErr:   false,
		},
		{
			name:  "struct to *html.Node",
			value: testStruct{Name: "Go", Age: 15},
			shape: htmlShape,
			wantValue: &html.Node{
				Type: html.TextNode,
				Data: "{\"Name\":\"Go\",\"Age\":15}",
			},
			wantErr: false,
		},
		{
			name:  "slice of ints to *html.Node",
			value: []int{10, 20, 30},
			shape: htmlShape,
			wantValue: &html.Node{
				Type: html.TextNode,
				Data: "[10,20,30]",
			},
			wantErr: false,
		},
		{
			name:      "nil slice of ints to *html.Node",
			value:     ([]int)(nil),
			shape:     htmlShape,
			wantValue: (*html.Node)(nil),
			wantErr:   false,
		},
		{
			name:  "map[string]any to *html.Node",
			value: map[string]any{"key": "val", "num": 123.0, "active": true},
			shape: htmlShape,
			wantValue: &html.Node{
				Type: html.TextNode,
				Data: `{"active":true,"key":"val","num":123}`,
			},
			wantErr: false,
		},
		{
			name:      "nil map[string]any to *html.Node",
			value:     (map[string]any)(nil),
			shape:     htmlShape,
			wantValue: (*html.Node)(nil),
			wantErr:   false,
		},

		// 6. Non-convertible types (general error cases - these remain the same)
		{
			name:        "string to int",
			value:       "not-an-int",
			shape:       intInstance,
			wantValue:   nil,
			wantErr:     true,
			errContains: "cannot convert type string to type int",
		},
		{
			name:        "struct to string",
			value:       testStruct{Name: "Test", Age: 1},
			shape:       stringInstance,
			wantValue:   nil,
			wantErr:     true,
			errContains: "cannot convert type chtml.testStruct to type string",
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
				// If an error was expected and its content matches (or no specific content check),
				// and we indeed got an error, then the value check is skipped.
				if err != nil { // Ensure we actually got an error if wantErr is true
					return
				}
				// If wantErr is true but err is nil, the first check (err != nil) != tt.wantErr would have caught it.
			}

			// If no error was expected, but we got one
			if err != nil {
				t.Errorf("convertToRenderShape() unexpected error = %v", err)
				return
			}

			// General reflect.DeepEqual for other types or if one is *Node and other isn't
			if !reflect.DeepEqual(gotValue, tt.wantValue) {
				t.Errorf("DeepEqual mismatch.\nGot:      %#v (type %T)\nWant:     %#v (type %T)", gotValue, gotValue, tt.wantValue, tt.wantValue)
			}
		})
	}
}
