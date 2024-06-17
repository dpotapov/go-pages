package chtml

import (
	"strings"
	"testing"

	"github.com/beevik/etree"
	"github.com/google/go-cmp/cmp"
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

	doc := etree.NewDocument()
	if err := doc.ReadFromString(testDoc); err != nil {
		t.Fatalf("error reading test document: %v", err)
	}

	tests := []struct {
		name string
		t    etree.Token
		want string
	}{
		{
			name: "topElement",
			t:    doc.FindElement("//html"),
			want: `<html>...</html>`,
		},
		{
			name: "textElement",
			t:    doc.FindElement("//title").Child[0],
			want: `<title>Test</title>`,
		},
		{
			name: "manyElements",
			t:    doc.FindElement("//p"),
			want: `
			<body style="color: red;">
				<h1>Lorem ipsum</h1>
				<p class="styled">dolor sit amet</p>
				consectetur adipiscing elit
				<span>sed do eiusmod</span>...</body>`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantDoc := etree.NewDocument()
			if err := wantDoc.ReadFromString(tt.want); err != nil {
				t.Fatalf("error reading want document: %v", err)
			}
			stripWhitespace(&wantDoc.Element)
			want, _ := wantDoc.WriteToString()

			got := buildErrorContext(tt.t)

			var buf strings.Builder
			for _, c := range got.Child {
				c.WriteTo(&buf, &etree.WriteSettings{})
			}

			if diff := cmp.Diff(buf.String(), want); diff != "" {
				t.Errorf("buildErrorContext() diff (-got +want):\n%s", diff)
			}
		})
	}
}

func stripWhitespace(el *etree.Element) {
	for _, c := range el.Child {
		switch t := c.(type) {
		case *etree.Element:
			stripWhitespace(t)
		case *etree.CharData:
			if t.IsWhitespace() {
				t.Data = ""
			}
		}
	}
}
