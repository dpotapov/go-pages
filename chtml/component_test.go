package chtml

import (
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
		d.Dispose()
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
