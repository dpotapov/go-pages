package pages

import (
	"encoding/json"
	"net/http"
	"reflect"
	"sync"
	"testing"

	"github.com/dpotapov/go-pages/chtml"
)

func TestHttpCallComponent_Render(t *testing.T) {
	type wantVars struct {
		Code  int
		Body  string
		Json  any
		Error string
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/data", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data": "hello"}`))
	})
	tests := []struct {
		name     string
		vars     map[string]any
		wantVars *wantVars
	}{
		{
			name:     "noArgs",
			vars:     map[string]any{},
			wantVars: nil,
		},
		{
			name:     "noURL",
			vars:     map[string]any{},
			wantVars: &wantVars{},
		},
		{
			name: "getData",
			vars: map[string]any{
				"url": "/api/data",
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
			s := chtml.NewBaseScope(tt.vars)

			comp := NewHttpCallComponent(mux)
			defer func() { _ = comp.Dispose() }()

			rr, err := comp.Render(s)
			if err != nil {
				t.Errorf("Render() error = %v", err)
				return
			}
			if tt.wantVars != nil {
				if got, ok := rr.(*HttpCallResponse); ok {
					if got.Code != tt.wantVars.Code {
						t.Errorf("Render() got.Code = %v, want %v", got.Code, tt.wantVars.Code)
					}
					if got.Body != tt.wantVars.Body {
						t.Errorf("Render() got.Body = %v, want %v", got.Body, tt.wantVars.Body)
					}
					if !reflect.DeepEqual(got.Json, tt.wantVars.Json) {
						t.Errorf("Render() got.Json = %v, want %v", got.Json, tt.wantVars.Json)
					}
					if got.Error != tt.wantVars.Error {
						t.Errorf("Render() got.Error = %v, want %v", got.Error, tt.wantVars.Error)
					}
				} else {
					t.Errorf("Render() got = nil, want %v", tt.wantVars)
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
	}, nil, nil)

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
