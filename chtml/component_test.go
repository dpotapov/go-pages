package chtml

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/beevik/etree"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/html"
)

func TestParse(t *testing.T) {
	s := `
		<c:arg name="link1">http://foo.bar</c:arg>
		<p>Links:</p>
		<ul>
			<li><a href="foo">Foo</a></li>
			<li><a href="/bar/baz">BarBaz</a></li>
		</ul>`
	r := strings.NewReader(s)
	comp, err := Parse(r, nil)
	if err != nil {
		t.Errorf("Parse() error = %v", err)
		return
	}
	c := comp.(*chtmlComponent)

	// Check args:
	require.Equal(t, 2, len(c.args))
	require.Equal(t, nil, c.args["_"])
	require.IsType(t, []etree.Token{}, c.args["link1"])

	// Check links:
	require.Len(t, c.doc.Element.ChildElements(), 3)
	ul := c.doc.Element.SelectElement("ul")
	require.NotNil(t, ul)
	require.Len(t, ul.ChildElements(), 2)
}

func Test_parseLoopExpr(t *testing.T) {
	tests := []struct {
		name     string
		s        string
		wantV    string
		wantK    string
		wantExpr string
		wantErr  bool
	}{
		{"empty", "", "", "", "", true},
		{"basic", "x in y", "x", "", "y", false},
		{"kv", "x, idx in y", "x", "idx", "y", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotV, gotK, gotExpr, err := parseLoopExpr(tt.s)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseLoopExpr() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if gotV != tt.wantV {
				t.Errorf("parseLoopExpr() gotV = %v, want %v", gotV, tt.wantV)
			}
			if gotK != tt.wantK {
				t.Errorf("parseLoopExpr() gotK = %v, want %v", gotK, tt.wantK)
			}
			if gotExpr != tt.wantExpr {
				t.Errorf("parseLoopExpr() gotExpr = %v, want %v", gotExpr, tt.wantExpr)
			}
		})
	}
}

func importFunc(name string) (Component, error) {
	s := ""
	switch strings.ToLower(name) {
	case "hello-world":
		s = "Hello, World!"
	case "greeting":
		s = `Hello, ${_}!`
	case "simple-page":
		s = `<c:arg name="title">NoTitle</c:arg><h1>${title}</h1><div>${_}</div>`
	default:
		return nil, fmt.Errorf("unknown component %q", name)
	}
	return Parse(strings.NewReader(s), ImporterFunc(importFunc))
}

func TestComponent_ParseAndRender(t *testing.T) {
	env := map[string]any{
		"foo":     "bar",
		"words":   []string{"foo", "bar", "baz"},
		"numbers": []int{1, 2, 3},
	}
	tests := []struct {
		name     string
		template string
		output   string
		wantErr  error
	}{
		{"empty", "", "", nil},
		{"helloWorld", "Hello, world!", "Hello, world!", nil},
		{"textNodeExpansion", `<c:arg name="foo">bar</c:arg>Hello, ${foo}!`, "Hello, bar!", nil},
		{"textNodeExpansion2", `<c:arg name="foo">bar</c:arg><p>Hello, ${foo}!</p>`, "<p>Hello, bar!</p>", nil},
		{"attrExpansion", `<c:arg name="foo">bar</c:arg><a href="${foo}">Link</a>`, `<a href="bar">Link</a>`, nil},

		// Testing conditionals (c:if, c:else-if, c:else)
		{"ifTrue", `<p c:if="true">Hello, world!</p>`, `<p>Hello, world!</p>`, nil},
		{"ifFalse", `<p c:if="false">Hello, world!</p>`, ``, nil},
		{"ifElse1", `<p c:if="true">OK</p><p c:else>NOTOK</p>`, `<p>OK</p>`, nil},
		{"ifElse2", `<p c:if="false">NOTOK</p><p c:else>OK</p>`, `<p>OK</p>`, nil},
		{"ifIfElse1",
			`<p c:if="true">OK</p><p c:if="false">NOTOK</p><p c:else>OK</p>`, `<p>OK</p><p>OK</p>`, nil},
		{"ifIfElse2",
			`<p c:if="false">NOTOK1</p><p c:if="true">OK</p><p c:else>NOTOK2</p>`, `<p>OK</p>`, nil},
		{"ifIfElse3",
			`<p c:if="false">NOTOK</p><p c:if="false">NOTOK</p><p c:else>OK</p>`, `<p>OK</p>`, nil},
		{"ifElifElse1",
			`<p c:if="true">OK</p><p c:else-if="false">NOTOK</p><p c:else>NOTOK</p>`, `<p>OK</p>`, nil},
		{"ifElifElse2",
			`<p c:if="false">NOTOK</p><p c:else-if="true">OK</p><p c:else>NOTOK</p>`, `<p>OK</p>`, nil},
		{"ifElifElse3",
			`<p c:if="false">NOTOK</p><p c:else-if="false">NOTOK</p><p c:else>OK</p>`, `<p>OK</p>`, nil},

		// Testing loops (c:for)
		{"forEmpty", `<p c:for="x in []">Hello, ${x}!</p>`, ``, nil},
		{"forOne", `<p c:for="x in ['foo']">${x}</p>`, `<p>foo</p>`, nil},
		{"forTwo", `<p c:for="x in ['foo', 'bar']">${x}</p>`, `<p>foo</p><p>bar</p>`, nil},
		{"forWords", `<c:arg name="words" /><ul><li c:for="w in words">${w}</li></ul>`,
			`<ul><li>foo</li><li>bar</li><li>baz</li></ul>`, nil},
		{"forNumbers", `<c:arg name="numbers" /><p c:for="i in numbers">${i}</p>`, `<p>1</p><p>2</p><p>3</p>`, nil},

		{"forIfFalse", `<p c:for="x in ['foo']" c:if="false">${x}</p>`, ``, nil},
		{"forIfTrue", `<p c:for="x in ['foo']" c:if="true">${x}</p>`, `<p>foo</p>`, nil},

		// Testing imports (<c:NAME>)
		{"simpleImport", `<c:hello-world />`, `Hello, World!`, nil},
		{"importWithArg", `<c:greeting></c:greeting>`, `Hello, !`, nil},
		{"importWithArg2", `<c:greeting>Bill</c:greeting>`, `Hello, Bill!`, nil},

		// Test component arguments
		{
			name:     "argString",
			template: `<c:simple-page title="Title">Content</c:simple-page>`,
			output:   `<h1>Title</h1><div>Content</div>`,
			wantErr:  nil,
		},
		{
			name: "argVar",
			template: `
				<c:arg name="page_title">Default Title</c:arg>
				<c:arg name="page_content">Default Content</c:arg>
				<c:simple-page title="${page_title}">
					${page_content}
				</c:simple-page>`,
			output: `<h1>Default Title</h1><div>
					Default Content
				</div>`,
			wantErr: nil,
		},
		{
			name: "htmlTitleAndContent",
			template: `
				<c:arg name="page_title">
					<i>Default Title</i>
				</c:arg>
				<c:arg name="page_content">
					<strong>Default Content</strong>
				</c:arg>
				<c:simple-page title="${page_title}">
					<p>${page_content}</p>
				</c:simple-page>`,
			output: `<h1>
					<i>Default Title</i>
				</h1><div><p>
					<strong>Default Content</strong>
				</p></div>`,
			wantErr: nil,
		},
		{
			name:     "doubleHtmlArgEval",
			template: `<c:arg name="content"><ul><li>Item</li></ul></c:arg>${content}<p>${content}</p>`,
			output:   `<ul><li>Item</li></ul><p><ul><li>Item</li></ul></p>`,
			wantErr:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			comp, err := Parse(strings.NewReader(tt.template), ImporterFunc(importFunc))
			if err != nil {
				t.Errorf("Parse() error = %v", err)
				return
			}

			s := NewScopeMap(nil)
			vars := s.Vars()
			for k, v := range env {
				vars[k] = v
			}

			rr, err := comp.Render(s)
			if err != nil {
				if tt.wantErr == nil {
					t.Errorf("Render() error = %v", err)
					return
				} else {
					if err.Error() != tt.wantErr.Error() {
						t.Errorf("Render() error = %v, wantErr %v", err, tt.wantErr)
					}
					return
				}
			}

			var b strings.Builder
			if err := html.Render(&b, rr.HTML); err != nil {
				t.Errorf("Render() error = %v", err)
				return
			}
			got := b.String()
			want := tt.output
			if diff := cmp.Diff(got, want); diff != "" {
				t.Errorf("Render() diff (-got +want):\n%s", diff)
			}
		})
	}
}

func TestComponent_parseArgs(t *testing.T) {
	tests := []struct {
		name     string
		nodes    string
		wantArgs map[string]any
		wantErr  bool
	}{
		{
			name:     "empty",
			nodes:    ``,
			wantArgs: map[string]any{"_": nil},
			wantErr:  false,
		},
		{
			name:     "noArgName",
			nodes:    `<c:arg></c:arg>`,
			wantArgs: map[string]any{"_": nil},
			wantErr:  true,
		},
		{
			name:     "emptyArg",
			nodes:    `<c:arg name="foo" />`,
			wantArgs: map[string]any{"_": nil, "foo": new(any)},
			wantErr:  false,
		},
		{
			name:     "stringArg",
			nodes:    `<c:arg name="foo">bar</c:arg>`,
			wantArgs: map[string]any{"_": nil, "foo": []etree.Token{etree.NewText("bar")}},
			wantErr:  false,
		},
		{
			name:     "stringArgInterpol",
			nodes:    `<c:arg name="foo">${"bar"}</c:arg>`,
			wantArgs: map[string]any{"_": nil, "foo": []etree.Token{etree.NewText("${\"bar\"}")}},
			wantErr:  false,
		},
		{
			name: "htmlArg",
			nodes: `
					<c:arg name="foo">
						<p>bar</p>
						<pre>baz</pre>
					</c:arg>
		    	`,
			wantArgs: map[string]any{
				"_": nil,
				"foo": []etree.Token{
					etree.NewText("\n\t\t\t\t\t"),
					etree.NewElement("p"),
					etree.NewText("\n\t\t\t\t\t"),
					etree.NewElement("pre"),
					etree.NewText("\n\t\t\t\t\t"),
				},
			},
			wantErr: false,
		},
		{
			name: "nestedArg",
			nodes: `
				<c:arg name="foo">
					<c:arg name="bar">baz</c:arg>
					<c:arg name="baz">${123}</c:arg>
				</c:arg>
			`,
			wantArgs: map[string]any{
				"_":   nil,
				"foo": []etree.Token{},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := Parse(strings.NewReader(tt.nodes), nil)
			if err != nil {
				if !tt.wantErr {
					t.Errorf("Parse() error = %v", err)
				}
				return
			}

			opts := cmp.Options{
				cmp.FilterPath(func(p cmp.Path) bool {
					if len(p) > 1 {
						prev := p.Index(-2)

						// Ignore non-Data properties for text nodes.
						if prev.Type() == reflect.TypeOf(etree.CharData{}) {
							return p.Last().String() != "Data"
						}

						// Ignore all properties for elements except Space and Tag.
						if prev.Type() == reflect.TypeOf(etree.Element{}) {
							switch p.Last().String() {
							case "Space", "Tag":
								return false
							}
							return true
						}
					}
					return false
				}, cmp.Ignore()),
			}

			if diff := cmp.Diff(c.(*chtmlComponent).args, tt.wantArgs, opts); diff != "" {
				t.Errorf("Parse() diff (-got +want):\n%s", diff)
			}
		})
	}
}
