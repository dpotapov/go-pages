package pages

import (
	"net/http"

	"github.com/dpotapov/go-pages/chtml"
)

// Scope wraps chtml.BaseScope to carry global variables.
type scope struct {
	*chtml.BaseScope
	globals *scopeGlobals
}

type scopeGlobals struct {
	req        *http.Request
	statusCode int
	header     http.Header
}

var _ chtml.Scope = (*scope)(nil)

func newScope(vars map[string]any, req *http.Request) *scope {
	return &scope{
		BaseScope: chtml.NewBaseScope(vars),
		globals: &scopeGlobals{
			req:        req,
			statusCode: 0,
			header:     make(http.Header),
		},
	}
}

func (s *scope) Spawn(vars map[string]any) chtml.Scope {
	return &scope{
		BaseScope: s.BaseScope.Spawn(vars).(*chtml.BaseScope),
		globals:   s.globals,
	}
}
