package pages

import (
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"
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
				"cookies": []*http.Cookie{
					{Name: "jwt", Value: "1234567890"},
				},
			},
			wantCode: 201,
			wantHeaders: map[string][]string{
				"Location":   {"/api/data"},
				"Set-Cookie": {"jwt=1234567890"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newScope(tt.vars, nil, nil)

			rr, err := HttpResponseComponent{}.Render(s)
			if err != nil {
				t.Fatal(err)
			}
			if rr != nil {
				t.Fatal("render result is not nil")
			}
			if s.globals.statusCode != tt.wantCode {
				t.Errorf("StatusCode = %v, want %v", s.globals.statusCode, tt.wantCode)
			}
			if len(s.globals.header) != len(tt.wantHeaders) {
				t.Errorf("Header = %v, want %v", s.globals.header, tt.wantHeaders)
			}
			for k, v := range tt.wantHeaders {
				if s.globals.header.Get(k) != v[0] {
					t.Errorf("Header[%s] = %v, want %v", k, s.globals.header.Get(k), v[0])
				}
			}
		})
	}
}

func TestCookieComponent(t *testing.T) {
	tests := []struct {
		name       string
		vars       map[string]any
		wantCookie *http.Cookie
	}{
		{
			name:       "noArgs",
			vars:       nil,
			wantCookie: &http.Cookie{},
		},
		{
			name: "withArgs",
			vars: map[string]any{
				"name":   "jwt",
				"value":  "1234567890",
				"secure": "ttt",
			},
			wantCookie: &http.Cookie{
				Name:   "jwt",
				Value:  "1234567890",
				Secure: true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newScope(tt.vars, nil, nil)

			rr, err := CookieComponent{}.Render(s)
			if err != nil {
				t.Fatal(err)
			}
			if c, ok := rr.(*http.Cookie); ok {
				if diff := cmp.Diff(c, tt.wantCookie); diff != "" {
					t.Errorf("Cookie diff (-got +want):\n%s", diff)
				}
			} else {
				t.Errorf("Cookie = %v, want %v", rr, tt.wantCookie)
			}
		})
	}
}
