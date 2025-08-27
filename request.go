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

func NewRequestComponentFactory() func() chtml.Component {
	instance := &RequestComponent{}
	return func() chtml.Component {
		return instance
	}
}

func (rc RequestComponent) InputShape() *chtml.Shape  { return nil }
func (rc RequestComponent) OutputShape() *chtml.Shape { return chtml.ShapeOf[RequestArg]() }
