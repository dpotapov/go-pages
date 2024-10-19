package chtml

import (
	"fmt"
	"iter"
	"reflect"

	"golang.org/x/net/html"
)

// render evaluates expressions in the CHTML node tree and returns either a new *html.Node tree or
// a data object.
// The algorithm of rendering is as follows:
//  1. Evaluate the conditional expression (c:if, c:else-if, c:else) for the given node and mark
//     remaining nodes in the same chain as hidden if the condition is true.
//  2. Evaluate the loop expression (c:for) for the given node and update the environment with the
//     loop variables (this is implemented by the spawning of new components within evalFor method).
//  3. Render the node and its children, calling the appropriate function based on a node type, and
//     appending the result to the destination node.
func (c *chtmlComponent) render(n *Node) any {
	if c.evalIf(n) {
		var res, rr any

		for c := range c.evalFor(n) {
			switch n.Type {
			case html.ElementNode:
				rr = c.renderElement(n)
			case html.TextNode:
				rr = c.renderText(n)
			case html.CommentNode:
				rr = c.renderComment(n)
			case html.DocumentNode:
				rr = c.renderDocument(n)
			case importNode:
				rr = c.renderImport(n)
			default:
				c.error(n, fmt.Errorf("unexpected node type: %v", n.Type))
			}

			res = AnyPlusAny(res, rr)
		}

		return res
	}

	return nil
}

// renderText evaluates the interpolated expression for the given text node and stores the result in
// the destination node.
// If the text node is not an expression, the value is copied as is.
func (c *chtmlComponent) renderText(n *Node) any {
	res, err := n.Data.Value(&c.vm, c.env)
	if err != nil {
		c.error(n, fmt.Errorf("eval text: %w", err))
		return nil
	}
	return res
}

func (c *chtmlComponent) renderComment(n *Node) *html.Node {
	if c.renderComments {
		data, err := n.Data.Value(&c.vm, c.env)
		if err != nil {
			c.error(n, fmt.Errorf("eval comment: %w", err))
			return nil
		}
		return &html.Node{
			Type: html.CommentNode,
			Data: fmt.Sprint(data),
		}
	}
	return nil
}

func (c *chtmlComponent) renderDocument(n *Node) any {
	var res any

	for child := n.FirstChild; child != nil; child = child.NextSibling {
		rr := c.render(child)
		if rr == nil {
			continue
		}
		if attr, ok := rr.(Attribute); ok {
			v, err := attr.Val.Value(&c.vm, env(c.env))
			if err != nil {
				c.error(n, fmt.Errorf("eval attr %q: %w", attr.Key, err))
				continue
			}
			if n == c.doc {
				snake := toSnakeCase(attr.Key)
				if !c.scopeHasVar(snake) {
					c.env[snake] = v
				}
			}
		} else {
			res = AnyPlusAny(res, rr)
		}
	}
	return res
}

func (c *chtmlComponent) renderElement(n *Node) any {
	clone := &html.Node{
		Type:     html.ElementNode,
		DataAtom: n.DataAtom,
		Data:     n.Data.RawString(),
	}

	// eval attributes into values for the cloned node
	if err := c.renderAttrs(clone, n); err != nil {
		c.error(n, fmt.Errorf("eval attributes: %w", err))
		return nil
	}

	for child := n.FirstChild; child != nil; child = child.NextSibling {
		rr := c.render(child)
		if rr == nil {
			continue
		}
		if attr, ok := rr.(Attribute); ok {
			v, err := attr.Val.Value(&c.vm, env(c.env))
			if err != nil {
				c.error(n, fmt.Errorf("eval attr %q: %w", attr.Key, err))
				continue
			}
			clone.Attr = append(clone.Attr, html.Attribute{
				Namespace: attr.Namespace,
				Key:       attr.Key,
				Val:       fmt.Sprintf("%v", v),
			})
		} else {
			if c := AnyToHtml(rr); c != nil {
				clone.AppendChild(cloneHtmlTree(c))
			}
		}
	}

	return clone
}

// renderImport renders the imported component (<c:NAME>) and appends the result to the destination.
func (c *chtmlComponent) renderImport(n *Node) any {
	// Build variables for the imported component
	vars := make(map[string]any)
	for _, attr := range n.Attr {
		res, err := attr.Val.Value(&c.vm, env(c.env))
		if err != nil {
			c.error(n, fmt.Errorf("eval attr %q: %w", attr.Key, err))
			return nil
		}
		snake := toSnakeCase(attr.Key)
		vars[snake] = res
	}

	if n.FirstChild != nil {
		vars["_"] = nil
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			rr := c.render(child)
			if attr, ok := rr.(Attribute); ok {
				v, err := attr.Val.Value(&c.vm, env(c.env))
				if err != nil {
					c.error(n, fmt.Errorf("eval attr %q: %w", attr.Key, err))
					return nil
				}
				vars[attr.Key] = v
			} else {
				vars["_"] = AnyPlusAny(vars["_"], rr)
			}
		}
	}

	// Create a new Scope for the imported component
	s := c.scope.Spawn(vars)

	// try to use an existing instance of the component:
	var comp Component
	if len(c.children[n]) == 1 {
		comp = c.children[n][0]
	} else {
		impName, err := n.Data.Value(&c.vm, env(c.env))
		if err != nil {
			c.error(n, fmt.Errorf("eval import name: %w", err))
			return nil
		}
		impNameStr, ok := impName.(string)
		if !ok {
			c.error(n, fmt.Errorf("import name must be a string"))
			return nil
		}
		imp := c.importer
		if impNameStr == "c:attr" {
			imp = &builtinImporter{}
		}
		if imp == nil {
			c.error(n, ErrImportNotAllowed)
			return nil
		}
		comp, err = imp.Import(impNameStr[2:])
		if err != nil {
			c.error(n, fmt.Errorf("import %q: %w", impNameStr, err))
			return nil
		}
		c.children[n] = append(c.children[n], comp)
	}

	rr, err := comp.Render(s)
	if err != nil {
		c.error(n, fmt.Errorf("render import: %w", err))
		return nil
	}
	return rr
}

// renderAttrs loops over the attributes of the source node and evaluates the expressions for them.
// If the attribute has no associated expression, it is copied as is.
// If the given element is an import, skip the evaluation and return immediately.
func (c *chtmlComponent) renderAttrs(dst *html.Node, n *Node) error {
	attrs := make([]html.Attribute, 0, len(n.Attr))
	for _, attr := range n.Attr {
		v, err := attr.Val.Value(&c.vm, c.env)
		if err != nil {
			c.error(n, fmt.Errorf("eval attr %q: %w", attr.Key, err))
			continue
		}

		if _, ok := v.(*Node); ok {
			continue // skip HTML nodes
		}

		sv := fmt.Sprint(v)
		if sv == "<nil>" {
			sv = ""
		}

		attrs = append(attrs, html.Attribute{Key: attr.Key, Val: sv})
	}
	dst.Attr = nil
	if len(attrs) > 0 {
		dst.Attr = attrs
	}
	return nil
}

// evalIf evaluates the conditional expression (c:if, c:else-if, c:else) for the given node and
// marks it as hidden if the condition is false.
// Returns true if the node should be rendered, false otherwise.
func (c *chtmlComponent) evalIf(n *Node) bool {
	if n.Cond.IsEmpty() {
		return true // no condition, render by default
	}

	if _, ok := c.hidden[n]; ok {
		delete(c.hidden, n) // reset hidden state for the next rendering cycle
		return false
	}

	render := true

	res, err := n.Cond.Value(&c.vm, c.env)
	if err != nil {
		c.error(n, fmt.Errorf("eval c:if: %w", err))
		render = false
	} else {
		switch v := res.(type) {
		case bool:
			render = v
		case string:
			render = v != ""
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			render = v != 0
		case float32, float64:
			render = v != 0.0
		case nil:
			render = false
		default:
			// check if the value is a non-empty slice
			rv := reflect.ValueOf(res)
			if rv.Kind() == reflect.Slice && rv.Len() == 0 {
				render = false
			}
			// check if the value is a non-empty map
			if rv.Kind() == reflect.Map && rv.Len() == 0 {
				render = false
			}
		}
	}

	if render {
		// mark next conditional as not rendered
		for next := n.NextCond; next != nil; next = next.NextCond {
			c.hidden[next] = struct{}{}
			c.closeChildren(next, 0)
		}
	} else {
		c.closeChildren(n, 0)
	}
	return render
}

// evalFor evaluates the loop expression (c:for) for the given node and updates the environment
// with the loop variables.
// If no loop expression is present, the function return true only once (assuming that the node
// should be rendered by default).
func (c *chtmlComponent) evalFor(n *Node) iter.Seq[*chtmlComponent] {
	if n.Loop.IsEmpty() {
		return func(yield func(*chtmlComponent) bool) {
			yield(c)
		}
	}

	res, err := n.Loop.Value(&c.vm, c.env)
	if err != nil {
		c.error(n, fmt.Errorf("eval c:for: %w", err))
		c.closeChildren(n, 0)
		return func(yield func(*chtmlComponent) bool) {}
	}
	v := reflect.ValueOf(res)
	// TODO: add support for maps, structs, arrays
	if v.Kind() != reflect.Slice {
		c.error(n, fmt.Errorf("c:for expression must return slice"))
		c.closeChildren(n, 0)
		return func(yield func(*chtmlComponent) bool) {}
	}

	return func(yield func(*chtmlComponent) bool) {
		defer func() {
			c.closeChildren(n, v.Len()) // close remaining children
		}()

		for i := 0; i < v.Len(); i++ {
			el := v.Index(i)

			// make a copy of the current environment with the loop variable
			loopEnv := make(map[string]any)
			for k, v := range c.env {
				loopEnv[k] = v
			}
			loopEnv[n.LoopVar] = el.Interface()

			if n.LoopIdx != "" {
				loopEnv[n.LoopIdx] = i
			}

			var loopComp *chtmlComponent
			if i < len(c.children[n]) {
				if c, ok := c.children[n][i].(*chtmlComponent); ok {
					loopComp = c
				} else {
					c.error(n, fmt.Errorf("unexpected node type: %T", c.children[n][i]))
					continue
				}
			} else {
				loopComp = &chtmlComponent{
					doc:            n,
					scope:          c.scope,
					env:            loopEnv,
					importer:       c.importer,
					renderComments: true,
					hidden:         c.hidden,
					children:       make(map[*Node][]Component),
					errs:           nil,
				}
				c.children[n] = append(c.children[n], loopComp)
			}

			yield(loopComp)
		}
	}
}

func (c *chtmlComponent) scopeHasVar(v string) bool {
	_, ok := c.scope.Vars()[v]
	return ok
}
