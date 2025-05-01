package pages

import (
	"fmt"
	"net/http"

	"github.com/dpotapov/go-pages/chtml"
)

type HttpResponseComponent struct{}

func (hc HttpResponseComponent) Render(s chtml.Scope) (any, error) {
	var args struct {
		Status   int
		Location string
		Cookies  []*http.Cookie
		Extra    any
	}
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

func NewCookieComponentFactory() func() chtml.Component {
	instance := &CookieComponent{}
	return func() chtml.Component {
		return instance
	}
}
