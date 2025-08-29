package chtml

import (
	"strings"
	"testing"
	
	"golang.org/x/net/html"
)

func TestSpanTracking(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantSpans []struct {
			nodeData string
			line     int
			column   int
			length   int
		}
	}{
		{
			name: "simple element",
			input: `<div>Hello</div>`,
			wantSpans: []struct {
				nodeData string
				line     int
				column   int
				length   int
			}{
				{"div", 1, 1, 5},   // <div> tag
				{"Hello", 1, 6, 5}, // Hello text
			},
		},
		{
			name: "multiline element",
			input: `<div>
  <span>Text</span>
</div>`,
			wantSpans: []struct {
				nodeData string
				line     int
				column   int
				length   int
			}{
				{"div", 1, 1, 5},     // <div> tag
				{"\n  ", 1, 6, 3},    // whitespace text node
				{"span", 2, 3, 6},    // <span> tag
				{"Text", 2, 9, 4},    // Text
				{"\n", 2, 20, 1},     // newline after </span>
			},
		},
		{
			name: "element with attributes",
			input: `<div id="test" class="foo">Content</div>`,
			wantSpans: []struct {
				nodeData string
				line     int
				column   int
				length   int
			}{
				{"div", 1, 1, 27},      // <div id="test" class="foo"> tag
				{"Content", 1, 28, 7},  // Content
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := ParseWithSource("test.chtml", strings.NewReader(tt.input), nil)
			if err != nil {
				t.Fatalf("ParseWithSource failed: %v", err)
			}

			// Collect all nodes with their spans
			var nodes []*Node
			var collectNodes func(*Node)
			collectNodes = func(n *Node) {
				if n.Type == html.ElementNode || n.Type == html.TextNode {
					nodes = append(nodes, n)
				}
				for child := n.FirstChild; child != nil; child = child.NextSibling {
					collectNodes(child)
				}
			}
			collectNodes(doc)

			// Check that we have the expected number of spans
			if len(nodes) != len(tt.wantSpans) {
				t.Errorf("got %d nodes, want %d", len(nodes), len(tt.wantSpans))
				for i, n := range nodes {
					t.Logf("  node %d: %q at %d:%d len=%d", 
						i, n.Data.RawString(), n.Source.Span.Line, n.Source.Span.Column, n.Source.Span.Length)
				}
				return
			}

			// Check each span
			for i, n := range nodes {
				want := tt.wantSpans[i]
				got := n.Source.Span
				
				if n.Data.RawString() != want.nodeData {
					t.Errorf("node %d: got data %q, want %q", i, n.Data.RawString(), want.nodeData)
				}
				if got.Line != want.line {
					t.Errorf("node %d (%q): got line %d, want %d", i, want.nodeData, got.Line, want.line)
				}
				if got.Column != want.column {
					t.Errorf("node %d (%q): got column %d, want %d", i, want.nodeData, got.Column, want.column)
				}
				if got.Length != want.length {
					t.Errorf("node %d (%q): got length %d, want %d", i, want.nodeData, got.Length, want.length)
				}
			}
		})
	}
}

func TestAttributeSpanTracking(t *testing.T) {
	input := `<div id="myid" class="myclass" data-value="42">Content</div>`
	
	doc, err := ParseWithSource("test.chtml", strings.NewReader(input), nil)
	if err != nil {
		t.Fatalf("ParseWithSource failed: %v", err)
	}

	// Find the div element
	var divNode *Node
	for child := doc.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && child.Data.RawString() == "div" {
			divNode = child
			break
		}
	}

	if divNode == nil {
		t.Fatal("div element not found")
	}

	// Check attribute spans
	expectedAttrs := []struct {
		key    string
		val    string
		column int
		length int
	}{
		{"id", "myid", 10, 4},
		{"class", "myclass", 23, 7},
		{"data-value", "42", 44, 2},
	}

	if len(divNode.Attr) != len(expectedAttrs) {
		t.Fatalf("got %d attributes, want %d", len(divNode.Attr), len(expectedAttrs))
	}

	for i, attr := range divNode.Attr {
		exp := expectedAttrs[i]
		if attr.Key != exp.key {
			t.Errorf("attr %d: got key %q, want %q", i, attr.Key, exp.key)
		}
		if attr.Val.RawString() != exp.val {
			t.Errorf("attr %d: got val %q, want %q", i, attr.Val.RawString(), exp.val)
		}
		if attr.Source.Span.Column != exp.column {
			t.Errorf("attr %d (%s): got column %d, want %d", i, exp.key, attr.Source.Span.Column, exp.column)
		}
		if attr.Source.Span.Length != exp.length {
			t.Errorf("attr %d (%s): got length %d, want %d", i, exp.key, attr.Source.Span.Length, exp.length)
		}
	}
}

func TestErrorSpanTracking(t *testing.T) {
	input := `<div>
  <span c:if="undefined_var">Error here</span>
</div>`
	
	doc, err := ParseWithSource("test.chtml", strings.NewReader(input), nil)
	// Parser should succeed but store errors
	if err == nil {
		t.Log("Parse succeeded without error as expected")
	}

	// Find the span element to check its span was captured
	var spanNode *Node
	var findSpan func(*Node)
	findSpan = func(n *Node) {
		if n.Type == html.ElementNode && n.Data.RawString() == "span" {
			spanNode = n
			return
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			findSpan(child)
		}
	}
	findSpan(doc)

	if spanNode != nil {
		// Check that span information was captured
		if spanNode.Source.File != "test.chtml" {
			t.Errorf("got file %q, want %q", spanNode.Source.File, "test.chtml")
		}
		if spanNode.Source.Span.Line != 2 {
			t.Errorf("got line %d, want 2", spanNode.Source.Span.Line)
		}
		if spanNode.Source.Span.Column != 3 {
			t.Errorf("got column %d, want 3", spanNode.Source.Span.Column)
		}
	} else {
		t.Error("span element not found")
	}
}