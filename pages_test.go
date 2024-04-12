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
		{"GET /", 200, "<h1>Index</h1><div>index-content</div>"},
		{"GET /asset.css", 200, "body { background: #fff; }\n"},
		{"GET /index.html", 404, "Not Found\n"},
		{"GET /js", 404, "Not Found\n"},
		{"GET /js/", 404, "Not Found\n"},
		{"GET /js/asset.js", 200, "console.log(1)\n"},
		{"GET /posts", 404, "Not Found\n"},
		{"GET /posts/123/", 200, "view-post"},
		{"GET /posts/123/edit", 200, "edit-post"},
		{"GET /posts/123/asset.txt", 200, "post-content\n"},
		{"GET /posts/new", 200, "new-post"},
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
