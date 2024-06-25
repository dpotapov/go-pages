package pages

import "github.com/dpotapov/go-pages/chtml"

type RequestComponent struct{}

var _ chtml.Component = &RequestComponent{}

func (r *RequestComponent) Render(s chtml.Scope) (*chtml.RenderResult, error) {
	rr := &chtml.RenderResult{}
	if v, ok := s.(*scope); ok {
		rr.Data = NewRequestArg(v.req)
	}
	return rr, nil
}

func (r *RequestComponent) ResultSchema() any {
	return RequestArg{}
}
