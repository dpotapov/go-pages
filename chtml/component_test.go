package chtml

/*

func importFunc(name string) (Component, error) {
	scope := ""
	switch strings.ToLower(name) {
	case "hello-world":
		scope = "Hello, World!"
	case "greeting":
		scope = `Hello, ${_}!`
	case "simple-page":
		scope = `<c:arg name="title">NoTitle</c:arg><h1>${title}</h1><div>${_}</div>`
	case "data-provider":
		return dataProviderComponent(), nil
	default:
		return nil, fmt.Errorf("unknown component %q", name)
	}

	parser, err := Parse(strings.NewReader(scope), ImporterFunc(importFunc))
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

		// Test component arguments
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

			scope := NewBaseScope(env(nil))

			comp, err := parser.Import("")
			if err != nil {
				t.Errorf("Import() error = %v", err)
				return
			}

			rr, err := comp.Render(scope)
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

			if diff := cmp.Diff(p.(*chtmlParserOld).inpSchema, tt.wantSchema, opts); diff != "" {
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

func (c *testComponent) Render(scope Scope) (any, error) {
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

	scope := NewBaseScope(nil)
	comp, err := parser.Import("")
	require.NoError(t, err)

	events = nil

	_, err = comp.Render(scope)
	require.NoError(t, err)

	require.Equal(t, 2, len(events))
	require.True(t, events[0].imported)
	require.True(t, events[1].rendered)

	// Reuse the component first time
	events = nil
	_, err = comp.Render(scope)
	require.NoError(t, err)
	require.Equal(t, 1, len(events))
	require.True(t, events[0].rendered) // no import event

	// Reuse the component second time
	events = nil
	_, err = comp.Render(scope)
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
	return ComponentFunc(func(scope Scope) (any, error) {
		vars := scope.Vars()
		rr := map[string]any{"key1": "value1"} // default value
		if _, ok := vars["key1"]; ok {
			rr["key1"] = vars["key1"]
		}
		return rr, nil
	})
}
*/
