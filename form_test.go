package pages

import (
	"net/url"
	"reflect"
	"testing"
)

func TestDecodeForm(t *testing.T) {
	tests := []struct {
		name     string
		input    url.Values
		expected map[string]any
	}{
		{
			name: "simple key-value",
			input: url.Values{
				"name": {"John Doe"},
				"age":  {"30"},
			},
			expected: map[string]any{
				"name": "John Doe",
				"age":  "30",
			},
		},
		{
			name: "nested object",
			input: url.Values{
				"project.id":   {"123"},
				"project.name": {"Tesla"},
			},
			expected: map[string]any{
				"project": map[string]any{
					"id":   "123",
					"name": "Tesla",
				},
			},
		},
		{
			name: "simple array",
			input: url.Values{
				"users[0]": {"Alice"},
				"users[1]": {"Bob"},
			},
			expected: map[string]any{
				"users": []any{"Alice", "Bob"},
			},
		},
		{
			name: "array of objects",
			input: url.Values{
				"apps[0].name": {"nginx"},
				"apps[0].port": {"80"},
				"apps[1].name": {"redis"},
				"apps[1].port": {"6379"},
			},
			expected: map[string]any{
				"apps": []any{
					map[string]any{"name": "nginx", "port": "80"},
					map[string]any{"name": "redis", "port": "6379"},
				},
			},
		},
		{
			name: "complex nested structure",
			input: url.Values{
				"project.id":            {"p1"},
				"project.name":          {"My Project"},
				"apps[0].name":          {"frontend"},
				"apps[0].vars[0].name":  {"API_URL"},
				"apps[0].vars[0].value": {"http://api.example.com"},
				"apps[0].vars[1].name":  {"RETRIES"},
				"apps[0].vars[1].value": {"3"},
				"apps[1].name":          {"backend"},
				"users[0]":              {"admin"},
			},
			expected: map[string]any{
				"project": map[string]any{
					"id":   "p1",
					"name": "My Project",
				},
				"apps": []any{
					map[string]any{
						"name": "frontend",
						"vars": []any{
							map[string]any{"name": "API_URL", "value": "http://api.example.com"},
							map[string]any{"name": "RETRIES", "value": "3"},
						},
					},
					map[string]any{"name": "backend"},
				},
				"users": []any{"admin"},
			},
		},
		{
			name:     "empty input",
			input:    url.Values{},
			expected: map[string]any{},
		},
		{
			name: "empty value",
			input: url.Values{
				"key": {""},
			},
			expected: map[string]any{
				"key": "",
			},
		},
		{
			name: "multiple values for one key",
			input: url.Values{
				"key": {"value1", "value2"},
			},
			expected: map[string]any{
				"key": "value1", // Should take the first value
			},
		},
		{
			name: "malformed array index",
			input: url.Values{
				"key[abc]": {"value"},
				"key[]":    {"value2"},
				"key[1a]":  {"value3"},
			},
			expected: map[string]any{
				"key[abc]": "value",
				"key[]":    "value2",
				"key[1a]":  "value3",
			},
		},
		{
			name: "out-of-order array indices",
			input: url.Values{
				"items[1]": {"B"},
				"items[0]": {"A"},
				"items[3]": {"D"},
			},
			expected: map[string]any{
				// Note: items[2] will be nil because index 3 requires size 4
				"items": []any{"A", "B", nil, "D"},
			},
		},
		{
			name: "overwrite simple value",
			input: url.Values{
				"key":        {"initial"},
				"key.nested": {"overwrite"}, // This attempts to overwrite string with map
			},
			// Expected: The first assignment creates "key":"initial".
			// The second assignment fails silently because it tries to access a nested field
			// on a string. The original value remains.
			// NOTE: The current implementation uses fmt.Printf for errors. A robust implementation
			// might return an error or log properly.
			expected: map[string]any{
				"key": "initial",
			},
		},
		{
			name: "overwrite map with simple value",
			input: url.Values{
				"key.nested": {"initial"},
				"key":        {"overwrite"},
			},
			expected: map[string]any{
				"key": "overwrite",
			},
		},
		{
			name: "complex keys",
			input: url.Values{
				"config.server[0].host":     {"localhost"},
				"config.server[0].ports[0]": {"8080"},
				"config.server[0].ports[1]": {"8081"},
				"config.database.url":       {"postgres://..."},
			},
			expected: map[string]any{
				"config": map[string]any{
					"server": []any{
						map[string]any{
							"host":  "localhost",
							"ports": []any{"8080", "8081"},
						},
					},
					"database": map[string]any{
						"url": "postgres://...",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := DecodeForm(tt.input, nil)
			if !reflect.DeepEqual(actual, tt.expected) {
				t.Errorf("DecodeForm() = %#v, want %#v", actual, tt.expected)
			}
		})
	}
}
