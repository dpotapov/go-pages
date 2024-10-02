// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This example demonstrates parsing HTML data and walking the resulting tree.
package chtml

import (
	"fmt"
	"strings"
)

func Example() {
	s := "<html><body><p>Hello World</p></body></html>"
	r := strings.NewReader(s)
	docNode, err := Parse(r, nil)
	if err != nil {
		panic(err)
	}

	fmt.Println(docNode)
}
