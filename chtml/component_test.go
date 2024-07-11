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
	imp, err := Parse(r, nil)
	if err != nil {
		t.Errorf("Parse() error = %v", err)
		return
	}
	parser := imp.(*chtmlParser)

	// Check inpSchema:
	require.Equal(t, 2, len(parser.inpSchema))
	require.Equal(t, new(any), parser.inpSchema["_"])
	require.Equal(t, "http://foo.bar", parser.inpSchema["link1"])

	// Check links:
	require.Len(t, parser.doc.Element.ChildElements(), 3)
	ul := parser.doc.Element.SelectElement("ul")
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
	case "data-provider":
		return dataProviderComponent(), nil
	default:
		return nil, fmt.Errorf("unknown component %q", name)
	}

	parser, err := Parse(strings.NewReader(s), ImporterFunc(importFunc))
	if err != nil {
		return nil, err
	}

	return parser.Import("")
}

func TestComponent_ParseAndRender(t *testing.T) {
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
		{"forWords", `<c:arg name="words">${['foo', 'bar', 'baz']}</c:arg><ul><li c:for="w in words">${w}</li></ul>`,
			`<ul><li>foo</li><li>bar</li><li>baz</li></ul>`, nil},
		{"forNumbers", `<c:arg name="numbers">${[1,2,3]}</c:arg><p c:for="i in numbers">${i}</p>`, `<p>1</p><p>2</p><p>3</p>`, nil},

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
		{
			name:     "compWithinArg",
			template: `<c:arg name="data"><c:data-provider key1="newVal" /></c:arg>${data.key1}`,
			output:   "newVal",
			wantErr:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser, err := Parse(strings.NewReader(tt.template), ImporterFunc(importFunc))
			if err != nil {
				t.Errorf("Parse() error = %v", err)
				return
			}

			s := NewScope(env(nil))

			comp, err := parser.Import("")
			if err != nil {
				t.Errorf("Import() error = %v", err)
				return
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
			if ht := AnyToHtml(rr); ht != nil {
				if err := html.Render(&b, ht); err != nil {
					t.Errorf("Render() error = %v", err)
					return
				}
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
		name       string
		nodes      string
		wantSchema map[string]any
		wantErr    bool
	}{
		{
			name:       "empty",
			nodes:      ``,
			wantSchema: map[string]any{"_": new(any)},
			wantErr:    false,
		},
		{
			name:       "noArgName",
			nodes:      `<c:arg></c:arg>`,
			wantSchema: map[string]any{"_": new(any)},
			wantErr:    true,
		},
		{
			name:       "emptyArg",
			nodes:      `<c:arg name="foo" />`,
			wantSchema: map[string]any{"_": new(any), "foo": new(any)},
			wantErr:    false,
		},
		{
			name:       "stringArg",
			nodes:      `<c:arg name="foo">bar</c:arg>`,
			wantSchema: map[string]any{"_": new(any), "foo": "bar"},
			wantErr:    false,
		},
		{
			name:       "stringArgInterpol",
			nodes:      `<c:arg name="foo">${"bar"}</c:arg>`,
			wantSchema: map[string]any{"_": new(any), "foo": "bar"},
			wantErr:    false,
		},
		{
			name: "stringWhitespaceArgInterpol",
			nodes: `<c:arg name="foo">
					${"bar"}
					</c:arg>`,
			wantSchema: map[string]any{"_": new(any), "foo": "\n\t\t\t\t\tbar\n\t\t\t\t\t"},
			wantErr:    false,
		},
		{
			name:       "boolArgInterpol",
			nodes:      `<c:arg name="foo">${true}</c:arg>`,
			wantSchema: map[string]any{"_": new(any), "foo": true},
			wantErr:    false,
		},
		{
			name:       "intArgInterpol",
			nodes:      `<c:arg name="foo">${123}</c:arg>`,
			wantSchema: map[string]any{"_": new(any), "foo": 123},
			wantErr:    false,
		},
		{
			name: "intWhitespaceArgInterpol",
			nodes: `<c:arg name="foo">
					${123}
				</c:arg>`,
			wantSchema: map[string]any{"_": new(any), "foo": 123},
			wantErr:    false,
		},
		{
			name:       "listArgInterpol",
			nodes:      `<c:arg name="foo">${ [1,2,3] }</c:arg>`,
			wantSchema: map[string]any{"_": new(any), "foo": []any{1, 2, 3}},
			wantErr:    false,
		},
		{
			name: "htmlArg",
			nodes: `
					<c:arg name="foo">
						<p>bar</p>
						<pre>baz</pre>
					</c:arg>
		    	`,
			wantSchema: map[string]any{
				"_":   new(any),
				"foo": &html.Node{},
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
			wantSchema: map[string]any{
				"_":   new(any),
				"foo": []etree.Token{},
			},
			wantErr: true,
		},
		{
			name: "importArg",
			nodes: `<c:arg name="foo">
						<c:data-provider />
					</c:arg>`,
			wantSchema: map[string]any{"_": new(any), "foo": map[string]any{"key1": "value1"}},
			wantErr:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := Parse(strings.NewReader(tt.nodes), ImporterFunc(importFunc))
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

			if diff := cmp.Diff(p.(*chtmlParser).inpSchema, tt.wantSchema, opts); diff != "" {
				t.Errorf("Parse() diff (-got +want):\n%s", diff)
			}
		})
	}
}

type componentLifecycleEvent struct {
	imported bool
	rendered bool
	disposed bool
}

type testComponent struct {
	events *[]componentLifecycleEvent
}

func (c *testComponent) Render(s Scope) (any, error) {
	*c.events = append(*c.events, componentLifecycleEvent{rendered: true})
	return nil, nil
}

func (c *testComponent) Dispose() {
	*c.events = append(*c.events, componentLifecycleEvent{disposed: true})
}

func TestComponentReuse(t *testing.T) {
	var events []componentLifecycleEvent

	importFunc := func(name string) (Component, error) {
		switch name {
		case "test":
			events = append(events, componentLifecycleEvent{imported: true})
			return &testComponent{&events}, nil
		default:
			return nil, fmt.Errorf("unknown component %q", name)
		}
	}

	parser, err := Parse(strings.NewReader("<c:test />"), ImporterFunc(importFunc))
	require.NoError(t, err)

	require.Equal(t, 3, len(events))
	require.True(t, events[0].imported)
	require.True(t, events[1].rendered)
	require.True(t, events[2].disposed)

	s := NewScope(nil)
	comp, err := parser.Import("")
	require.NoError(t, err)

	events = nil

	_, err = comp.Render(s)
	require.NoError(t, err)

	require.Equal(t, 2, len(events))
	require.True(t, events[0].imported)
	require.True(t, events[1].rendered)

	// Reuse the component first time
	events = nil
	_, err = comp.Render(s)
	require.NoError(t, err)
	require.Equal(t, 1, len(events))
	require.True(t, events[0].rendered) // no import event

	// Reuse the component second time
	events = nil
	_, err = comp.Render(s)
	require.NoError(t, err)
	require.Equal(t, 1, len(events))
	require.True(t, events[0].rendered) // no import event

	// Dispose the component
	events = nil
	if d, ok := comp.(Disposable); ok {
		d.Dispose()
	}
	require.Equal(t, 1, len(events))
	require.True(t, events[0].disposed)
}

var dataProviderComponent = func() Component {
	return ComponentFunc(func(s Scope) (any, error) {
		vars := s.Vars()
		rr := map[string]any{"key1": "value1"} // default value
		if _, ok := vars["key1"]; ok {
			rr["key1"] = vars["key1"]
		}
		return rr, nil
	})
}
