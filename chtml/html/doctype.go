// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package html

import (
	"strings"

	"golang.org/x/net/html"
)

// parseDoctype parses the data from a DoctypeToken into a name,
// public identifier, and system identifier. It returns a Node whose Type
// is DoctypeNode, whose Data is the name, and which has attributes
// named "system" and "public" for the two identifiers if they were present.
func parseDoctype(s string) (n *html.Node) {
	n = &html.Node{Type: html.DoctypeNode}

	// Find the name.
	space := strings.IndexAny(s, whitespace)
	if space == -1 {
		space = len(s)
	}
	n.Data = strings.ToLower(s[:space])
	s = strings.TrimLeft(s[space:], whitespace)

	if len(s) < 6 {
		// It can't start with "PUBLIC" or "SYSTEM".
		// Ignore the rest of the string.
		return n
	}

	key := strings.ToLower(s[:6])
	s = s[6:]
	for key == "public" || key == "system" {
		s = strings.TrimLeft(s, whitespace)
		if s == "" {
			break
		}
		quote := s[0]
		if quote != '"' && quote != '\'' {
			break
		}
		s = s[1:]
		q := strings.IndexRune(s, rune(quote))
		var id string
		if q == -1 {
			id = s
			s = ""
		} else {
			id = s[:q]
			s = s[q+1:]
		}
		n.Attr = append(n.Attr, html.Attribute{Key: key, Val: id})
		if key == "public" {
			key = "system"
		} else {
			key = ""
		}
	}

	return n
}
