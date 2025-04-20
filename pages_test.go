package pages

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestPages_Handler(t *testing.T) {
	tests := []struct {
		url        string
		wantStatus int
		wantBody   string
	}{
		{"GET /", 200, "\n\n<h1>Index</h1>\n\n<div>index-content</div>\n\n"},
		{"GET /asset.css", 200, "body { background: #fff; }\n"},
		{"GET /index.html", 404, "Not Found\n"},
		{"GET /js", 404, "Not Found\n"},
		{"GET /js/", 404, "Not Found\n"},
		{"GET /js/asset.js", 200, "console.log(1)\n"},
		{"GET /posts", 404, "Not Found\n"},
		{"GET /posts/123/", 200, "\n123\n"},
		{"GET /posts/123/edit", 200, "\nedit-post\n"},
		{"GET /posts/123/asset.txt", 200, "post-content\n"},
		{"GET /posts/new", 200, "new-post\n"},
	}
	for i, tt := range tests {
		t.Run(fmt.Sprintf("%d_%s", i, tt.url), func(t *testing.T) {
			urlParts := strings.SplitN(tt.url, " ", 2)
			method, url := urlParts[0], urlParts[1]
			req, err := http.NewRequest(method, url, nil)
			if err != nil {
				t.Fatal(err)
			}

			rr := httptest.NewRecorder()

			h := &Handler{
				FileSystem: os.DirFS("testdata"),
				OnError:    func(r *http.Request, pagesErr error) { err = pagesErr },
			}

			h.ServeHTTP(rr, req)

			if err != nil {
				t.Errorf("Handler() err = %v", err)
			}

			if rr.Code != tt.wantStatus {
				t.Errorf("status code: got %v, want %v", rr.Code, tt.wantStatus)
				return
			}

			if rr.Body.String() != tt.wantBody {
				t.Errorf("body: got %q, want %q", rr.Body.String(), tt.wantBody)
				return
			}
		})
	}
}

func TestPages_Handler_Fragments(t *testing.T) {
	tests := []struct {
		name             string
		url              string
		htmxTarget       string                       // Simulate HX-Target header
		fragmentSelector func(r *http.Request) string // Use the function signature directly
		wantStatus       int
		wantBody         string
	}{
		{
			name:       "No fragment requested, full page",
			url:        "GET /fragment-test",
			wantStatus: 200,
			wantBody:   `<body><div id="frag1">Fragment 1</div><hr/><div id="frag2">Fragment 2</div></body>`,
		},
		{
			name: "Custom selector - fragment via query param",
			url:  "GET /fragment-test?frag=frag1",
			fragmentSelector: func(r *http.Request) string {
				return r.URL.Query().Get("frag")
			},
			wantStatus: 200,
			wantBody:   `Fragment 1`,
		},
		{
			name: "Custom selector - different fragment via query param",
			url:  "GET /fragment-test?frag=frag2",
			fragmentSelector: func(r *http.Request) string {
				return r.URL.Query().Get("frag")
			},
			wantStatus: 200,
			wantBody:   `Fragment 2`,
		},
		{
			name: "Custom selector - fragment not found",
			url:  "GET /fragment-test?frag=frag3",
			fragmentSelector: func(r *http.Request) string {
				return r.URL.Query().Get("frag")
			},
			wantStatus: 200, // Handler returns empty body if fragment not found
			wantBody:   ``,
		},
		{
			name:             "HTMX selector - no header, full page",
			url:              "GET /fragment-test",
			fragmentSelector: HTMXFragmentSelector,
			wantStatus:       200,
			wantBody:         `<body><div id="frag1">Fragment 1</div><hr/><div id="frag2">Fragment 2</div></body>`,
		},
		{
			name:             "HTMX selector - with HX-Target header frag1",
			url:              "GET /fragment-test",
			htmxTarget:       "frag1",
			fragmentSelector: HTMXFragmentSelector,
			wantStatus:       200,
			wantBody:         `Fragment 1`,
		},
		{
			name:             "HTMX selector - with HX-Target header frag2",
			url:              "GET /fragment-test",
			htmxTarget:       "frag2",
			fragmentSelector: HTMXFragmentSelector,
			wantStatus:       200,
			wantBody:         `Fragment 2`,
		},
		{
			name:             "HTMX selector - target not found",
			url:              "GET /fragment-test",
			htmxTarget:       "frag3",
			fragmentSelector: HTMXFragmentSelector,
			wantStatus:       200,
			wantBody:         ``,
		},
		{
			name:             "HTMX selector - target is CSS selector (should use ID)",
			url:              "GET /fragment-test",
			htmxTarget:       "#frag1", // HTMX might send selector, we extract ID
			fragmentSelector: HTMXFragmentSelector,
			wantStatus:       200,
			wantBody:         `Fragment 1`,
		},
	}

	// Create a dummy fragment test file in testdata
	filePath := "testdata/fragment-test.chtml"
	content := `<body><div id="frag1">Fragment 1</div><hr/><div id="frag2">Fragment 2</div></body>`
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create test file %s: %v", filePath, err)
	}
	defer os.Remove(filePath) // Clean up the test file

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			urlParts := strings.SplitN(tt.url, " ", 2)
			method, urlPath := urlParts[0], urlParts[1]
			req, err := http.NewRequest(method, urlPath, nil)
			if err != nil {
				t.Fatal(err)
			}

			if tt.htmxTarget != "" {
				req.Header.Set("HX-Target", tt.htmxTarget)
			}

			rr := httptest.NewRecorder()

			h := &Handler{
				FileSystem:       os.DirFS("testdata"),
				FragmentSelector: tt.fragmentSelector,
				OnError: func(r *http.Request, pagesErr error) {
					t.Errorf("Handler() unexpected error for %s: %v", tt.url, pagesErr)
				},
			}

			h.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Errorf("status code: got %v, want %v", rr.Code, tt.wantStatus)
			}

			if body := strings.TrimSpace(rr.Body.String()); body != tt.wantBody {
				t.Errorf("body:\ngot  %q\nwant %q", body, tt.wantBody)
			}
		})
	}
}
