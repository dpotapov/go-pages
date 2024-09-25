// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// Modifications:
// Copyright 2024 Daniel Potapov
//  - New Node struct with additional fields for parsed expressions.

package chtml

import (
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

type Node struct {
	// The following fields are replicated from golang.org/x/net/html.Node.
	Parent, FirstChild, LastChild, PrevSibling, NextSibling *Node

	Type      html.NodeType
	DataAtom  atom.Atom
	Data      Expr
	Namespace string

	// Attr is the list of attributes for the node. Also includes c:attr elements.
	Attr []Attribute

	// Cond is the value of c:if attribute. The c:if attribute itself is not included in Attr.
	Cond Expr

	// PrevCond is the previous c:else-if, or c:if node in the condition chain. It is not being
	// used during the rendering process (only NextCond), but is useful for the testing.
	// NextCond is the next c:else-if, or c:else node in the condition chain.
	PrevCond, NextCond *Node

	// Loop is the value of c:for attribute. The c:for attribute itself is not included in Attr.
	Loop Expr

	// LoopIdx is the index variable name for c:for loops.
	LoopIdx string

	// LoopVar is the value variable name for c:for loops.
	LoopVar string
}

type Attribute struct {
	Namespace string
	Key       string
	Val       Expr
}

const (
	scopeMarkerNode html.NodeType = 7
	importNode      html.NodeType = 100
)

// Section 12.2.4.3 says "The markers are inserted when entering applet,
// object, marquee, template, td, th, and caption elements, and are used
// to prevent formatting from "leaking" into applet, object, marquee,
// template, td, th, and caption elements".
var scopeMarker = Node{Type: scopeMarkerNode}

func (n *Node) IsWhitespace() bool {
	return strings.TrimLeft(n.Data.RawString(), whitespace) == ""
}

// InsertBefore inserts newChild as a child of n, immediately before oldChild
// in the sequence of n'scope children. oldChild may be nil, in which case newChild
// is appended to the end of n'scope children.
//
// It will panic if newChild already has a parent or siblings.
func (n *Node) InsertBefore(newChild, oldChild *Node) {
	if newChild.Parent != nil || newChild.PrevSibling != nil || newChild.NextSibling != nil {
		panic("html: InsertBefore called for an attached child Node")
	}
	var prev, next *Node
	if oldChild != nil {
		prev, next = oldChild.PrevSibling, oldChild
	} else {
		prev = n.LastChild
	}
	if prev != nil {
		prev.NextSibling = newChild
	} else {
		n.FirstChild = newChild
	}
	if next != nil {
		next.PrevSibling = newChild
	} else {
		n.LastChild = newChild
	}
	newChild.Parent = n
	newChild.PrevSibling = prev
	newChild.NextSibling = next
}

// AppendChild adds a node c as a child of n.
//
// It will panic if c already has a parent or siblings.
func (n *Node) AppendChild(c *Node) {
	if c.Parent != nil || c.PrevSibling != nil || c.NextSibling != nil {
		panic("chtml: AppendChild called for an attached child Node")
	}
	last := n.LastChild
	if last != nil {
		last.NextSibling = c
	} else {
		n.FirstChild = c
	}
	n.LastChild = c
	c.Parent = n
	c.PrevSibling = last
}

// RemoveChild removes a node c that is a child of n. Afterwards, c will have
// no parent and no siblings.
//
// It will panic if c'scope parent is not n.
func (n *Node) RemoveChild(c *Node) {
	if c.Parent != n {
		panic("chtml: RemoveChild called for a non-child Node")
	}
	if n.FirstChild == c {
		n.FirstChild = c.NextSibling
	}
	if c.NextSibling != nil {
		c.NextSibling.PrevSibling = c.PrevSibling
	}
	if n.LastChild == c {
		n.LastChild = c.PrevSibling
	}
	if c.PrevSibling != nil {
		c.PrevSibling.NextSibling = c.NextSibling
	}
	c.Parent = nil
	c.PrevSibling = nil
	c.NextSibling = nil
}

// nodeStack is a stack of nodes.
type nodeStack []*Node

// pop pops the stack. It will panic if scope is empty.
func (s *nodeStack) pop() *Node {
	i := len(*s)
	n := (*s)[i-1]
	*s = (*s)[:i-1]
	return n
}

// top returns the most recently pushed node, or nil if scope is empty.
func (s *nodeStack) top() *Node {
	if i := len(*s); i > 0 {
		return (*s)[i-1]
	}
	return nil
}

// index returns the index of the top-most occurrence of n in the stack, or -1
// if n is not present.
func (s *nodeStack) index(n *Node) int {
	for i := len(*s) - 1; i >= 0; i-- {
		if (*s)[i] == n {
			return i
		}
	}
	return -1
}

// remove removes a node from the stack. It is a no-op if n is not present.
func (s *nodeStack) remove(n *Node) {
	i := s.index(n)
	if i == -1 {
		return
	}
	copy((*s)[i:], (*s)[i+1:])
	j := len(*s) - 1
	(*s)[j] = nil
	*s = (*s)[:j]
}

// cloneNode returns a new node with the same type, data and attributes.
// The clone has no parent, no siblings and no children.
// PrevCond and NextCond are not copied.
func cloneNode(n *Node) *Node {
	m := &Node{
		Type:     n.Type,
		DataAtom: n.DataAtom,
		Data:     n.Data,
		Attr:     make([]Attribute, len(n.Attr)),

		Cond:     n.Cond,
		PrevCond: nil,
		NextCond: nil,
		Loop:     n.Loop,
		LoopVar:  n.LoopVar,
		LoopIdx:  n.LoopIdx,
	}
	copy(m.Attr, n.Attr)
	return m
}
