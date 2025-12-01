package chtml

import (
	"strings"
	"testing"
)

func TestNewExprCond(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantShape  *Shape
		wantBind   string
		wantErr    bool
		errContain string
	}{
		{
			name:      "simple expression without is",
			input:     "true",
			wantShape: nil,
			wantBind:  "",
		},
		{
			name:      "expression with variable without is",
			input:     "x > 5",
			wantShape: nil,
			wantBind:  "",
		},
		{
			name:      "is with bool shape",
			input:     "x is bool",
			wantShape: Bool,
			wantBind:  "x", // implicit: single identifier
		},
		{
			name:      "is with string shape",
			input:     "data is string",
			wantShape: String,
			wantBind:  "data",
		},
		{
			name:      "is with number shape",
			input:     "value is number",
			wantShape: Number,
			wantBind:  "value",
		},
		{
			name:      "is with any shape",
			input:     "x is any",
			wantShape: Any,
			wantBind:  "x",
		},
		{
			name:      "is with explicit as",
			input:     "x is bool as flag",
			wantShape: Bool,
			wantBind:  "flag",
		},
		{
			name:      "is with object shape",
			input:     "resp is {success: bool, data: string}",
			wantShape: Object(map[string]*Shape{"success": Bool, "data": String}),
			wantBind:  "resp",
		},
		{
			name:      "is with object shape and explicit as",
			input:     "result is {ok: bool} as r",
			wantShape: Object(map[string]*Shape{"ok": Bool}),
			wantBind:  "r",
		},
		{
			name:      "is with array shape",
			input:     "items is [string]",
			wantShape: ArrayOf(String),
			wantBind:  "items",
		},
		{
			name:      "complex expression no binding",
			input:     "true || false is bool",
			wantShape: Bool,
			wantBind:  "", // not a simple identifier, no implicit binding
		},
		{
			name:      "ternary expression no binding",
			input:     "(x ? 1 : 0) is number",
			wantShape: Number,
			wantBind:  "", // not a simple identifier
		},
		{
			name:      "complex expression with explicit as",
			input:     "(x + y) is number as val",
			wantShape: Number,
			wantBind:  "val",
		},
		{
			name:      "nested object shape",
			input:     "resp is {user: {name: string, id: number}}",
			wantShape: Object(map[string]*Shape{"user": Object(map[string]*Shape{"name": String, "id": Number})}),
			wantBind:  "resp",
		},
		{
			name:      "array of objects",
			input:     "users is [{name: string}]",
			wantShape: ArrayOf(Object(map[string]*Shape{"name": String})),
			wantBind:  "users",
		},
		{
			name:       "invalid shape",
			input:      "x is unknowntype",
			wantErr:    true,
			errContain: "invalid shape literal",
		},
		{
			name:       "invalid identifier after as",
			input:      "x is bool as 123invalid",
			wantErr:    true,
			errContain: "invalid identifier",
		},
		{
			name:      "isActive not parsed as is",
			input:     "isActive",
			wantShape: nil,
			wantBind:  "",
		},
		{
			name:      "string containing is",
			input:     `status == "is_active"`,
			wantShape: nil,
			wantBind:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr, err := NewExprCond(tt.input, nil)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				} else if tt.errContain != "" && !strings.Contains(err.Error(), tt.errContain) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errContain)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantShape == nil {
				if expr.IsMatchCond() {
					t.Errorf("isMatchCond = true, want false")
				}
			} else {
				if !expr.IsMatchCond() {
					t.Errorf("isMatchCond = false, want true")
				}
				if expr.Shape() == nil {
					t.Errorf("shape = nil, want %v", tt.wantShape)
				} else if !expr.Shape().Equal(tt.wantShape) {
					t.Errorf("shape = %v, want %v", expr.Shape(), tt.wantShape)
				}
			}

			if expr.BindVar() != tt.wantBind {
				t.Errorf("bindVar = %q, want %q", expr.BindVar(), tt.wantBind)
			}
		})
	}
}

func TestRenderCondMatch(t *testing.T) {
	tests := []struct {
		name string
		text string
		want any
		vars map[string]any
	}{
		// Basic shape matching
		{
			name: "is bool - matches true",
			text: `<p c:if="val is bool">${ val }</p>`,
			want: "<p>true</p>",
			vars: map[string]any{"val": true},
		},
		{
			name: "is bool - matches false",
			text: `<p c:if="val is bool">${ val }</p>`,
			want: "<p>false</p>",
			vars: map[string]any{"val": false},
		},
		{
			name: "is bool - not match string",
			text: `<p c:if="val is bool">matched</p><p c:else>not matched</p>`,
			want: "<p>not matched</p>",
			vars: map[string]any{"val": "hello"},
		},
		{
			name: "is string - matches",
			text: `<p c:if="val is string">${ val }</p>`,
			want: "<p>hello</p>",
			vars: map[string]any{"val": "hello"},
		},
		{
			name: "is string - not match number",
			text: `<p c:if="val is string">matched</p><p c:else>not matched</p>`,
			want: "<p>not matched</p>",
			vars: map[string]any{"val": 42},
		},
		{
			name: "is number - matches int",
			text: `<p c:if="val is number">${ val }</p>`,
			want: "<p>42</p>",
			vars: map[string]any{"val": 42},
		},
		{
			name: "is number - matches float",
			text: `<p c:if="val is number">${ val }</p>`,
			want: "<p>3.14</p>",
			vars: map[string]any{"val": 3.14},
		},
		{
			name: "is number - not match string",
			text: `<p c:if="val is number">matched</p><p c:else>not matched</p>`,
			want: "<p>not matched</p>",
			vars: map[string]any{"val": "42"},
		},

		// Explicit variable binding with 'as'
		{
			name: "is with as - binds variable",
			text: `<p c:if="response is {success: bool} as r">matched</p>`,
			want: "<p>matched</p>",
			vars: map[string]any{"response": map[string]any{"success": true}},
		},
		{
			name: "is with as - different name",
			text: `<p c:if="data is string as s">${ s }</p>`,
			want: "<p>hello</p>",
			vars: map[string]any{"data": "hello"},
		},

		// Implicit variable binding (single identifier)
		{
			name: "is implicit binding",
			text: `<p c:if="msg is string">${ msg }</p>`,
			want: "<p>test</p>",
			vars: map[string]any{"msg": "test"},
		},

		// Object shape matching
		{
			name: "is object - matches",
			text: `<p c:if="resp is {success: bool}">matched</p>`,
			want: "<p>matched</p>",
			vars: map[string]any{"resp": map[string]any{"success": true}},
		},
		{
			name: "is object - access member of bound var",
			text: `<p c:if="resp is {success: bool, data: string} as r">${ r.data }</p>`,
			want: "<p>hello</p>",
			vars: map[string]any{"resp": map[string]any{"success": true, "data": "hello"}},
		},
		{
			name: "is object - access nested member",
			text: `<p c:if="resp is {user: {name: string}} as r">${ r.user.name }</p>`,
			want: "<p>Alice</p>",
			vars: map[string]any{"resp": map[string]any{"user": map[string]any{"name": "Alice"}}},
		},
		{
			name: "is object - missing field",
			text: `<p c:if="resp is {success: bool}">matched</p><p c:else>not matched</p>`,
			want: "<p>not matched</p>",
			vars: map[string]any{"resp": map[string]any{"other": true}},
		},
		{
			name: "is object - nil value",
			text: `<p c:if="resp is {success: bool}">matched</p><p c:else>not matched</p>`,
			want: "<p>not matched</p>",
			vars: map[string]any{"resp": nil},
		},
		{
			name: "is object - extra fields ok",
			text: `<p c:if="resp is {success: bool}">matched</p>`,
			want: "<p>matched</p>",
			vars: map[string]any{"resp": map[string]any{"success": true, "data": "extra"}},
		},

		// Array shape matching
		{
			name: "is array - matches",
			text: `<p c:if="items is [string]">has items</p>`,
			want: "<p>has items</p>",
			vars: map[string]any{"items": []string{"a", "b", "c"}},
		},
		{
			name: "is array - not match non-array",
			text: `<p c:if="items is [string]">matched</p><p c:else>not matched</p>`,
			want: "<p>not matched</p>",
			vars: map[string]any{"items": "not an array"},
		},

		// Complex expressions (no implicit binding)
		{
			name: "complex expr no binding - ternary",
			text: `<p c:if="(x > 0 ? x : 0) is number">matched</p>`,
			want: "<p>matched</p>",
			vars: map[string]any{"x": 5},
		},
		{
			name: "binary expr with explicit as",
			text: `<p c:if="(a + b) is number as sum">${ sum }</p>`,
			want: "<p>8</p>",
			vars: map[string]any{"a": 3, "b": 5},
		},

		// else-if with shape matching
		{
			name: "else-if with is",
			text: `<p c:if="val is bool">bool: ${ val }</p><p c:else-if="val is string">str: ${ val }</p><p c:else>other</p>`,
			want: "<p>str: hello</p>",
			vars: map[string]any{"val": "hello"},
		},
		{
			name: "if-elif-else all with is",
			text: `<p c:if="val is bool">bool</p><p c:else-if="val is number">num: ${ val }</p><p c:else>other</p>`,
			want: "<p>num: 42</p>",
			vars: map[string]any{"val": 42},
		},

		// <c> element syntax
		{
			name: "c element if with is",
			text: `<c if="val is string">${ val }</c>`,
			want: "hello",
			vars: map[string]any{"val": "hello"},
		},
		{
			name: "c element if with is and as",
			text: `<c if="response is {ok: bool} as r">matched</c>`,
			want: "matched",
			vars: map[string]any{"response": map[string]any{"ok": true}},
		},

		// Backwards compatibility - regular conditions still work
		{
			name: "regular condition still works",
			text: `<p c:if="x > 5">big</p><p c:else>small</p>`,
			want: "<p>big</p>",
			vars: map[string]any{"x": 10},
		},
		{
			name: "regular condition with variable",
			text: `<p c:if="enabled">yes</p><p c:else>no</p>`,
			want: "<p>yes</p>",
			vars: map[string]any{"enabled": true},
		},

		// Nested object shape
		{
			name: "nested object shape",
			text: `<p c:if="resp is {user: {name: string}}">matched</p>`,
			want: "<p>matched</p>",
			vars: map[string]any{"resp": map[string]any{"user": map[string]any{"name": "Alice"}}},
		},

		// any shape always matches non-nil values
		{
			name: "is any matches everything",
			text: `<p c:if="val is any">matched</p>`,
			want: "<p>matched</p>",
			vars: map[string]any{"val": "anything"},
		},
		// nil doesn't match any shape (design choice for pattern matching)
		{
			name: "is any does not match nil",
			text: `<p c:if="val is any">matched</p><p c:else>nil</p>`,
			want: "<p>nil</p>",
			vars: map[string]any{"val": nil},
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

func TestCondMatchEdgeCases(t *testing.T) {
	tests := []struct {
		name string
		text string
		want any
		vars map[string]any
	}{
		{
			name: "variable not leaked outside scope",
			text: `<p c:if="val is string as s">${ s }</p><p>${ s ?? "undefined" }</p>`,
			want: "<p>hello</p><p>undefined</p>",
			vars: map[string]any{"val": "hello"},
		},
		{
			name: "is keyword inside string literal",
			text: `<p c:if="status == 'is_active'">active</p>`,
			want: "<p>active</p>",
			vars: map[string]any{"status": "is_active"},
		},
		{
			name: "is in nested parentheses",
			text: `<p c:if="(val == 'is') && true">matched</p>`,
			want: "<p>matched</p>",
			vars: map[string]any{"val": "is"},
		},
		{
			name: "shape with deeply nested structure",
			text: `<p c:if="data is {a: {b: {c: string}}}">matched</p>`,
			want: "<p>matched</p>",
			vars: map[string]any{"data": map[string]any{
				"a": map[string]any{
					"b": map[string]any{
						"c": "deep",
					},
				},
			}},
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
