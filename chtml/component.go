package chtml

import (
	"errors"
	"fmt"
	"io"

	"github.com/expr-lang/expr/vm"
)

type Component interface {
	// Render transforms the input data from the scope into another data object, typically
	// an HTML document (*html.Node) or anything else that can be sent over the wire or
	// passed to another Component as an input.
	Render(s Scope) (any, error)
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
var _ io.Closer = (*chtmlComponent)(nil)

// Render evaluates expressions in the CHTML document and returns either a new *html.Node tree with
// HTML content or a data object if the result of the evaluation is not HTML.
func (c *chtmlComponent) Render(s Scope) (any, error) {
	c.scope = s

	// Check inputs: scope.Vars() keys should be a subset of c.doc.Attr keys.
	attrMap := make(map[string]any, len(c.doc.Attr))
	for _, attr := range c.doc.Attr {
		attrMap[attr.Key] = attr.Val // TODO: should we evaluate chtml.Expr here?
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
	c.env = make(map[string]any)
	for _, attr := range c.doc.Attr {
		v, err := attr.Val.Value(&c.vm, env(c.env))
		if err != nil {
			return nil, fmt.Errorf("eval attr %q: %w", attr.Key, err)
		}

		// Default args could be unrendered nodes, so we need to evaluate them first.
		if n, ok := v.(*Node); ok {
			c.env[attr.Key] = c.render(n)
		} else {
			c.env[attr.Key] = v
		}
	}

	// Load variables from the Scope into vars, performing type conversion if necessary
	if err := UnmarshalScope(s, &c.env); err != nil {
		return nil, err
	}

	// Evaluate the component'scope expressions
	return c.render(c.doc), errors.Join(c.errs...)
}

func (c *chtmlComponent) Close() error {
	for n := range c.children {
		c.closeChildren(n, 0)
	}
	return nil
}

// closeChildren closes all child components starting from the given index.
// This is used to close components in c:for loops and c:if.
func (c *chtmlComponent) closeChildren(n *Node, idx int) {
	if comps, ok := c.children[n]; ok {
		for i := idx; i < len(comps); i++ {
			if d, ok := comps[i].(io.Closer); ok {
				if err := d.Close(); err != nil {
					c.error(n, fmt.Errorf("close child %d: %w", i, err))
				}
			}
		}
		c.children[n] = comps[:idx]
	}
	if idx == 0 {
		delete(c.children, n)
	}
}

// error appends a new error to the errs list.
func (c *chtmlComponent) error(n *Node, err error) {
	c.errs = append(c.errs, newComponentError("", n, err))
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
