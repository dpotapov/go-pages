package pages

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"github.com/dpotapov/go-pages/chtml"
)

func TestHttpCallComponent_Render(t *testing.T) {
	type wantResponse struct {
		Code  int
		Data  any
		Error any
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/data", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data": "hello"}`))
	})

	mux.HandleFunc("/api/cookies", func(w http.ResponseWriter, r *http.Request) {
		cookies := r.Cookies()
		data := map[string]any{
			"cookies": make([]string, 0, len(cookies)),
		}
		for _, c := range cookies {
			data["cookies"] = append(data["cookies"].([]string), c.Name+"="+c.Value)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(data)
	})

	tests := []struct {
		name         string
		vars         map[string]any
		req          *http.Request
		wantResponse *wantResponse // When ignore_http_error=true, expect full HttpCallResponse
		wantData     any           // When ignore_http_error=false (default), expect data directly
		wantNil      bool          // When no URL provided, expect nil
	}{
		{
			name:    "noArgs",
			vars:    map[string]any{},
			req:     nil,
			wantNil: true,
		},
		{
			name:    "noURL",
			vars:    map[string]any{},
			req:     nil,
			wantNil: true,
		},
		{
			name: "getData",
			vars: map[string]any{
				"url": "/api/data",
			},
			req:      nil,
			wantData: map[string]any{"data": "hello"},
		},
		{
			name: "passthrough cookies",
			vars: map[string]any{
				"url":               "/api/cookies",
				"ignore_http_error": true,
			},
			req: func() *http.Request {
				r := httptest.NewRequest("GET", "/original", nil)
				r.AddCookie(&http.Cookie{Name: "session", Value: "xyz123"})
				r.AddCookie(&http.Cookie{Name: "theme", Value: "dark"})
				return r
			}(),
			wantResponse: &wantResponse{
				Code: 200,
				Data: map[string]any{
					"cookies": []any{"session=xyz123", "theme=dark"},
				},
			},
		},
		{
			name: "explicit cookies override request cookies",
			vars: map[string]any{
				"url":               "/api/cookies",
				"ignore_http_error": true,
				"cookies": []*http.Cookie{
					{Name: "explicit", Value: "cookie"},
				},
			},
			req: func() *http.Request {
				r := httptest.NewRequest("GET", "/original", nil)
				r.AddCookie(&http.Cookie{Name: "session", Value: "xyz123"})
				return r
			}(),
			wantResponse: &wantResponse{
				Code: 200,
				Data: map[string]any{
					"cookies": []any{"explicit=cookie"},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var s chtml.Scope
			if tt.req != nil {
				s = newScope(tt.vars, tt.req)
			} else {
				s = chtml.NewBaseScope(tt.vars)
			}

			comp := NewHttpCallComponent(mux)
			defer func() { _ = comp.Dispose() }()

			rr, err := comp.Render(s)
			if err != nil {
				t.Errorf("Render() error = %v", err)
				return
			}

			if tt.wantNil {
				if rr != nil {
					t.Errorf("Render() got = %v, want nil", rr)
				}
				return
			}

			if tt.wantResponse != nil {
				// Expect full HttpCallResponse
				got, ok := rr.(*HttpCallResponse)
				if !ok {
					t.Errorf("Render() got = %T, want *HttpCallResponse", rr)
					return
				}
				if got.Code != tt.wantResponse.Code {
					t.Errorf("Render() got.Code = %v, want %v", got.Code, tt.wantResponse.Code)
				}
				if !reflect.DeepEqual(got.Data, tt.wantResponse.Data) {
					t.Errorf("Render() got.Data = %v, want %v", got.Data, tt.wantResponse.Data)
				}
				if got.Error != tt.wantResponse.Error {
					t.Errorf("Render() got.Error = %v, want %v", got.Error, tt.wantResponse.Error)
				}
			} else if tt.wantData != nil {
				// Expect data directly
				if !reflect.DeepEqual(rr, tt.wantData) {
					t.Errorf("Render() got = %v, want %v", rr, tt.wantData)
				}
			}
		})
	}
}

func TestHttpCallComponent_WithInterval(t *testing.T) {
	var wg sync.WaitGroup
	testData := []string{"monday", "tuesday", "wednesday"}
	wg.Add(2)

	s := newScope(map[string]any{
		"url":      "/api/data",
		"interval": "1s",
	}, nil)

	done := make(chan struct{})
	defer close(done)

	go func() {
		for {
			select {
			case <-s.Touched():
				t.Logf("scope touched")
				wg.Done()
			case <-done:
				return
			}
		}
	}()

	mux := http.NewServeMux()

	// the /api/data handler will return the first element of testData on each request
	// and shift the testData slice
	mux.HandleFunc("/api/data", func(w http.ResponseWriter, r *http.Request) {
		if len(testData) == 0 {
			t.Errorf("unexpected request")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": testData[0],
		})
		testData = testData[1:]

		if len(testData) == 0 {
			delete(s.Vars(), "interval")
		}
	})

	comp := NewHttpCallComponent(mux)
	defer func() { _ = comp.Dispose() }()

	if _, err := comp.Render(s); err != nil {
		t.Errorf("Render() error = %v", err)
		return
	}

	// wait for the poller to update 3 times
	wg.Wait()
}

func TestHttpCallComponent_SetCookieHeaders(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/set-cookies", func(w http.ResponseWriter, r *http.Request) {
		// Set several cookies in the response
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "abc123", Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "theme", Value: "light", Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "language", Value: "en", Path: "/"})

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status": "cookies-set"}`))
	})

	// Create a scope with the test URL (use ignore_http_error to get full response)
	req := httptest.NewRequest("GET", "/original", nil)
	s := newScope(map[string]any{"url": "/api/set-cookies", "ignore_http_error": true}, req)

	// Create the component and render
	comp := NewHttpCallComponent(mux)
	defer func() { _ = comp.Dispose() }()

	resp, err := comp.Render(s)
	if err != nil {
		t.Errorf("Render() error = %v", err)
		return
	}

	// Verify response
	httpResp, ok := resp.(*HttpCallResponse)
	if !ok {
		t.Errorf("Expected HttpCallResponse, got %T", resp)
		return
	}

	if httpResp.Code != 200 {
		t.Errorf("Expected status 200, got %d", httpResp.Code)
	}

	// Check that the response contains Set-Cookie values
	if len(httpResp.SetCookie) != 3 {
		t.Errorf("Expected 3 SetCookie values in response, got %d", len(httpResp.SetCookie))
	}

	// Check that the global headers contain the Set-Cookie headers
	setCookies := s.globals.header.Values("Set-Cookie")
	if len(setCookies) != 3 {
		t.Errorf("Expected 3 Set-Cookie headers in scope globals, got %d", len(setCookies))
	}

	// Verify each cookie is present in the headers
	cookieNames := []string{"session", "theme", "language"}
	for _, name := range cookieNames {
		// Check in response SetCookie field
		foundInResp := false
		for _, cookie := range httpResp.SetCookie {
			if cookie[:len(name)+1] == name+"=" {
				foundInResp = true
				break
			}
		}
		if !foundInResp {
			t.Errorf("Expected cookie %s in response SetCookie field, but not found", name)
		}

		// Check in scope globals headers
		foundInGlobals := false
		for _, cookie := range setCookies {
			if cookie[:len(name)+1] == name+"=" {
				foundInGlobals = true
				break
			}
		}
		if !foundInGlobals {
			t.Errorf("Expected cookie %s in scope globals headers, but not found", name)
		}
	}

	// Test polling behavior: cookies should be in response but not in headers
	// Create a scope with interval for polling (use ignore_http_error to get full response)
	pollingReq := httptest.NewRequest("GET", "/original", nil)
	sPolling := newScope(map[string]any{
		"url":               "/api/set-cookies",
		"interval":          "1s",
		"ignore_http_error": true,
	}, pollingReq)

	// Verify no Set-Cookie headers are already in the scope
	if len(sPolling.globals.header.Values("Set-Cookie")) > 0 {
		t.Errorf("Expected no Set-Cookie headers in polling scope globals before test")
	}

	// Create the component and render
	compPolling := NewHttpCallComponent(mux)
	defer func() { _ = compPolling.Dispose() }()

	respPolling, err := compPolling.Render(sPolling)
	if err != nil {
		t.Errorf("Render() error = %v", err)
		return
	}

	// Verify response
	httpRespPolling, ok := respPolling.(*HttpCallResponse)
	if !ok {
		t.Errorf("Expected HttpCallResponse, got %T", respPolling)
		return
	}

	// Check that the response contains Set-Cookie values
	if len(httpRespPolling.SetCookie) != 3 {
		t.Errorf("Expected 3 SetCookie values in polling response, got %d", len(httpRespPolling.SetCookie))
	}

	// In polling mode, the Set-Cookie headers should NOT be added to scope globals
	if len(sPolling.globals.header.Values("Set-Cookie")) > 0 {
		t.Errorf("Expected no Set-Cookie headers in polling scope globals, got %d",
			len(sPolling.globals.header.Values("Set-Cookie")))
	}
}

func TestHttpCallComponent_ErrorHandling(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/not-found", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Header().Set("Content-Type", "application/problem+json")
		_, _ = w.Write([]byte(`{"detail": "Resource not found"}`))
	})

	mux.HandleFunc("/api/forbidden", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Header().Set("Content-Type", "application/problem+json")
		_, _ = w.Write([]byte(`{"detail": "Access denied"}`))
	})

	mux.HandleFunc("/api/success", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status": "ok"}`))
	})

	tests := []struct {
		name        string
		url         string
		expectError bool
		expectData  bool
		wantData    map[string]any
	}{
		{
			name:        "404 returns HttpCallError",
			url:         "/api/not-found",
			expectError: true,
			expectData:  false,
		},
		{
			name:        "403 returns HttpCallError",
			url:         "/api/forbidden",
			expectError: true,
			expectData:  false,
		},
		{
			name:        "200 returns data directly",
			url:         "/api/success",
			expectError: false,
			expectData:  true,
			wantData:    map[string]any{"status": "ok"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := chtml.NewBaseScope(map[string]any{"url": tt.url})
			comp := NewHttpCallComponent(mux)
			defer func() { _ = comp.Dispose() }()

			rr, err := comp.Render(s)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected HttpCallError, got nil")
					return
				}
				httpErr, ok := err.(*HttpCallError)
				if !ok {
					t.Errorf("expected HttpCallError, got %T: %v", err, err)
					return
				}
				if httpErr.Response.Code < 200 || httpErr.Response.Code >= 300 {
					// Good, it's a non-2xx code
				} else {
					t.Errorf("expected non-2xx status code in error, got %d", httpErr.Response.Code)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
					return
				}
				if tt.expectData {
					if !reflect.DeepEqual(rr, tt.wantData) {
						t.Errorf("got data = %v, want %v", rr, tt.wantData)
					}
				}
			}
		})
	}
}

func TestHttpCallComponent_IgnoreHttpErrorAttribute(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/not-found", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Header().Set("Content-Type", "application/problem+json")
		_, _ = w.Write([]byte(`{"detail": "Resource not found"}`))
	})

	// With ignore_http_error=true, should return full response even on 404
	s := chtml.NewBaseScope(map[string]any{
		"url":               "/api/not-found",
		"ignore_http_error": true,
	})
	comp := NewHttpCallComponent(mux)
	defer func() { _ = comp.Dispose() }()

	rr, err := comp.Render(s)
	if err != nil {
		t.Errorf("unexpected error with ignore_http_error=true: %v", err)
		return
	}

	resp, ok := rr.(*HttpCallResponse)
	if !ok {
		t.Errorf("expected HttpCallResponse, got %T", rr)
		return
	}

	if resp.Code != http.StatusNotFound {
		t.Errorf("expected status %d, got %d", http.StatusNotFound, resp.Code)
	}

	if resp.Success {
		t.Errorf("expected Success=false for 404, got true")
	}
}

func TestHttpCallComponent_QueryMerging(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/query", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = r.ParseForm()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"query": r.Form,
		})
	})

	// URL already has ?foo=bar, Query adds foo=baz and x=1,x=2
	// Use ignore_http_error to get full response for this test
	vars := map[string]any{
		"url":               "/api/query?foo=bar",
		"query":             map[string]any{"foo": "baz", "x": []string{"1", "2"}},
		"ignore_http_error": true,
	}
	s := chtml.NewBaseScope(vars)
	comp := NewHttpCallComponent(mux)
	defer func() { _ = comp.Dispose() }()

	rr, err := comp.Render(s)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	resp, ok := rr.(*HttpCallResponse)
	if !ok {
		t.Fatalf("expected HttpCallResponse, got %T", rr)
	}
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any in response data, got %T", resp.Data)
	}
	query, ok := data["query"].(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any for query, got %T", data["query"])
	}
	want := map[string][]string{"foo": {"bar", "baz"}, "x": {"1", "2"}}
	for k, wantVals := range want {
		gotValsIface, ok := query[k]
		if !ok {
			t.Errorf("missing key %q in query", k)
			continue
		}
		gotVals := make([]string, 0)
		for _, v := range gotValsIface.([]any) {
			gotVals = append(gotVals, v.(string))
		}
		if !reflect.DeepEqual(gotVals, wantVals) {
			t.Errorf("for key %q: got %v, want %v", k, gotVals, wantVals)
		}
	}
}
