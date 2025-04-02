package pages

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"time"

	"github.com/dpotapov/go-pages/chtml"
)

// HttpCallComponent implements a CHTML component for making HTTP requests and storing
// returned data in the scope.
type HttpCallComponent struct {
	// router is the HTTP router used to make requests
	router http.Handler

	// mu protects pollingStop and currentInterval
	mu sync.Mutex

	// pollingStop is a channel to signal the polling goroutine to stop
	pollingStop chan struct{}

	// currentInterval is the current polling interval
	currentInterval time.Duration

	// lastArgs is the last arguments used to make the request
	lastArgs *HttpCallArgs

	// lastResponse is the last response received
	lastResponse *HttpCallResponse
}

var _ chtml.Component = &HttpCallComponent{}
var _ chtml.Disposable = &HttpCallComponent{}

type HttpCallArgs struct {
	Method            string
	URL               string
	Interval          time.Duration
	BasicAuthUsername string
	BasicAuthPassword string
	Cookies           []*http.Cookie
	Header            http.Header
	Body              io.Reader
}

type HttpCallResponse struct {
	Code  int    `expr:"code"`
	Body  any    `expr:"body"`
	Error string `expr:"error"`
}

func NewHttpCallComponent(router http.Handler) *HttpCallComponent {
	p := &HttpCallComponent{
		router: router,
	}
	return p
}

func (c *HttpCallComponent) Render(s chtml.Scope) (any, error) {
	if c.router == nil {
		return nil, fmt.Errorf("http router not set")
	}

	var args HttpCallArgs
	if err := chtml.UnmarshalScope(s, &args); err != nil {
		return nil, fmt.Errorf("unmarshal scope: %w", err)
	}

	if s.DryRun() || args.URL == "" {
		return &HttpCallResponse{}, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.lastArgs = &args

	if args.Interval == 0 {
		// Stop the existing poller if the interval is 0
		if c.pollingStop != nil {
			close(c.pollingStop)
			c.pollingStop = nil
		}
	} else if args.Interval != c.currentInterval {
		// Stop the existing poller and start a new one if the interval has changed
		if c.pollingStop != nil {
			close(c.pollingStop)
		}
		c.pollingStop = make(chan struct{})
		c.currentInterval = args.Interval
		go c.startPolling(s, c.pollingStop)
	}

	return c.render(&args), nil
}

func (c *HttpCallComponent) Dispose() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Stop the existing poller if it is running
	if c.pollingStop != nil {
		close(c.pollingStop)
		c.pollingStop = nil
	}
	return nil
}

func (c *HttpCallComponent) startPolling(s chtml.Scope, stopChan chan struct{}) {
	ticker := time.NewTicker(c.currentInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.mu.Lock()
			newResponse := c.render(c.lastArgs)
			if c.hasResponseChanged(newResponse) {
				c.lastResponse = newResponse
				s.Touch()
			}
			c.mu.Unlock()
		case <-stopChan:
			return
		}
	}
}

func (c *HttpCallComponent) hasResponseChanged(newResponse *HttpCallResponse) bool {
	if c.lastResponse == nil {
		return true
	}
	return c.lastResponse.Code != newResponse.Code ||
		!reflect.DeepEqual(c.lastResponse.Body, newResponse.Body) ||
		c.lastResponse.Error != newResponse.Error
}

// render makes an HTTP call
func (c *HttpCallComponent) render(args *HttpCallArgs) *HttpCallResponse {
	if args.Method == "" {
		args.Method = "GET"
	}

	req, err := http.NewRequest(args.Method, args.URL, args.Body)
	if err != nil {
		return c.makeResponse(nil, fmt.Errorf("create request: %w", err))
	}
	req.RequestURI = args.URL

	if args.BasicAuthUsername != "" || args.BasicAuthPassword != "" {
		req.SetBasicAuth(args.BasicAuthUsername, args.BasicAuthPassword)
	}

	if len(args.Header) > 0 {
		req.Header = args.Header
	}

	for _, cookie := range args.Cookies {
		req.AddCookie(cookie)
	}

	rr := httptest.NewRecorder()
	c.router.ServeHTTP(rr, req)

	return c.makeResponse(rr.Result(), nil)
}

func (c *HttpCallComponent) makeResponse(res *http.Response, err error) *HttpCallResponse {
	var r HttpCallResponse

	if res != nil {
		r.Code = res.StatusCode
		body, err2 := io.ReadAll(res.Body)
		if err2 != nil && err != nil {
			err = fmt.Errorf("read body: %v", err2)
		}

		jsonContentTypes := false
		for _, ct := range []string{"application/json", "application/problem+json"} {
			if res.Header.Get("Content-Type") == ct {
				jsonContentTypes = true
				break
			}
		}

		if jsonContentTypes && r.Body != "" {
			err2 := json.Unmarshal(body, &r.Body)
			if err2 != nil && err != nil {
				err = fmt.Errorf("unmarshal json: %w", err)
			}
		} else if res.Header.Get("Content-Type") == "text/plain" {
			r.Body = string(body)
		} else {
			r.Body = body
		}
	}

	if err != nil {
		r.Error = err.Error()
	}

	return &r
}
