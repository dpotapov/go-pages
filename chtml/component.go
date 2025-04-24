package chtml

import (
	"errors"
	"fmt"

	"github.com/expr-lang/expr/vm"
)

type Component interface {
	// Render transforms the input data from the scope into another data object, typically
	// an HTML document (*html.Node) or anything else that can be sent over the wire or
	// passed to another Component as an input.
	Render(s Scope) (any, error)
}

// Disposable is an optional interface for components that require explicit resource cleanup.
// Components that allocate resources such as files, network connections, or memory buffers
// should implement this interface to release those resources when they are no longer needed.
type Disposable interface {
	// Dispose releases any resources held by the component.
	// It should be called when the component is no longer needed to prevent resource leaks.
	// If an error occurs during disposal, it should be returned.
	Dispose() error
}

type ComponentOptions struct {
	// Importer is the factory for components. It is invoked when a <c:NAME> element is encountered.
	Importer Importer

	// RenderComments is a flag to enable rendering of comments
	RenderComments bool
}

// chtmlComponent is an instance of a CHTML component, ready to be rendered.
// The Render method evaluates expressions in the CHTML document and returns either a new *html.Node
// tree with HTML content or a data object if the result of the evaluation is not HTML.
type chtmlComponent struct {
	// doc is a root node of the parsed CHTML document
	doc *Node

	// scope is a reference to the current Scope of the rendering component.
	// It is used to spawn new contexts for imports.
	scope Scope

	// env holds variables for the expression engine.
	// When evaluating loops, the env map is updated with the loop variables.
	env map[string]any

	// renderComments is a flag to enable rendering of comments
	renderComments bool

	// importer is the factory for components. It is invoked when a <c:NAME> element is encountered.
	importer Importer

	// hidden stores pointers to nodes that should not be rendered. This map is populated when
	// evaluating c:if directives.
	hidden map[*Node]struct{}

	// children stores disposable components created for imports and loops.
	// A Node can have multiple children in case of c:for loops.
	children map[*Node][]Component

	// errs stores errors that occurred during rendering.
	errs []error

	// vm is the expression engine used to evaluate expressions in the CHTML nodes.
	vm vm.VM
}

var _ Component = (*chtmlComponent)(nil)
var _ Disposable = (*chtmlComponent)(nil)

// Render evaluates expressions in the CHTML document and returns either a new *html.Node tree with
// HTML content or a data object if the result of the evaluation is not HTML.
func (c *chtmlComponent) Render(s Scope) (any, error) {
	c.scope = s

	// Check inputs: scope.Vars() keys should be a subset of c.doc.Attr keys.
	attrMap := make(map[string]any, len(c.doc.Attr))
	for _, attr := range c.doc.Attr {
		snake := toSnakeCase(attr.Key)
		attrMap[snake] = attr.Val // TODO: should we evaluate chtml.Expr here?
	}

	for k := range s.Vars() {
		if k == "_" {
			continue
		}
		if _, ok := attrMap[k]; !ok {
			// c.error(c.el, fmt.Errorf("unrecognized argument %s", k))
			return nil, &UnrecognizedArgumentError{Name: k}
		}
	}

	// Copy default values from c.args into a new map.
	if c.env == nil {
		c.env = map[string]any{"_": nil}
	}
	for _, attr := range c.doc.Attr {
		v, err := attr.Val.Value(&c.vm, env(c.env))
		if err != nil {
			return nil, fmt.Errorf("eval attr %q: %w", attr.Key, err)
		}

		snake := toSnakeCase(attr.Key)

		// Default args could be unrendered nodes, so we need to evaluate them first.
		if n, ok := v.(*Node); ok {
			c.env[snake] = c.render(n)
		} else {
			c.env[snake] = v
		}
	}

	// Load variables from the Scope into vars, performing type conversion if necessary
	if err := UnmarshalScope(s, &c.env); err != nil {
		return nil, err
	}

	// If we're in dry run mode, return shape inference after input validation
	if s.DryRun() {
		return c.inferNodeShape(c.doc), nil
	}

	// Evaluate the component's expressions for actual rendering
	return c.render(c.doc), errors.Join(c.errs...)
}

func (c *chtmlComponent) Dispose() error {
	for n := range c.children {
		c.closeChildren(n, 0)
	}
	return nil
}

// closeChildren closes and removes child components starting from the given index.
// This is used to close components in c:for loops and c:if.
func (c *chtmlComponent) closeChildren(n *Node, idx int) {
	comps, ok := c.children[n]
	if !ok {
		// Node not found or already cleaned up, nothing to do.
		return
	}

	// Dispose components from index idx onwards.
	// This iterates over the elements that are intended to be removed.
	for i := idx; i < len(comps); i++ {
		if d, ok := comps[i].(Disposable); ok {
			if err := d.Dispose(); err != nil {
				c.error(n, fmt.Errorf("dispose child %d: %w", i, err))
			}
		}
	}

	// Determine the final length of the slice.
	var finalLen int
	if idx > len(comps) {
		// This condition is hit if the caller (e.g., evalFor's defer) expected
		// 'idx' children, but the loop producing them terminated early, resulting
		// in only 'len(comps)' children being present. This is handled gracefully
		// by only keeping the existing children.
		// c.error(n, fmt.Errorf("internal warning: closeChildren called with idx %d > current len %d", idx, len(comps)))
		finalLen = len(comps)
	} else {
		// idx <= len(comps), this is the expected case.
		// We want to keep the first 'idx' elements.
		finalLen = idx
	}

	// Update the map with the truncated slice or remove the entry if empty.
	if finalLen == 0 {
		delete(c.children, n)
	} else {
		// Slice operation `comps[:finalLen]` is now safe because finalLen <= len(comps).
		c.children[n] = comps[:finalLen]
	}
}

// error appends a new error to the errs list.
func (c *chtmlComponent) error(n *Node, err error) {
	c.errs = append(c.errs, newComponentError(n, err))
}

func NewComponent(n *Node, opts *ComponentOptions) Component {
	c := &chtmlComponent{
		doc:            n,
		renderComments: true,
		hidden:         make(map[*Node]struct{}),
		children:       make(map[*Node][]Component),
		errs:           nil,
	}
	if opts != nil {
		c.importer = opts.Importer
		c.renderComments = opts.RenderComments
	}
	return c
}

// inferNodeShape walks only the immediate children of a node, and
// combines their RenderShapes to determine the final output shape.
// This method is used in dry run mode to optimize component composition.
func (c *chtmlComponent) inferNodeShape(n *Node) any {
	if n == nil {
		return nil
	}

	// If the node has a direct RenderShape assigned, return it
	if n.RenderShape != nil {
		return n.RenderShape
	}

	var shape any
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		shape = AnyPlusAny(shape, child.RenderShape)
	}
	return shape
}
