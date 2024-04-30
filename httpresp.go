package pages

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/dpotapov/go-pages/chtml"
)

type HttpResponseComponent struct {
}

type httpResponseArgs struct {
	Status   int
	Location string
}

var _ chtml.Component = &HttpResponseComponent{}

// args is a helper function that extracts the arguments from the scope.
func (r *HttpResponseComponent) args(s chtml.Scope) (*httpResponseArgs, error) {
	p := &httpResponseArgs{}

	vars := s.Vars()
	if v, ok := vars["status"].(string); ok && v != "" {
		c, _ := strconv.ParseUint(v, 10, 64)
		p.Status = int(c)
	}
	if v, ok := vars["location"].(string); ok {
		p.Location = v
	}
	return p, nil
}

func (r *HttpResponseComponent) Render(s chtml.Scope) (*chtml.RenderResult, error) {
	args, err := r.args(s)
	if err != nil {
		return nil, fmt.Errorf("get arg: %w", err)
	}

	rr := &chtml.RenderResult{}

	if args.Status != 0 {
		rr.StatusCode = args.Status
	}
	if args.Location != "" {
		if rr.Header == nil {
			rr.Header = make(http.Header)
		}
		rr.Header.Add("Location", args.Location)
	}
	return rr, nil
}
