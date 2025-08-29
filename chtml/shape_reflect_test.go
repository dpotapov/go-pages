package chtml

import (
	"testing"
)

func TestShapeFromMapTypes(t *testing.T) {
	tests := []struct {
		name     string
		value    any
		expected string
	}{
		{
			name:     "map[string]string",
			value:    map[string]string{"foo": "bar"},
			expected: "{_:string}",
		},
		{
			name:     "map[string]int",
			value:    map[string]int{"foo": 42},
			expected: "{_:number}",
		},
		{
			name:     "map[string][]string",
			value:    map[string][]string{"foo": {"bar", "baz"}},
			expected: "{_:[string]}",
		},
		{
			name:     "map[string]any",
			value:    map[string]any{"foo": "bar", "baz": 42},
			expected: "{_:any}",
		},
		{
			name:     "map[string]map[string]string",
			value:    map[string]map[string]string{"foo": {"bar": "baz"}},
			expected: "{_:{_:string}}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shape := ShapeFrom(tt.value)
			if shape == nil {
				t.Fatal("ShapeFrom returned nil")
			}
			got := shape.String()
			if got != tt.expected {
				t.Errorf("ShapeFrom(%T) = %q, want %q", tt.value, got, tt.expected)
			}
		})
	}
}

func TestShapeOfMapTypes(t *testing.T) {
	// Test using ShapeOf with type parameters
	t.Run("ShapeOf[map[string]string]", func(t *testing.T) {
		shape := ShapeOf[map[string]string]()
		expected := "{_:string}"
		if got := shape.String(); got != expected {
			t.Errorf("ShapeOf[map[string]string]() = %q, want %q", got, expected)
		}
	})

	t.Run("ShapeOf[map[string][]string]", func(t *testing.T) {
		shape := ShapeOf[map[string][]string]()
		expected := "{_:[string]}"
		if got := shape.String(); got != expected {
			t.Errorf("ShapeOf[map[string][]string]() = %q, want %q", got, expected)
		}
	})
}