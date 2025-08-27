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

	// RenderShape holds information about the expected output shape of this node
	// This is used to optimize component composition and validation
	RenderShape *Shape

	// Symbols contains the final input symbol shapes collected during parsing (snake_case handled at read time).
	Symbols map[string]*Shape
}

type Attribute struct {
	Namespace string
	Key       string
	Val       Expr
}

const (
	importNode html.NodeType = 100
	cNode      html.NodeType = 101
)

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
