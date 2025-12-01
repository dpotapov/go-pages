package chtml

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestErrorReportingWithSpans(t *testing.T) {
	// Create a template with a malformed expression that will trigger a parse error
	input := `<div>
  <span c:if="${unclosed">${another_bad}</span>
</div>`

	_, err := ParseWithSource("example.chtml", strings.NewReader(input), nil)
	if err == nil {
		t.Fatal("Expected parsing to produce errors, but got none")
	}

	// Parse should return a multierror with ComponentError instances
	var componentErrors []*ComponentError
	if multierr, ok := err.(interface{ Unwrap() []error }); ok {
		for _, e := range multierr.Unwrap() {
			if ce, ok := e.(*ComponentError); ok {
				componentErrors = append(componentErrors, ce)
			}
		}
	} else if ce, ok := err.(*ComponentError); ok {
		componentErrors = append(componentErrors, ce)
	}

	if len(componentErrors) == 0 {
		t.Fatal("No ComponentError instances found in error")
	}

	// Check that span information is captured in at least one error
	hasSpanInfo := false
	for _, ce := range componentErrors {
		if ce.HasSourceLocation() {
			hasSpanInfo = true

			// Verify the span information makes sense
			if ce.File != "example.chtml" {
				t.Errorf("got file %q, want %q", ce.File, "example.chtml")
			}
			if ce.Line < 1 || ce.Line > 3 {
				t.Errorf("got line %d, want between 1-3", ce.Line)
			}
			if ce.Column < 1 {
				t.Errorf("got column %d, want >= 1", ce.Column)
			}
			if ce.Length < 1 {
				t.Errorf("got length %d, want >= 1", ce.Length)
			}

			t.Logf("Error with span info: %s at %s:%d:%d (length %d)",
				ce.Error(), ce.File, ce.Line, ce.Column, ce.Length)
		}
	}

	if !hasSpanInfo {
		t.Error("No ComponentError had source location information")
	}
}

func TestComponentErrorAccessors(t *testing.T) {
	// Create a simple error with span info
	n := &Node{
		Type: html.ElementNode,
		Data: NewExprRaw("div"),
		Source: Source{
			File: "test.chtml",
			Span: Span{
				Offset: 10,
				Line:   2,
				Column: 5,
				Length: 15,
			},
		},
	}

	originalErr := errors.New("test error")
	ce := newComponentError(n, originalErr)

	// Test all fields
	if ce.File != "test.chtml" {
		t.Errorf("File = %q, want %q", ce.File, "test.chtml")
	}
	if ce.Line != 2 {
		t.Errorf("Line = %d, want %d", ce.Line, 2)
	}
	if ce.Column != 5 {
		t.Errorf("Column = %d, want %d", ce.Column, 5)
	}
	if ce.Length != 15 {
		t.Errorf("Length = %d, want %d", ce.Length, 15)
	}
	if !ce.HasSourceLocation() {
		t.Error("HasSourceLocation() = false, want true")
	}

	// Test error without span info
	nNoSpan := &Node{Type: html.ElementNode, Data: NewExprRaw("div")}
	ceNoSpan := newComponentError(nNoSpan, originalErr)

	if ceNoSpan.HasSourceLocation() {
		t.Error("HasSourceLocation() = true for error without span, want false")
	}
}

func TestStackTraceFrameSkipping(t *testing.T) {
	// Create a simple error with a node
	n := &Node{
		Type: html.ElementNode,
		Data: NewExprRaw("div"),
		Source: Source{
			File: "test.chtml",
			Span: Span{Line: 1, Column: 1, Length: 5},
		},
	}

	originalErr := errors.New("test error for stack trace")
	ce := newComponentError(n, originalErr)

	// Get the stack trace
	stack := ce.StackTrace()

	// Check that unwanted frames are not present
	if strings.Contains(stack, "runtime/debug.Stack") {
		t.Error("Stack trace should not contain runtime/debug.Stack frame")
	}
	if strings.Contains(stack, "chtml.newComponentError") {
		t.Error("Stack trace should not contain chtml.newComponentError frame")
	}
	if strings.Contains(stack, "chtml.captureStack") {
		t.Error("Stack trace should not contain chtml.captureStack frame")
	}

	// Check that the actual caller (TestStackTraceFrameSkipping) is present
	if !strings.Contains(stack, "TestStackTraceFrameSkipping") {
		t.Error("Stack trace should contain TestStackTraceFrameSkipping frame")
	}

	// Print the stack trace for manual inspection
	t.Logf("Stack trace:\n%s", stack)
}

func TestComponentStack(t *testing.T) {
	// Create a chain of nested ComponentErrors to simulate:
	// c:page-node -> c:http-call -> actual error

	// The innermost error (root cause)
	rootCause := errors.New("http call to /api/nodes/xyz failed with status 400")

	// Create nodes for each level
	httpCallNode := &Node{
		Type: html.ElementNode,
		Data: NewExprRaw("c:http-call"),
		Source: Source{
			File: "page-node.chtml",
			Span: Span{Line: 15, Column: 3, Length: 40},
		},
	}

	pageNodeNode := &Node{
		Type: html.ElementNode,
		Data: NewExprRaw("c:page-node"),
		Source: Source{
			File: "logs.chtml",
			Span: Span{Line: 3, Column: 1, Length: 34},
		},
	}

	// Build the error chain: inner -> outer
	httpCallErr := newComponentError(httpCallNode, rootCause)
	pageNodeErr := newComponentError(pageNodeNode, httpCallErr)

	// Test ComponentStack
	stack := pageNodeErr.ComponentStack()

	if len(stack) != 2 {
		t.Errorf("ComponentStack() returned %d frames, want 2", len(stack))
	}

	// First frame should be the outermost (page-node)
	if len(stack) > 0 {
		if stack[0].File != "logs.chtml" {
			t.Errorf("stack[0].File = %q, want %q", stack[0].File, "logs.chtml")
		}
		if stack[0].Line != 3 {
			t.Errorf("stack[0].Line = %d, want 3", stack[0].Line)
		}
	}

	// Second frame should be the inner (http-call)
	if len(stack) > 1 {
		if stack[1].File != "page-node.chtml" {
			t.Errorf("stack[1].File = %q, want %q", stack[1].File, "page-node.chtml")
		}
		if stack[1].Line != 15 {
			t.Errorf("stack[1].Line = %d, want 15", stack[1].Line)
		}
	}

	// Test RootCause
	rc := pageNodeErr.RootCause()
	if rc == nil {
		t.Fatal("RootCause() returned nil")
	}
	if rc.Error() != rootCause.Error() {
		t.Errorf("RootCause() = %q, want %q", rc.Error(), rootCause.Error())
	}

	t.Logf("Full error message: %s", pageNodeErr.Error())
	t.Logf("Root cause: %s", rc.Error())
	for i, frame := range stack {
		t.Logf("Stack frame %d: %s at %s:%d:%d", i, frame.Path(), frame.File, frame.Line, frame.Column)
	}
}

func TestComponentStackSingleError(t *testing.T) {
	// Test with a single ComponentError (no nesting)
	rootCause := errors.New("simple error")
	node := &Node{
		Type: html.ElementNode,
		Data: NewExprRaw("div"),
		Source: Source{
			File: "test.chtml",
			Span: Span{Line: 5, Column: 2, Length: 10},
		},
	}

	ce := newComponentError(node, rootCause)

	stack := ce.ComponentStack()
	if len(stack) != 1 {
		t.Errorf("ComponentStack() returned %d frames, want 1", len(stack))
	}

	rc := ce.RootCause()
	if rc.Error() != rootCause.Error() {
		t.Errorf("RootCause() = %q, want %q", rc.Error(), rootCause.Error())
	}
}

func TestComponentStackWithErrorsJoin(t *testing.T) {
	// Simulate how errors bubble up through nested components with errors.Join
	// This is the pattern used in chtmlComponent.Render()

	// The innermost error (root cause)
	rootCause := errors.New("http call to /api/nodes/xyz failed with status 400")

	// Inner component error (c:http-call in page-node.chtml)
	httpCallNode := &Node{
		Type: html.ElementNode,
		Data: NewExprRaw("c:http-call"),
		Source: Source{
			File: "page-node.chtml",
			Span: Span{Line: 15, Column: 3, Length: 40},
		},
	}
	httpCallErr := newComponentError(httpCallNode, rootCause)

	// Simulate errors.Join wrapping (as chtmlComponent.Render() does)
	joinedErr := errors.Join(httpCallErr)

	// Outer component error (c:page-node in logs.chtml)
	pageNodeNode := &Node{
		Type: html.ElementNode,
		Data: NewExprRaw("c:page-node"),
		Source: Source{
			File: "logs.chtml",
			Span: Span{Line: 3, Column: 1, Length: 34},
		},
	}
	// Wrap with fmt.Errorf as render.go does
	pageNodeErr := newComponentError(pageNodeNode, fmt.Errorf("render import c:page-node: %w", joinedErr))

	// Test ComponentStack - should find both ComponentErrors
	stack := pageNodeErr.ComponentStack()

	if len(stack) != 2 {
		t.Errorf("ComponentStack() returned %d frames, want 2", len(stack))
		for i, frame := range stack {
			t.Logf("  Frame %d: %s at %s:%d", i, frame.Path(), frame.File, frame.Line)
		}
	}

	// First frame should be the outermost (page-node)
	if len(stack) > 0 {
		if stack[0].File != "logs.chtml" {
			t.Errorf("stack[0].File = %q, want %q", stack[0].File, "logs.chtml")
		}
	}

	// Second frame should be the inner (http-call)
	if len(stack) > 1 {
		if stack[1].File != "page-node.chtml" {
			t.Errorf("stack[1].File = %q, want %q", stack[1].File, "page-node.chtml")
		}
	}

	// Test RootCause
	rc := pageNodeErr.RootCause()
	if rc == nil {
		t.Fatal("RootCause() returned nil")
	}
	if rc.Error() != rootCause.Error() {
		t.Errorf("RootCause() = %q, want %q", rc.Error(), rootCause.Error())
	}

	t.Logf("Full error message: %s", pageNodeErr.Error())
	t.Logf("Root cause: %s", rc.Error())
	for i, frame := range stack {
		t.Logf("Stack frame %d: %s at %s:%d:%d", i, frame.Path(), frame.File, frame.Line, frame.Column)
	}
}
