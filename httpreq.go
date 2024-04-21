package pages

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"github.com/dpotapov/go-pages/chtml"

	"golang.org/x/net/html"
)

// HttpRequestComponent implements a CHTML component for making HTTP requests and storing
// returned data in the scope.
//
// Usage example:
//   <c:http-request url="/api/data" method="POST" var="data" interval="15s">
//     { "input1": "value1", "input2": "value2" }
//   </c:http-request>
//
// In this example, the component will make a POST request to /api/data every 15 seconds and store
// the response in the scope under the key "data". If the response data has changed, the component
// will re-render the page.
// By default, the interval is 0, which means the component will only make the request once.
// The interval can be set to a duration string, such as "15s" or "1m".
// If var is not set, the response data will not be stored in the scope.
// If url is not set, the component will not make a request.
type HttpRequestComponent struct {
	Router http.Handler
}

var _ chtml.Component = &HttpRequestComponent{}

// args is a helper function that extracts the arguments from the scope.
func (r *HttpRequestComponent) args(s chtml.Scope) (*httpRequestArgs, error) {
	p := &httpRequestArgs{
		Method:   "GET",
		URL:      "",
		Var:      "",
		Interval: 0,
	}
	vars := s.Vars()
	if v, ok := vars["method"].(string); ok && v != "" {
		p.Method = v
	}
	if v, ok := vars["url"].(string); ok {
		p.URL = v
	}
	if v, ok := vars["var"].(string); ok {
		p.Var = v
	}
	if v, ok := vars["interval"].(string); ok {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("invalid interval: %v", err)
		}
		p.Interval = d
	}
	if v, ok := vars["interval"].(time.Duration); ok {
		p.Interval = v
	}
	if v, ok := vars["_"].(*html.Node); ok {
		// collect all text nodes into a buffer:
		var buf bytes.Buffer
		for child := v.FirstChild; child != nil; child = child.NextSibling {
			if child.Type == html.TextNode {
				buf.WriteString(child.Data)
			}
		}
		p.Body = &buf
	}
	return p, nil
}

func (r *HttpRequestComponent) Execute(ctx context.Context, s chtml.Scope) error {
	if r.Router == nil {
		return fmt.Errorf("http router not set")
	}

	args, err := r.args(s)
	if err != nil {
		return fmt.Errorf("get arg: %w", err)
	}

	poller, ok := s.Vars()[args.Var].(*httpRequestPoller)
	if !ok {
		poller = newHttpRequestPoller(r.Router, s.Closed())
		poller.onChange = s.Touch
		s.Vars()[args.Var] = poller
	}

	// start polling if the interval is set and the poller is not already polling
	if args.Interval > 0 {
		if !poller.polling {
			poller.polling = true
			poller.argsC <- args
			go poller.start(args.Interval)
		}
	} else {
		poller.execute(args)
	}

	return nil
}

type httpRequestArgs struct {
	Method   string
	URL      string
	Var      string
	Interval time.Duration
	Body     io.Reader
}

type httpRequestPoller struct {
	Code  int
	Body  string
	Json  any
	Error string

	// router is the HTTP router used to make requests
	router http.Handler

	// onChange is called when the poller has new data
	onChange func()

	// argsC communicates new arguments to the poller
	argsC chan *httpRequestArgs

	// polling is true if the poller is currently polling
	polling bool

	// closed is a notification channel telling the poller to stop
	closed <-chan struct{}

	// mu protects public fields
	mu sync.Mutex
}

func newHttpRequestPoller(router http.Handler, c <-chan struct{}) *httpRequestPoller {
	p := &httpRequestPoller{
		router: router,
		argsC:  make(chan *httpRequestArgs, 1),
		closed: c,
	}
	return p
}

func (p *httpRequestPoller) start(interval time.Duration) {
	var args *httpRequestArgs
	for p.polling {
		select {
		case <-p.closed:
			p.polling = false
			return
		case newArgs := <-p.argsC:
			interval = newArgs.Interval
			if interval <= 0 {
				p.polling = false
			}
			args = newArgs
		case <-time.After(interval):
		}
		if args != nil {
			p.execute(args)
		}
	}

}

// execute makes an HTTP call
func (p *httpRequestPoller) execute(args *httpRequestArgs) {
	p.mu.Lock()
	defer p.mu.Unlock()

	updVars := func(res *http.Response, err error) {
		var bodyStr, errStr string
		var code int

		if res != nil {
			code = res.StatusCode
			body, err2 := io.ReadAll(res.Body)
			if err2 != nil && err != nil {
				err = fmt.Errorf("read body: %v", err2)
			} else if err2 == nil {
				bodyStr = string(body)
			}

			if res.Header.Get("Content-Type") == "application/json" && bodyStr != "" {
				err2 := json.Unmarshal([]byte(bodyStr), &p.Json)
				if err2 != nil && err != nil {
					err = fmt.Errorf("unmarshal json: %w", err)
				}
			}
		}

		if err != nil {
			errStr = err.Error()
		}

		changed := p.Code != code || p.Body != bodyStr || p.Error != errStr

		p.Code = code
		p.Body = bodyStr
		p.Error = errStr

		if changed && p.onChange != nil {
			p.onChange()
		}
	}

	req, err := http.NewRequest(args.Method, args.URL, args.Body)
	if err != nil {
		updVars(nil, fmt.Errorf("create request: %w", err))
		return
	}

	rr := httptest.NewRecorder()
	p.router.ServeHTTP(rr, req)
	updVars(rr.Result(), nil)
}
