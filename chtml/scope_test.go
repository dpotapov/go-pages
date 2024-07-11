package chtml

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestBaseScope_NewScope(t *testing.T) {
	vars := map[string]any{"key": "value"}
	scope := NewScope(vars)

	if scope == nil {
		t.Fatal("NewScope returned nil")
	}

	if len(scope.Vars()) != 1 || scope.Vars()["key"] != "value" {
		t.Errorf("Scope variables not set correctly")
	}
}

func TestBaseScope_Spawn(t *testing.T) {
	parent := NewScope(nil)
	childVars := map[string]any{"child": "value"}
	child := parent.Spawn(childVars)

	if child == nil {
		t.Fatal("Spawn returned nil")
	}

	if len(child.Vars()) != 1 || child.Vars()["child"] != "value" {
		t.Errorf("Child scope variables not set correctly")
	}
}

func TestBaseScope_Vars(t *testing.T) {
	vars := map[string]any{"key": "value"}
	scope := NewScope(vars)

	scopeVars := scope.Vars()
	if len(scopeVars) != 1 || scopeVars["key"] != "value" {
		t.Errorf("Vars() did not return correct variables")
	}

	scopeVars["newKey"] = "newValue"
	if len(scope.Vars()) != 2 {
		t.Errorf("Unable to modify variables")
	}
}

func TestBaseScope_Touch(t *testing.T) {
	scope := NewScope(nil)
	scope.Touch()

	select {
	case <-scope.Touched():
	case <-time.After(100 * time.Millisecond):
		t.Error("Touch did not trigger the Touched channel")
	}

	// Test touching a child scope
	child := scope.Spawn(nil)
	child.Touch()

	select {
	case <-scope.Touched():
	case <-time.After(100 * time.Millisecond):
		t.Error("Touch did not trigger the parent's Touched channel")
	}
}

func TestUnmarshalScope(t *testing.T) {
	tests := []struct {
		name      string
		scope     Scope
		target    any
		want      any
		expectErr bool
	}{
		{
			name:  "Struct target",
			scope: NewScope(map[string]any{"full_name": "John", "age": 30}),
			target: &struct {
				FullName string
				Age      int
			}{},
			want: &struct {
				FullName string
				Age      int
			}{FullName: "John", Age: 30},
		},
		{
			name:   "Empty map target",
			scope:  NewScope(map[string]any{"name": "John", "age": 30}),
			target: &map[string]any{},
			want:   &map[string]any{},
		},
		{
			name:   "Map target",
			scope:  NewScope(map[string]any{"name": "John", "age": 30}),
			target: &map[string]any{"name": "", "age": 0},
			want:   &map[string]any{"name": "John", "age": 30},
		},
		{
			name:  "Invalid target (non-pointer)",
			scope: NewScope(map[string]any{"name": "John", "age": 30}),
			target: struct {
				Name string
				Age  int
			}{},
			expectErr: true,
		},
		{
			name:  "Invalid target (nil pointer)",
			scope: NewScope(map[string]any{"name": "John", "age": 30}),
			target: (*struct {
				Name string
				Age  int
			})(nil),
			expectErr: true,
		},
		{
			name:  "Incompatible types",
			scope: NewScope(map[string]any{"name": "John", "age": "thirty"}),
			target: &struct {
				Name string
				Age  int
			}{},
			expectErr: true,
		},
		{
			name: "Type conversion",
			scope: NewScope(map[string]any{
				"int":        "30",
				"float":      "12.3",
				"bool_true":  "T",
				"bool_false": "",
				"duration":   "1h30s",
				"reader":     "data",
			}),
			target: &struct {
				Int       int
				Float     float64
				BoolTrue  bool
				BoolFalse bool
				Duration  time.Duration
				Reader    io.Reader
			}{},
			want: &struct {
				Int       int
				Float     float64
				BoolTrue  bool
				BoolFalse bool
				Duration  time.Duration
				Reader    io.Reader
			}{
				Int:       30,
				Float:     12.3,
				BoolTrue:  true,
				BoolFalse: false,
				Duration:  1*time.Hour + 30*time.Second,
				Reader:    strings.NewReader("data"),
			},
		},
		{
			name: "Type conversion for Map target",
			scope: NewScope(map[string]any{
				"int":        "30",
				"float":      "12.3",
				"bool_true":  "foobar",
				"bool_false": "",
				"duration":   "1h30s",
				"reader":     strings.NewReader("data"),
			}),
			target: &map[string]any{
				"int":        0,
				"float":      0.0,
				"bool_true":  false,
				"bool_false": true,
				"duration":   time.Duration(0),
				"reader":     strings.NewReader(""),
			},
			want: &map[string]any{
				"int":        30,
				"float":      12.3,
				"bool_true":  true,
				"bool_false": false,
				"duration":   1*time.Hour + 30*time.Second,
				"reader":     strings.NewReader("data"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := UnmarshalScope(tt.scope, tt.target)
			if (err != nil) != tt.expectErr {
				t.Errorf("UnmarshalScope() error = %v, expectErr %v", err, tt.expectErr)
				return
			}
			if !tt.expectErr {
				// custom cmpopts.EquateComparable for io.Reader
				opt := cmp.Comparer(func(x, y io.Reader) bool {
					return true
				})

				if diff := cmp.Diff(tt.target, tt.want, opt); diff != "" {
					t.Errorf("UnmarshalScope() mismatch (-got +want):\n%s", diff)
				}
			}
		})
	}
}

func TestMarshalScope(t *testing.T) {
	tests := []struct {
		name      string
		scope     Scope
		src       any
		want      map[string]any
		expectErr bool
	}{
		{
			name:  "Struct source",
			scope: NewScope(map[string]any{}),
			src: struct {
				FullName string
				Age      int
			}{FullName: "Alice", Age: 25},
			want: map[string]any{"full_name": "Alice", "age": 25},
		},
		{
			name:  "Map source",
			scope: NewScope(map[string]any{}),
			src:   map[string]any{"Full-Name": "Alice", "Age": 25},
			want:  map[string]any{"full_name": "Alice", "age": 25},
		},
		{
			name:      "Invalid source (non-struct, non-map)",
			scope:     NewScope(map[string]any{}),
			src:       []any{"Name", "Alice"},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := MarshalScope(tt.scope, tt.src)
			if (err != nil) != tt.expectErr {
				t.Errorf("MarshalScope() error = %v, expectErr %v", err, tt.expectErr)
				return
			}
			if !tt.expectErr {
				if diff := cmp.Diff(tt.scope.Vars(), tt.want); diff != "" {
					t.Errorf("MarshalScope() mismatch (-got +want):\n%s", diff)
				}
			}
		})
	}
}

func TestToSnakeCase(t *testing.T) {
	cases := []struct {
		name       string
		input      string
		wantResult string
	}{
		{
			name:       "Empty String",
			input:      "",
			wantResult: "",
		},
		{
			name:       "Underscore",
			input:      "_",
			wantResult: "_",
		},
		{
			name:       "One Word",
			input:      "word",
			wantResult: "word",
		},
		{
			name:       "Dash In String",
			input:      "dash-word",
			wantResult: "dash_word",
		},
		{
			name:       "Camel Case String",
			input:      "CamelCase",
			wantResult: "camel_case",
		},
		{
			name:       "Pascal Case String",
			input:      "PascalCase",
			wantResult: "pascal_case",
		},
		{
			name:       "String Starts with Underscore",
			input:      "_privateVar",
			wantResult: "private_var",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			getResult := toSnakeCase(tc.input)
			if tc.wantResult != getResult {
				t.Errorf("expected %s, but got %s", tc.wantResult, getResult)
			}
		})
	}
}
