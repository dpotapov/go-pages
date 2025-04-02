package chtml

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type componentLifecycleEvent struct {
	imported bool
	rendered bool
	disposed bool
}

type testComponent struct {
	events *[]componentLifecycleEvent
}

var _ Component = (*testComponent)(nil)
var _ Disposable = (*testComponent)(nil)

func (c *testComponent) Render(s Scope) (any, error) {
	*c.events = append(*c.events, componentLifecycleEvent{rendered: true})
	return nil, nil
}

func (c *testComponent) Dispose() error {
	*c.events = append(*c.events, componentLifecycleEvent{disposed: true})
	return nil
}

func TestComponentReuse(t *testing.T) {
	var events []componentLifecycleEvent
	imp := &lifecycleImporter{&events}

	doc, err := Parse(strings.NewReader("<c:test></c:test>"), imp)
	require.NoError(t, err)

	require.Equal(t, 3, len(events))
	require.True(t, events[0].imported)
	require.True(t, events[1].rendered)
	require.True(t, events[2].disposed)

	scope := NewBaseScope(nil)
	comp := NewComponent(doc, &ComponentOptions{Importer: imp})

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
		err = d.Dispose()
		require.NoError(t, err)
	}
	require.Equal(t, 1, len(events))
	require.True(t, events[0].disposed)
}

type lifecycleImporter struct {
	events *[]componentLifecycleEvent
}

func (imp *lifecycleImporter) Import(name string) (Component, error) {
	switch name {
	case "test":
		*imp.events = append(*imp.events, componentLifecycleEvent{imported: true})
		return &testComponent{imp.events}, nil
	default:
		return nil, fmt.Errorf("unknown component %q", name)
	}
}

// TestDryRunValidation tests that components properly validate inputs even in dry run mode
func TestDryRunValidation(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		vars    map[string]any
		wantErr bool
	}{
		{
			name:    "valid input",
			text:    `<c:attr name="foo">bar</c:attr><div class="${foo}">Content</div>`,
			vars:    map[string]any{"foo": "custom-class"},
			wantErr: false,
		},
		{
			name:    "invalid input - unknown parameter",
			text:    `<c:attr name="foo">bar</c:attr><div class="${foo}">Content</div>`,
			vars:    map[string]any{"unknown": "value"},
			wantErr: true,
		},
		{
			name:    "valid with underscore",
			text:    `<c:attr name="content">Hello</c:attr>${content}`,
			vars:    map[string]any{"_": "child content", "content": "Override"},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := Parse(strings.NewReader(tt.text), nil)
			if err != nil {
				t.Fatalf("Parse error: %v", err)
			}

			comp := NewComponent(doc, nil)

			// Create dry run scope with test variables
			s := NewDryRunScope(tt.vars)

			// Test rendering in dry run mode
			_, err = comp.Render(s)

			if (err != nil) != tt.wantErr {
				t.Errorf("DryRun validation error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantErr && err != nil {
				// If we expect an error, verify it's an UnrecognizedArgumentError
				var argErr *UnrecognizedArgumentError
				if !errors.As(err, &argErr) {
					t.Errorf("Expected UnrecognizedArgumentError, got %T: %v", err, err)
				}
			}
		})
	}
}
