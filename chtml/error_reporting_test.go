package chtml

import (
	"errors"
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
			if ce.SourceFile() != "example.chtml" {
				t.Errorf("got file %q, want %q", ce.SourceFile(), "example.chtml")
			}
			if ce.SourceLine() < 1 || ce.SourceLine() > 3 {
				t.Errorf("got line %d, want between 1-3", ce.SourceLine())
			}
			if ce.SourceColumn() < 1 {
				t.Errorf("got column %d, want >= 1", ce.SourceColumn())
			}
			if ce.SourceLength() < 1 {
				t.Errorf("got length %d, want >= 1", ce.SourceLength())
			}
			
			t.Logf("Error with span info: %s at %s:%d:%d (length %d)", 
				ce.Error(), ce.SourceFile(), ce.SourceLine(), ce.SourceColumn(), ce.SourceLength())
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

	// Test all accessor methods
	if ce.SourceFile() != "test.chtml" {
		t.Errorf("SourceFile() = %q, want %q", ce.SourceFile(), "test.chtml")
	}
	if ce.SourceLine() != 2 {
		t.Errorf("SourceLine() = %d, want %d", ce.SourceLine(), 2)
	}
	if ce.SourceColumn() != 5 {
		t.Errorf("SourceColumn() = %d, want %d", ce.SourceColumn(), 5)
	}
	if ce.SourceLength() != 15 {
		t.Errorf("SourceLength() = %d, want %d", ce.SourceLength(), 15)
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