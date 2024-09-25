package chtml

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/html"
)

func Test_buildErrorContext(t *testing.T) {
	testDoc := `
		<html>
			<head>
				<title>Test</title>	
			</head>
			<body style="color: red;">
				<h1>Lorem ipsum</h1>
				<p class="styled">dolor sit amet</p>
				consectetur adipiscing elit
				<span>sed do eiusmod</span>
				tempor incididunt ut labore et dolore magna aliqua
			</body>
		</html>
		`

	var findElement func(n *Node, name string) *Node
	findElement = func(n *Node, name string) *Node {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && c.Data.RawString() == name {
				return c
			}
			if found := findElement(c, name); found != nil {
				return found
			}
		}
		return nil
	}

	doc, err := Parse(strings.NewReader(testDoc), nil)
	require.NoError(t, err)

	tests := []struct {
		name string
		t    *Node
		want string
	}{
		{
			name: "topElement",
			t:    findElement(doc, "html"),
			want: "<html>\n\t\t\t...\n\t\t\t</html>",
		},
		{
			name: "textElement",
			t:    findElement(doc, "title").FirstChild,
			want: `<title>Test</title>`,
		},
		{
			name: "manyElements",
			t:    findElement(doc, "p"),
			want: `<body style="color: red;">
				<h1>Lorem ipsum</h1>
				<p class="styled">dolor sit amet</p>
				consectetur adipiscing elit
				<span>sed do eiusmod</span>...</body>`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildErrorContext(tt.t)
			var buf strings.Builder
			_ = html.Render(&buf, got)

			if diff := cmp.Diff(buf.String(), tt.want); diff != "" {
				t.Errorf("buildErrorContext() diff (-got +want):\n%s", diff)
			}
		})
	}
}
