package pages

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/dpotapov/go-pages/chtml"
	"golang.org/x/net/html"
)

type HttpResponseComponent struct {
}

type httpResponseArgs struct {
	Status   int
	Location string
	Cookies  []*http.Cookie
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
	if v, ok := vars["cookies"].(*html.Node); ok {
		for c := v.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && c.Data == "cookie" {
				cookie := &http.Cookie{}
				for _, attr := range c.Attr {
					switch attr.Key {
					case "name":
						cookie.Name = attr.Val
					case "secure":
						cookie.Secure = true
					case "httponly":
						cookie.HttpOnly = true
					}
				}
				for c := c.FirstChild; c != nil; c = c.NextSibling {
					if c.Type == html.TextNode {
						cookie.Value = c.Data
					}
				}
				if cookie.Name != "" {
					p.Cookies = append(p.Cookies, cookie)
				}
			}
		}
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
	if len(args.Cookies) > 0 {
		rr.Header.Add("Set-Cookie", args.Cookies[0].String())
	}
	return rr, nil
}

func (r *HttpResponseComponent) ResultSchema() any {
	return nil
}
