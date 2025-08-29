package pages

import (
	"io/fs"

	"github.com/dpotapov/go-pages/chtml"
)

// sourceCodeComponent provides source code context for errors
type sourceCodeComponent struct {
	fsys fs.FS
}

// NewSourceCodeComponentFactory creates a factory function for the source code component
func NewSourceCodeComponentFactory(fsys fs.FS) func() chtml.Component {
	return func() chtml.Component {
		return &sourceCodeComponent{fsys: fsys}
	}
}

func (s *sourceCodeComponent) Render(scope chtml.Scope) (any, error) {
	// Get the error from scope
	vars := scope.Vars()
	err, ok := vars["error"]
	if !ok {
		return nil, nil
	}
	
	ce, ok := err.(*chtml.ComponentError)
	if !ok || ce == nil {
		return nil, nil
	}
	
	// Get source context
	ctx := ce.SourceCodeContext(s.fsys, 3) // 3 lines before/after
	if ctx == nil {
		return nil, nil
	}
	
	// Return formatted source code data
	return map[string]any{
		"lines":       ctx.Lines,
		"errorLine":   ctx.ErrorLine,
		"errorColumn": ctx.ErrorColumn,
		"errorLength": ctx.ErrorLength,
	}, nil
}

func (s *sourceCodeComponent) InputShape() *chtml.Shape { return nil }
func (s *sourceCodeComponent) OutputShape() *chtml.Shape { return chtml.Any }