package chtml

import (
	"errors"
	"fmt"
	"io/fs"
	"runtime"
	"strings"

	"golang.org/x/net/html"
)

var (
	// ErrComponentNotFound is returned by Importer implementations when a component is not found.
	ErrComponentNotFound = errors.New("component not found")

	// ErrImportNotAllowed is returned when an Importer is not set for the component.
	ErrImportNotAllowed = errors.New("imports are not allowed")
)

// captureStack captures a stack trace, skipping the specified number of frames
// plus the frames for runtime.Callers and captureStack itself
func captureStack(skip int) []byte {
	// Use runtime.Stack directly but we need to skip frames manually
	// since runtime.Stack doesn't have a skip parameter
	buf := make([]byte, 64*1024) // Large buffer to capture full stack
	n := runtime.Stack(buf, false)
	if n == 0 {
		return []byte("stack trace unavailable")
	}

	// Parse the stack trace and skip the first few frames
	stack := string(buf[:n])
	lines := strings.Split(stack, "\n")

	// Each frame consists of 2 lines: function name and file:line info
	// Skip frames: captureStack and caller-specified frames (skip+1 total)
	framesToSkip := 1 + skip // captureStack + caller frames
	linesToSkip := framesToSkip * 2

	if len(lines) <= linesToSkip+1 { // +1 for goroutine header
		return []byte("stack trace too short")
	}

	// Keep the goroutine header line and skip the unwanted frames
	filteredLines := []string{lines[0]} // Keep "goroutine N [running]:"
	if len(lines) > linesToSkip+1 {
		filteredLines = append(filteredLines, lines[linesToSkip+1:]...)
	}

	return []byte(strings.Join(filteredLines, "\n"))
}

type UnrecognizedArgumentError struct {
	Name string
}

func (e *UnrecognizedArgumentError) Error() string {
	return fmt.Sprintf("unrecognized argument %s", e.Name)
}

func (e *UnrecognizedArgumentError) Is(target error) bool {
	var ua *UnrecognizedArgumentError
	if errors.As(target, &ua) {
		return e.Name == ua.Name
	}
	return false
}

type DecodeError struct {
	Key string
	Err error
}

func (e *DecodeError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("could not decode %s", e.Key)
	}
	return fmt.Sprintf("could not decode %s: %s", e.Key, e.Err.Error())
}

func (e *DecodeError) Unwrap() error {
	return e.Err
}

func (e *DecodeError) Is(target error) bool {
	var de *DecodeError
	if errors.As(target, &de) {
		return e.Key == de.Key
	}
	return false
}

// CastError is returned when a cast operation fails due to type mismatch.
// Use errors.As to check for CastError and extract details.
type CastError struct {
	Expected *Shape // The shape we tried to cast to
	Actual   any    // The actual value that failed validation
	Err      error  // Underlying error with details (includes path info for nested errors)
}

func (e *CastError) Error() string {
	return fmt.Sprintf("cast to %s failed: %v", e.Expected.String(), e.Err)
}

func (e *CastError) Unwrap() error {
	return e.Err
}

type ComponentError struct {
	err    error
	path   string
	stack  []byte
	File   string `expr:"file"`   // Source file path
	Line   int    `expr:"line"`   // Line number (1-based)
	Column int    `expr:"column"` // Column number (1-based)
	Length int    `expr:"length"` // Span length in bytes
}

func newComponentError(n *Node, err error) *ComponentError {
	ce := &ComponentError{
		err:   err,
		path:  buildErrorPath(n),
		stack: captureStack(1), // Skip newComponentError frame
	}

	// Check if the error is a TypeError with position info
	var typeErr *TypeError
	if errors.As(err, &typeErr) && typeErr.Pos > 0 && n != nil && !n.Source.Span.IsZero() {
		// For TypeErrors with position info, calculate the position within the expression
		ce.File = n.Source.File
		// The TypeError.Pos is a byte offset within the expression,
		// so we add it to the node's starting position
		ce.Line = n.Source.Span.Line
		ce.Column = n.Source.Span.Column + typeErr.Pos
		ce.Length = 1 // Default to highlighting just one character
	} else if n != nil && !n.Source.Span.IsZero() {
		ce.File = n.Source.File
		ce.Line = n.Source.Span.Line
		ce.Column = n.Source.Span.Column
		ce.Length = n.Source.Span.Length
	}
	return ce
}

func (e *ComponentError) Error() string {
	if e.path == "" {
		return e.err.Error()
	}
	return e.path + ": " + e.err.Error()
}

func (e *ComponentError) Unwrap() error {
	return e.err
}

// StackTrace returns the captured stack trace from when the error was created
func (e *ComponentError) StackTrace() string {
	return string(e.stack)
}

// HasSourceLocation returns true if the error has source location information
func (e *ComponentError) HasSourceLocation() bool {
	return e.Line > 0 && e.Column > 0
}

// Path returns the DOM path to the element where the error occurred (e.g., "html/body/c:page-node")
func (e *ComponentError) Path() string {
	return e.path
}

// ComponentStack returns all nested ComponentErrors in the error chain,
// from outermost (this error) to innermost (root cause).
// This allows rendering a "component stack trace" showing the path through
// nested component imports where the error occurred.
func (e *ComponentError) ComponentStack() []*ComponentError {
	var stack []*ComponentError
	// Use a queue to handle both single and multi-error unwrapping
	queue := []error{e}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if current == nil {
			continue
		}

		if ce, ok := current.(*ComponentError); ok {
			stack = append(stack, ce)
			// Add the wrapped error to the queue
			queue = append(queue, ce.err)
		} else if multiUnwrapper, ok := current.(interface{ Unwrap() []error }); ok {
			// Handle errors.Join and similar multi-error types
			queue = append(queue, multiUnwrapper.Unwrap()...)
		} else if unwrapper, ok := current.(interface{ Unwrap() error }); ok {
			// Handle single-error wrapping (like fmt.Errorf with %w)
			queue = append(queue, unwrapper.Unwrap())
		}
		// If none of the above, the error doesn't wrap anything - we're done with this branch
	}
	return stack
}

// RootCause returns the innermost non-ComponentError in the error chain.
// This is the actual error that caused the failure.
func (e *ComponentError) RootCause() error {
	var lastNonComponent error
	queue := []error{e}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if current == nil {
			continue
		}

		if ce, ok := current.(*ComponentError); ok {
			queue = append(queue, ce.err)
		} else if multiUnwrapper, ok := current.(interface{ Unwrap() []error }); ok {
			// Handle errors.Join and similar multi-error types
			queue = append(queue, multiUnwrapper.Unwrap()...)
		} else if unwrapper, ok := current.(interface{ Unwrap() error }); ok {
			// Handle single-error wrapping (like fmt.Errorf with %w)
			lastNonComponent = current
			queue = append(queue, unwrapper.Unwrap())
		} else {
			// Found a leaf error that doesn't wrap anything
			return current
		}
	}
	return lastNonComponent
}

// SourceLine represents a single line of source code with context.
type SourceLine struct {
	Number  int    `expr:"number" json:"number"`
	Text    string `expr:"text" json:"text"`
	IsError bool   `expr:"is_error" json:"is_error"`
}

// SourceLines returns lines of source code around the error location
// with contextLines before and after. Returns nil if source is not available.
func (e *ComponentError) SourceLines(fsys fs.FS, contextLines int) []SourceLine {
	if !e.HasSourceLocation() || e.File == "" {
		return nil
	}

	// Read the file
	content, err := fs.ReadFile(fsys, e.File)
	if err != nil {
		return nil
	}

	// Split into lines
	lines := strings.Split(string(content), "\n")

	// Calculate line range
	startLine := max(1, e.Line-contextLines)
	endLine := min(len(lines), e.Line+contextLines)

	// Extract relevant lines
	var sourceLines []SourceLine
	for i := startLine; i <= endLine; i++ {
		lineText := ""
		if i-1 < len(lines) {
			lineText = lines[i-1]
		}
		sourceLines = append(sourceLines, SourceLine{
			Number:  i,
			Text:    lineText,
			IsError: i == e.Line,
		})
	}

	return sourceLines
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func buildErrorPath(n *Node) string {
	// recursively build path to the node n from the root
	var path []string
	for n != nil {
		if n.Type == html.ElementNode {
			path = append(path, n.Data.RawString())
		}
		n = n.Parent
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return strings.Join(path, "/")
}
