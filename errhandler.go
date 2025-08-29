package pages

import (
	"errors"
	"io/fs"

	"github.com/dpotapov/go-pages/chtml"
)

type errorHandlerComponent struct {
	// comp is the component to render in Render. It could be nil if the Importer failed.
	comp chtml.Component

	// importErr is the error returned by the Importer. It is nil if the Importer succeeded.
	importErr error

	// fallback is the component to render when importErr is not nil or comp.Render return an error.
	fallback chtml.Component

	// compErrs is a list of ComponentError that occurred during parsing or rendering of comp.
	compErrs []*chtml.ComponentError

	// fsys is the FileSystem for reading source files
	fsys fs.FS
}

var _ chtml.Component = &errorHandlerComponent{}

func NewErrorHandlerComponent(name string, imp chtml.Importer, fallback chtml.Component, fsys fs.FS) *errorHandlerComponent {
	comp, err := imp.Import(name)

	return &errorHandlerComponent{
		comp:      comp,
		importErr: err,
		fallback:  fallback,
		fsys:      fsys,
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

	var ce *chtml.ComponentError
	for _, err := range errs {
		if errors.As(err, &ce) {
			eh.compErrs = append(eh.compErrs, ce)
		}
	}

	// Enhanced: Add source code for each error
	var enrichedErrors []map[string]any
	for _, ce := range eh.compErrs {
		sourceCtx := ce.SourceCodeContext(eh.fsys, 3)
		enrichedErrors = append(enrichedErrors, map[string]any{
			"error":  ce,
			"source": sourceCtx,
		})
	}

	ss := s.Spawn(map[string]any{
		"errors": enrichedErrors,
		"fsys":   eh.fsys,
	})

	if eh.fallback == nil {
		return nil, errors.Join(errs...)
	}

	return eh.fallback.Render(ss)
}

func (eh *errorHandlerComponent) InputShape() *chtml.Shape { return nil }
func (eh *errorHandlerComponent) OutputShape() *chtml.Shape           { return chtml.Any }

func (eh *errorHandlerComponent) Dispose() error {
	var errs []error
	if d, ok := eh.comp.(chtml.Disposable); ok {
		errs = append(errs, d.Dispose())
	}
	if d, ok := eh.fallback.(chtml.Disposable); ok {
		errs = append(errs, d.Dispose())
	}
	return errors.Join(errs...)
}
