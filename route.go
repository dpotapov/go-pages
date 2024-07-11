package pages

import "github.com/dpotapov/go-pages/chtml"

type RouteComponent struct{}

func (rc RouteComponent) Render(s chtml.Scope) (any, error) {
	rr := map[string]string{}
	if v, ok := s.(*scope); ok {
		rr = v.globals.route
	}
	return rr, nil
}
