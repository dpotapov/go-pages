package pages

import (
	"testing"

	"github.com/dpotapov/go-pages/chtml"
)

func TestHttpResponseComponent_Render(t *testing.T) {
	tests := []struct {
		name        string
		vars        map[string]any
		wantCode    int
		wantHeaders map[string][]string
	}{
		{
			name:        "noArgs",
			vars:        nil,
			wantCode:    0,
			wantHeaders: nil,
		},
		{
			name: "withArgs",
			vars: map[string]any{
				"status":   "201",
				"location": "/api/data",
			},
			wantCode: 201,
			wantHeaders: map[string][]string{
				"Location": {"/api/data"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := chtml.NewScopeMap(nil)
			s.SetVars(tt.vars)

			r := &HttpResponseComponent{}
			rr, err := r.Render(s)
			if err != nil {
				t.Fatal(err)
			}
			if rr == nil {
				t.Fatal("RenderResult is nil")
			}
			if rr.StatusCode != tt.wantCode {
				t.Errorf("StatusCode = %v, want %v", rr.StatusCode, tt.wantCode)
			}
			if len(rr.Header) != len(tt.wantHeaders) {
				t.Errorf("Header = %v, want %v", rr.Header, tt.wantHeaders)
			}
			for k, v := range tt.wantHeaders {
				if rr.Header.Get(k) != v[0] {
					t.Errorf("Header[%s] = %v, want %v", k, rr.Header.Get(k), v[0])
				}
			}
		})
	}
}
