package pages

import "github.com/dpotapov/go-pages/chtml"

type RequestComponent struct{}

func (rc RequestComponent) Render(s chtml.Scope) (any, error) {
	rr := &RequestArg{}
	if v, ok := s.(*scope); ok {
		rr = NewRequestArg(v.globals.req)
	}
	return rr, nil
}

// requestComponentInstance is a singleton instance of the RequestComponent.
// The instance is used to avoid allocating a new one for each request.
var requestComponentInstance = &RequestComponent{}

func RequestComponentFactory() chtml.Component {
	return requestComponentInstance
}
