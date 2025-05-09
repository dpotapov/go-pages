package chtml

import (
	"fmt"
	"iter"
	"reflect"
	"sort"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
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
	var res, rr any // res is the accumulated result, rr is the result of the current node/operation

	if c.evalIf(n) {
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
	}

	// Apply RenderShape conversion if specified for the node
	if n.RenderShape != nil {
		convertedRes, convErr := convertToRenderShape(res, n.RenderShape)
		if convErr != nil {
			c.error(n, fmt.Errorf("convert to render shape: %w", convErr))
		} else {
			res = convertedRes
		}
	}
	return res
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
			v, err := attr.Val.Value(&c.vm, c.env)
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

	var res any

	for child := n.FirstChild; child != nil; child = child.NextSibling {
		rr := c.render(child)
		if rr == nil {
			continue
		}
		if attr, ok := rr.(Attribute); ok {
			v, err := attr.Val.Value(&c.vm, c.env)
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
			res = AnyPlusAny(res, rr)
		}
	}

	// append the result to the clone
	if c := AnyToHtml(res); c != nil {
		if c.Type == html.DocumentNode {
			// iterate over c children and move them to the clone
			for child := c.FirstChild; child != nil; child = child.NextSibling {
				clone.AppendChild(cloneHtmlTree(child)) // TODO: avoid cloning
			}
		} else {
			clone.AppendChild(cloneHtmlTree(c)) // TODO: avoid cloning
		}
	}

	return clone
}

// renderImport renders the imported component (<c:NAME>) and appends the result to the destination.
func (c *chtmlComponent) renderImport(n *Node) any {
	// Build variables for the imported component
	vars := make(map[string]any)
	for _, attr := range n.Attr {
		res, err := attr.Val.Value(&c.vm, c.env)
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
				v, err := attr.Val.Value(&c.vm, c.env)
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
		impName, err := n.Data.Value(&c.vm, c.env)
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

		// save component for reuse:
		c.children[n] = append(c.children[n], comp)
	}

	rr, err := comp.Render(s)
	if err != nil {
		c.error(n, fmt.Errorf("render import %s: %w", n.Data.RawString(), err))
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

		// special case for inputs:
		if dst.Type == html.ElementNode && dst.DataAtom == atom.Input {
			// don't add the checked attribute if it's false or 0
			if attr.Key == "checked" && (v == false || v == 0) {
				continue
			}
		}
		if dst.Type == html.ElementNode && dst.DataAtom == atom.Option {
			// don't add the selected attribute if it's false or 0
			if attr.Key == "selected" && (v == false || v == 0) {
				continue
			}
		}
		if dst.Type == html.ElementNode {
			switch dst.DataAtom {
			case atom.Button, atom.Fieldset, atom.Optgroup, atom.Option, atom.Select, atom.Textarea, atom.Input:
				// don't add the disabled attribute if it's false or 0
				if attr.Key == "disabled" && (v == false || v == 0) {
					continue
				}
			}
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

	if res == nil || !v.IsValid() {
		c.closeChildren(n, 0)
		return func(yield func(*chtmlComponent) bool) {}
	}

	switch v.Kind() {
	case reflect.Slice, reflect.Array:
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
					if childComp, ok := c.children[n][i].(*chtmlComponent); ok {
						loopComp = childComp
						loopComp.env = loopEnv
					} else {
						c.error(n, fmt.Errorf("unexpected node type: %T", c.children[n][i]))
						continue
					}
				} else {
					loopComp = c.newChildComponent(n, loopEnv)
					c.children[n] = append(c.children[n], loopComp)
				}

				if !yield(loopComp) {
					break
				}
			}
		}
	case reflect.Map:
		return func(yield func(*chtmlComponent) bool) {
			mapKeys := v.MapKeys()
			defer func() {
				c.closeChildren(n, len(mapKeys)) // close remaining children
			}()

			// Note: Map iteration order is not guaranteed.
			// For deterministic output in tests, sort the keys:
			sort.Slice(mapKeys, func(i, j int) bool {
				return mapKeys[i].String() < mapKeys[j].String()
			})
			for i, key := range mapKeys {
				val := v.MapIndex(key)

				// make a copy of the current environment with the loop variables
				loopEnv := make(map[string]any)
				for k, envVal := range c.env {
					loopEnv[k] = envVal
				}
				loopEnv[n.LoopVar] = val.Interface()
				if n.LoopIdx != "" {
					loopEnv[n.LoopIdx] = key.Interface()
				}

				var loopComp *chtmlComponent
				if i < len(c.children[n]) {
					if childComp, ok := c.children[n][i].(*chtmlComponent); ok {
						loopComp = childComp
						loopComp.env = loopEnv
					} else {
						c.error(n, fmt.Errorf("unexpected node type: %T", c.children[n][i]))
						continue
					}
				} else {
					loopComp = c.newChildComponent(n, loopEnv)
					c.children[n] = append(c.children[n], loopComp)
				}

				if !yield(loopComp) {
					break
				}
			}
		}
	default:
		c.error(n, fmt.Errorf("c:for expression must return slice, array, or map, got %v", v.Kind()))
		c.closeChildren(n, 0)
		return func(yield func(*chtmlComponent) bool) {}
	}
}

// newChildComponent creates a new child chtmlComponent instance for loops or imports.
func (c *chtmlComponent) newChildComponent(doc *Node, env map[string]any) *chtmlComponent {
	return &chtmlComponent{
		doc:            doc,
		scope:          c.scope,
		env:            env,
		importer:       c.importer,
		renderComments: c.renderComments,
		hidden:         c.hidden,
		children:       make(map[*Node][]Component),
		errs:           nil, // child component starts with no errors
	}
}

func (c *chtmlComponent) scopeHasVar(v string) bool {
	_, ok := c.scope.Vars()[v]
	return ok
}

func convertToRenderShape(v any, shape any) (any, error) {
	// If no target shape is specified, return the original value.
	// `shape` is an `any` (interface{}), so `shape == nil` means the interface itself is nil.
	if shape == nil || shape == AnyShape {
		return v, nil
	}

	targetType := reflect.TypeOf(shape)
	if targetType == nil {
		// This occurs if `shape` is an untyped nil passed as `any`.
		// Consistent with `shape == nil` at the top, implies no specific shape to convert to.
		return v, nil
	}

	// Handle the case where the input value `v` is nil.
	if v == nil {
		// If v is nil, return the zero value for the targetType.
		// return reflect.Zero(targetType).Interface(), nil
		return shape, nil
	}

	sourceValue := reflect.ValueOf(v) // v is not nil at this point
	sourceType := sourceValue.Type()

	// 1. If the types are already the same, return the value as is.
	if sourceType == targetType {
		return v, nil
	}

	// 2. If the source type is directly convertible to the target type using Go's rules.
	if sourceType.ConvertibleTo(targetType) {
		convertedValue := sourceValue.Convert(targetType)
		return convertedValue.Interface(), nil
	}

	// 3. Special conversion rules
	if converted, ok := tryConvertToHtmlNode(v, sourceType, targetType); ok {
		return converted, nil
	}

	if converted, ok := trySliceToSliceConversion(v, shape, sourceType, targetType, sourceValue); ok {
		return converted, nil
	}

	// 4. If none of the above conversion strategies work, return an error.
	return nil, fmt.Errorf("cannot convert type %s to type %s", sourceType.String(), targetType.String())
}

// tryConvertToHtmlNode attempts to convert a value to *html.Node.
// If the targetType is *html.Node, it converts v to an *html.Node representation using repr().
func tryConvertToHtmlNode(v any, sourceType, targetType reflect.Type) (any, bool) {
	htmlNodeType := reflect.TypeOf((*html.Node)(nil))
	if targetType != htmlNodeType {
		return nil, false // Not targeting *html.Node
	}

	if sourceType == htmlNodeType { // Already *html.Node
		return v, true
	}

	if v == nil { // Source is nil (untyped), target is *html.Node
		return (*html.Node)(nil), true
	}

	// Check for typed nils (e.g., (*SomeStruct)(nil), ([]byte)(nil))
	val := reflect.ValueOf(v)
	switch val.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		if val.IsNil() {
			return (*html.Node)(nil), true
		}
	}

	return &html.Node{Type: html.TextNode, Data: repr(v)}, true
}

// trySliceToSliceConversion attempts to convert a slice of interface{} to a slice of a specific type.
// It returns the converted slice and true if successful, otherwise nil and false.
func trySliceToSliceConversion(v any, shape any, sourceType, targetType reflect.Type, sourceValue reflect.Value) (any, bool) {
	if targetType.Kind() == reflect.Slice && sourceType.Kind() == reflect.Slice && sourceType.Elem().Kind() == reflect.Interface {
		targetElemType := targetType.Elem()
		// Optimistically assume all elements can be converted.
		// We will build a new slice of reflect.Value to hold potentially converted elements.
		convertedElements := make([]reflect.Value, sourceValue.Len())

		for i := 0; i < sourceValue.Len(); i++ {
			elem := sourceValue.Index(i).Interface() // Get element as any

			if elem == nil { // Handle nil elements in the source slice
				// If target element type is a pointer, chan, func, interface, map, or slice, its zero value is nil.
				// Otherwise, we can't assign nil to a non-pointer/interface type.
				if !isNilCompatible(targetElemType) {
					return nil, false // Cannot convert, nil is not compatible
				}
				// For nil-compatible types, the zero value is appropriate.
				convertedElements[i] = reflect.Zero(targetElemType)
				continue
			}

			elemValue := reflect.ValueOf(elem)
			if elemValue.Type().ConvertibleTo(targetElemType) {
				convertedElements[i] = elemValue.Convert(targetElemType)
			} else {
				// Try recursive conversion for complex elements
				// Pass the zero value of the target element type as the shape hint for the recursive call.
				recursivelyConverted, err := convertToRenderShape(elem, reflect.Zero(targetElemType).Interface())
				if err == nil {
					// Ensure the recursively converted value is assignable to the target element type.
					// This check is important if convertToRenderShape returns a value of a different,
					// albeit compatible, type.
					val := reflect.ValueOf(recursivelyConverted)
					if val.Type().AssignableTo(targetElemType) {
						convertedElements[i] = val
					} else if val.Type().ConvertibleTo(targetElemType) {
						convertedElements[i] = val.Convert(targetElemType)
					} else {
						return nil, false // Recursive conversion returned an incompatible type
					}
				} else {
					return nil, false // Recursive conversion failed
				}
			}
		}

		// If we've reached here, all elements were convertible (or were nil and compatible).
		// Create the new slice and copy converted elements.
		newSlice := reflect.MakeSlice(targetType, sourceValue.Len(), sourceValue.Len())
		for i := 0; i < sourceValue.Len(); i++ {
			newSlice.Index(i).Set(convertedElements[i])
		}
		return newSlice.Interface(), true
	}
	return nil, false
}

func isNilCompatible(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return true
	default:
		return false
	}
}
