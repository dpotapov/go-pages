package pages

import (
	"errors"

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
}

var _ chtml.Component = &errorHandlerComponent{}

func NewErrorHandlerComponent(name string, imp chtml.Importer, fallback chtml.Component) chtml.Component {
	comp, err := imp.Import(name)

	return &errorHandlerComponent{
		comp:      comp,
		importErr: err,
		fallback:  fallback,
	}
}

func (eh *errorHandlerComponent) Render(s chtml.Scope) (*chtml.RenderResult, error) {
	errs := []error{eh.importErr}

	if eh.importErr == nil {
		rr, err := eh.comp.Render(s)
		if err == nil || eh.fallback == nil {
			return rr, err
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

	s.Vars()["errors"] = eh.compErrs

	return eh.fallback.Render(s)
}

func (eh *errorHandlerComponent) ResultSchema() any {
	return nil
}
