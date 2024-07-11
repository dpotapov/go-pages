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
	}
	if err := chtml.UnmarshalScope(s, &args); err != nil {
		return nil, fmt.Errorf("unmarshal scope: %w", err)
	}

	ss, ok := s.(*scope)
	if !ok {
		return nil, fmt.Errorf("invalid scope type")
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

type CookieComponent struct{}

func (cc CookieComponent) Render(s chtml.Scope) (any, error) {
	var c http.Cookie
	return &c, chtml.UnmarshalScope(s, &c)
}
