package chtml

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/net/html"
)

// Counter is a component that tracks the number of times it's rendered
type Counter struct {
	count int64
}

// NewCounter creates a new counter component
func NewCounter() *Counter {
	return &Counter{}
}

// Render increments the counter and returns the count
func (c *Counter) Render(scope Scope) (any, error) {
	newCount := atomic.AddInt64(&c.count, 1)
	return fmt.Sprintf("Counter: %d", newCount), nil
}

func (c *Counter) InputShape() *Shape  { return nil }
func (c *Counter) OutputShape() *Shape { return Any }

// GetCount returns the current count
func (c *Counter) GetCount() int64 {
	return atomic.LoadInt64(&c.count)
}

func (c *Counter) Reset() {
	atomic.StoreInt64(&c.count, 0)
}

// TestImporter implements the Importer interface for testing
type TestImporter struct {
	stack      []string
	components map[string]Component
	templates  map[string]string
}

// NewTestImporter creates a new test importer
func NewTestImporter() *TestImporter {
	return &TestImporter{
		stack:      []string{"root"},
		components: make(map[string]Component),
		templates:  make(map[string]string),
	}
}

// RegisterComponent registers a component with the importer
func (i *TestImporter) RegisterComponent(name string, comp Component) {
	i.components[name] = comp
}

// RegisterTemplate registers a template with the importer
func (i *TestImporter) RegisterTemplate(name, template string) {
	i.templates[name] = template
}

// Import implements the Importer interface
func (i *TestImporter) Import(name string) (Component, error) {
	fmt.Println(strings.Join(i.stack, " > ")+":", "importing", name)

	// Check if we have a pre-registered component
	if comp, exists := i.components[name]; exists {
		return comp, nil
	}

	// Check if we have a template to parse
	if template, exists := i.templates[name]; exists {
		// Push the current component to the stack
		i.stack = append(i.stack, name)
		defer func() {
			// Pop the component from the stack when done
			i.stack = i.stack[:len(i.stack)-1]
		}()

		doc, err := Parse(strings.NewReader(template), i)
		if err != nil {
			return nil, fmt.Errorf("parse template %s: %w", name, err)
		}

		comp := NewComponent(doc, &ComponentOptions{
			Importer: i,
		})

		i.RegisterComponent(name, comp)

		return comp, nil
	}

	return nil, fmt.Errorf("component %s not found", name)
}

func TestNestedImports(t *testing.T) {
	// Create a counter component
	counter := NewCounter()

	// Create our test importer
	importer := NewTestImporter()

	// Register the counter component
	importer.RegisterComponent("counter", counter)

	htmlContent := `<html><body><c var="var1"><c:counter /></c></body></html>`
	_, err := Parse(strings.NewReader(htmlContent), importer)
	if err != nil {
		t.Fatalf("Failed to parse HTML: %v", err)
	}

	fmt.Println("--------------------------------")

	count := counter.GetCount()
	if count != 0 {
		t.Errorf("Counter component was rendered %d times, expected 0", count)
	}

	counter.Reset()

	// Register templates for our test pages
	importer.RegisterTemplate("page3", `<c var="var3"><c:counter /></c>`)
	importer.RegisterTemplate("page2", `<c var="var2"><c:page3 /></c>`)
	importer.RegisterTemplate("page1", `<c var="var1"><c:page2 /></c>`)

	// Parse and render page1
	htmlContent = `<html><body><c:page1 /></body></html>`
	_, err = Parse(strings.NewReader(htmlContent), importer)
	if err != nil {
		t.Fatalf("Failed to parse HTML: %v", err)
	}

	fmt.Println("--------------------------------")

	// Check how many times the counter component was rendered during parsing
	count = counter.GetCount()
	if count != 0 {
		t.Errorf("Counter component was rendered %d times, expected 0", count)
	}

	counter.Reset()

	// Let's parse the same template again and check the count again
	doc, err := Parse(strings.NewReader(htmlContent), importer)
	if err != nil {
		t.Fatalf("Failed to parse HTML: %v", err)
	}

	count = counter.GetCount()
	if count != 0 {
		t.Errorf("Counter component was rendered %d times, expected 0", count)
	}

	fmt.Println("--------------------------------")

	counter.Reset()

	comp := NewComponent(doc, &ComponentOptions{
		Importer: importer,
	})

	// Render the component
	_, err = comp.Render(NewBaseScope(nil))
	if err != nil {
		t.Fatalf("Failed to render: %v", err)
	}

	// Check how many times the counter component was rendered
	// If there's a bug with multiple rendering, this count would be greater than 1
	count = counter.GetCount()
	if count != 1 {
		t.Errorf("Counter component was rendered %d times, expected 1", count)
	}
}

func TestComponentPassesListToAnotherComponent(t *testing.T) {
	importer := NewTestImporter()

	importer.RegisterTemplate("listparent", `<c var="data_list">${ { "data": [1,2,3] } }</c><c:listchild list="${data_list.data}" />`)
	importer.RegisterTemplate("listchild", `<c var="list">${ [] }</c><p c:for="i in list">${i}</p>`)

	doc, err := Parse(strings.NewReader(`<c:listparent />`), importer)
	if err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}

	comp := NewComponent(doc, &ComponentOptions{Importer: importer})
	out, err := comp.Render(NewBaseScope(nil))
	if err != nil {
		t.Fatalf("Failed to render: %v", err)
	}

	var rendered string
	switch v := out.(type) {
	case string:
		rendered = v
	case *html.Node:
		var sb strings.Builder
		if err := html.Render(&sb, v); err != nil {
			t.Fatalf("Failed to render HTML node: %v", err)
		}
		rendered = sb.String()
	default:
		t.Fatalf("Unexpected render output type: %T", out)
	}

	want := `<p>1</p><p>2</p><p>3</p>`
	if rendered != want {
		t.Errorf("got %q, want %q", rendered, want)
	}
}
