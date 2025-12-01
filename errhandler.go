package pages

import (
	"errors"
	"fmt"
	"html"
	"io/fs"
	"strings"

	"github.com/dpotapov/go-pages/chtml"
)

// ErrorView contains all information needed to render an error in templates.
// All fields use expr tags to enable snake_case access in CHTML templates.
type ErrorView struct {
	Err       error  `expr:"err"`        // Original error object
	Type      string `expr:"type"`       // A hint how to render the error (e.g. "http_call", "generic")
	RootCause string `expr:"root_cause"` // Innermost error message unwrapped from the error chain
	GoStack   string `expr:"go_stack"`   // Go runtime stack trace captured when the error was created

	// Stack frames from outermost to innermost component error.
	// The first frame contains the location where the error was raised.
	Stack []*ErrorViewFrame `expr:"stack"`
}

// ErrorViewFrame represents a single frame in the component error stack.
type ErrorViewFrame struct {
	Path        string             `expr:"path"`
	File        string             `expr:"file"`
	Line        int                `expr:"line"`
	Column      int                `expr:"column"`
	Length      int                `expr:"length"`
	SourceLines []chtml.SourceLine `expr:"source_lines"`
}

type errorHandlerComponent struct {
	// comp is the component to render in Render. It could be nil if the Importer failed.
	comp chtml.Component

	// importErr is the error returned by the Importer. It is nil if the Importer succeeded.
	importErr error

	// imp is the importer used to import the fallback component lazily.
	imp chtml.Importer

	// fallbackName is the name of the component to render when importErr is not nil or
	// comp.Render returns an error. The component is imported on-demand when an error occurs.
	fallbackName string

	// fsys is the FileSystem for reading source files
	fsys fs.FS
}

var _ chtml.Component = &errorHandlerComponent{}

func NewErrorHandlerComponent(name string, imp chtml.Importer, fallbackName string, fsys fs.FS) *errorHandlerComponent {
	comp, err := imp.Import(name)

	return &errorHandlerComponent{
		comp:         comp,
		importErr:    err,
		imp:          imp,
		fallbackName: fallbackName,
		fsys:         fsys,
	}
}

func (eh *errorHandlerComponent) Render(s chtml.Scope) (any, error) {
	errs := []error{eh.importErr}

	if eh.importErr == nil {
		rr, err := eh.comp.Render(s)
		if err == nil {
			return rr, nil
		}
		errs[0] = err
	}

	if multierr, ok := errs[0].(interface{ Unwrap() []error }); ok {
		errs = multierr.Unwrap()
	}

	// Build error views for template rendering
	errorViews := make([]*ErrorView, 0, len(errs))
	for _, err := range errs {
		if err == nil {
			continue
		}
		errorViews = append(errorViews, eh.toErrorView(err))
	}

	ss := s.Spawn(map[string]any{
		"errors": errorViews,
		"fsys":   eh.fsys,
	})

	if eh.fallbackName == "" {
		return nil, errors.Join(errs...)
	}

	// Import the fallback component on-demand when an error occurs.
	// This allows the error component to be updated without restarting the server.
	fallback, err := eh.imp.Import(eh.fallbackName)
	if err != nil {
		// If the error component itself fails to import, return both the original errors
		// and the import error so we don't lose context about what went wrong.
		importErr := fmt.Errorf("import error component %q: %w", eh.fallbackName, err)
		return nil, errors.Join(append(errs, importErr)...)
	}
	defer func() {
		if d, ok := fallback.(chtml.Disposable); ok {
			_ = d.Dispose()
		}
	}()

	result, err := fallback.Render(ss)
	if err != nil {
		// Error handler component failed - render a basic HTML fallback
		// Set HTTP 500 status code
		if sc, ok := ss.(*scope); ok {
			sc.globals.statusCode = 500
		}
		return eh.renderBuiltinFallback(errs, err), nil
	}
	return result, nil
}

// renderBuiltinFallback renders a minimal HTML error page when the custom error handler fails.
func (eh *errorHandlerComponent) renderBuiltinFallback(originalErrs []error, handlerErr error) string {
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<title>Error</title>
<style>
body { font-family: system-ui, -apple-system, sans-serif; max-width: 800px; margin: 40px auto; padding: 0 20px; background: #1a1a2e; color: #eee; }
h1 { color: #ff6b6b; }
.error { background: #16213e; border-left: 4px solid #ff6b6b; padding: 16px; margin: 16px 0; border-radius: 4px; }
.error-title { font-weight: bold; color: #ff6b6b; margin-bottom: 8px; }
.error-detail { color: #aaa; white-space: pre-wrap; font-family: monospace; font-size: 13px; }
.handler-error { border-left-color: #ffa502; }
.handler-error .error-title { color: #ffa502; }
.note { color: #888; font-size: 12px; margin-top: 24px; }
</style>
</head>
<body>
<h1>An error occurred</h1>
`)
	// Show handler error first
	b.WriteString(`<div class="error handler-error">`)
	b.WriteString(`<div class="error-title">Error handler failed</div>`)
	b.WriteString(`<div class="error-detail">`)
	b.WriteString(html.EscapeString(handlerErr.Error()))
	b.WriteString(`</div></div>`)

	// Show original errors
	b.WriteString(`<h2 style="color:#eee;margin-top:32px;">Original errors:</h2>`)
	for _, err := range originalErrs {
		if err == nil {
			continue
		}
		b.WriteString(`<div class="error">`)
		b.WriteString(`<div class="error-title">`)
		// Try to get error type
		var ce *chtml.ComponentError
		if errors.As(err, &ce) {
			b.WriteString(html.EscapeString(ce.Path()))
		} else {
			b.WriteString("Error")
		}
		b.WriteString(`</div>`)
		b.WriteString(`<div class="error-detail">`)
		b.WriteString(html.EscapeString(err.Error()))
		b.WriteString(`</div></div>`)
	}

	b.WriteString(`<p class="note">This is a fallback error page. The custom error handler failed to render.</p>`)
	b.WriteString(`</body></html>`)
	return b.String()
}

// toErrorView converts an error into an ErrorView for template rendering.
func (eh *errorHandlerComponent) toErrorView(err error) *ErrorView {
	var ce *chtml.ComponentError
	if !errors.As(err, &ce) {
		// Plain error - minimal information
		return &ErrorView{
			Err:       err,
			Type:      "generic",
			RootCause: err.Error(),
			Stack:     []*ErrorViewFrame{},
		}
	}

	// ComponentError - extract rich information
	// Use the root cause error (the actual underlying error) instead of the ComponentError wrapper
	rc := ce.RootCause()
	if rc == nil {
		// If RootCause returns nil, use the ComponentError itself
		rc = ce
	}

	view := &ErrorView{
		Err:       rc,
		Type:      "generic",
		GoStack:   ce.StackTrace(),
		RootCause: rc.Error(),
	}

	// Build stack frames from component stack
	for _, frame := range ce.ComponentStack() {
		sf := &ErrorViewFrame{
			Path:        frame.Path(),
			File:        frame.File,
			Line:        frame.Line,
			Column:      frame.Column,
			Length:      frame.Length,
			SourceLines: frame.SourceLines(eh.fsys, 3),
		}
		view.Stack = append(view.Stack, sf)
	}

	// Type detection
	var httpErr *HttpCallError
	if errors.As(ce, &httpErr) {
		view.Type = "http_call"
	}

	var decodeErr *chtml.DecodeError
	if errors.As(ce, &decodeErr) {
		view.Type = "decode"
	}

	return view
}

func (eh *errorHandlerComponent) InputShape() *chtml.Shape  { return nil }
func (eh *errorHandlerComponent) OutputShape() *chtml.Shape { return chtml.Any }

func (eh *errorHandlerComponent) Dispose() error {
	if d, ok := eh.comp.(chtml.Disposable); ok {
		return d.Dispose()
	}
	return nil
}
