package pages

import (
	"fmt"
	"net/http"

	"github.com/dpotapov/go-pages/chtml"
)

type HttpResponseComponent struct{}

// HttpResponseArgs describes accepted inputs for HttpResponseComponent.
// Field names are mapped to snake_case for expression/args binding.
type HttpResponseArgs struct {
    Status   int
    Location string
    Cookies  []*http.Cookie
    Extra    any
}

func (hc HttpResponseComponent) Render(s chtml.Scope) (any, error) {
	var args HttpResponseArgs
	if err := chtml.UnmarshalScope(s, &args); err != nil {
		return nil, fmt.Errorf("unmarshal scope: %w", err)
	}

	ss, ok := s.(*scope)
	if !ok {
		return nil, nil
	}

	if args.Status != 0 {
		ss.globals.statusCode = args.Status
	}
	if args.Location != "" {
		ss.globals.header.Add("Location", args.Location)
	}
	if len(args.Cookies) > 0 {
		ss.globals.header.Add("Set-Cookie", args.Cookies[0].String())
	}
	return nil, nil
}

func (hc HttpResponseComponent) InputShape() *chtml.Shape { return chtml.ShapeOf[HttpResponseArgs]() }
func (hc HttpResponseComponent) OutputShape() *chtml.Shape           { return nil }

func NewHttpResponseComponentFactory() func() chtml.Component {
	instance := &HttpResponseComponent{}
	return func() chtml.Component {
		return instance
	}
}

type CookieComponent struct{}

func (cc CookieComponent) Render(s chtml.Scope) (any, error) {
    var c http.Cookie
    return &c, chtml.UnmarshalScope(s, &c)
}

func (cc CookieComponent) InputShape() *chtml.Shape  { return chtml.ShapeOf[http.Cookie]() }
func (cc CookieComponent) OutputShape() *chtml.Shape { return chtml.ShapeOf[http.Cookie]() }

func NewCookieComponentFactory() func() chtml.Component {
	instance := &CookieComponent{}
	return func() chtml.Component {
		return instance
	}
}
