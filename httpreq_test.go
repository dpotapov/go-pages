package pages

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"sync"
	"testing"

	"pages/chtml"
)

func TestHttpRequestComponent_Execute(t *testing.T) {
	type wantVars struct {
		Code  int
		Body  string
		Json  any
		Error string
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/data", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": "hello"}`))
	})
	comp := &HttpRequestComponent{
		Router: mux,
	}
	tests := []struct {
		name     string
		vars     map[string]any
		wantVars *wantVars
	}{
		{
			name:     "noArgs",
			vars:     nil,
			wantVars: nil,
		},
		{
			name: "noURL",
			vars: map[string]any{
				"var": "p",
			},
			wantVars: &wantVars{
				Code: 301, // by default, the router redirects to the root
				Body: "<a href=\"/\">Moved Permanently</a>.\n\n",
			},
		},
		{
			name: "getData",
			vars: map[string]any{
				"url": "/api/data",
				"var": "p",
			},
			wantVars: &wantVars{
				Code: 200,
				Body: `{"data": "hello"}`,
				Json: map[string]any{
					"data": "hello",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := chtml.NewScopeMap(nil)
			s.SetVars(tt.vars)
			if err := comp.Execute(context.Background(), s); err != nil {
				t.Errorf("Execute() error = %v", err)
				return
			}
			if tt.wantVars != nil {
				if got, ok := s.Vars()["p"].(*httpRequestPoller); ok {
					if got.Code != tt.wantVars.Code {
						t.Errorf("Execute() got.Code = %v, want %v", got.Code, tt.wantVars.Code)
					}
					if got.Body != tt.wantVars.Body {
						t.Errorf("Execute() got.Body = %v, want %v", got.Body, tt.wantVars.Body)
					}
					if !reflect.DeepEqual(got.Json, tt.wantVars.Json) {
						t.Errorf("Execute() got.Json = %v, want %v", got.Json, tt.wantVars.Json)
					}
					if got.Error != tt.wantVars.Error {
						t.Errorf("Execute() got.Error = %v, want %v", got.Error, tt.wantVars.Error)
					}
				} else {
					t.Errorf("Execute() got = nil, want %v", tt.wantVars)
				}
			}
		})
	}
}

func TestHttpRequestComponent_WithInterval(t *testing.T) {
	var wg sync.WaitGroup
	testData := []string{"monday", "tuesday", "wednesday"}
	wg.Add(1)

	s := newScope(map[string]any{
		"url":      "/api/data",
		"var":      "p",
		"interval": "1s",
	})
	defer s.close()

	s.setOnChangeCallback(func() {
		p := s.Vars()["p"].(*httpRequestPoller)
		t.Logf("poller updated: %v", p.Body)
		if len(testData) == 0 {
			p.polling = false
			wg.Done()
		}
	})

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

	comp := &HttpRequestComponent{
		Router: mux,
	}

	if err := comp.Execute(context.Background(), s); err != nil {
		t.Errorf("Execute() error = %v", err)
		return
	}

	// wait for the poller to update 3 times
	wg.Wait()
}
