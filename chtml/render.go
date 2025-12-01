package chtml

import (
	"fmt"
	"iter"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

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
func (c *chtmlComponent) render(n *Node) (any, error) {
	var res, rr any // res is the accumulated result, rr is the result of the current node/operation

	// Delegate C element entirely to its own renderer to keep this method clean
	if n.Type == cNode {
		return c.renderC(n)
	}

	cond, err := c.evalIf(n)
	if err != nil {
		return nil, err
	}

	if cond.shouldRender {
		defer c.bindVar(cond.bindVar, cond.bindValue)()

		seq, err := c.evalFor(n)
		if err != nil {
			return nil, err
		}
		for loopComp := range seq {
			var renderErr error
			switch n.Type {
			case html.ElementNode:
				rr, renderErr = loopComp.renderElement(n)
			case html.TextNode:
				rr, renderErr = loopComp.renderText(n)
			case html.CommentNode:
				rr, renderErr = loopComp.renderComment(n)
			case html.DocumentNode:
				rr, renderErr = loopComp.renderDocument(n)
			case html.DoctypeNode:
				// Skip doctype nodes - they should not be rendered to avoid conflicts
				// when components are nested (e.g., comp1 with doctype imports comp2 with doctype)
				rr = nil
			case importNode:
				rr, renderErr = loopComp.renderImport(n)
			default:
				return nil, newComponentError(n, fmt.Errorf("unexpected node type: %v", n.Type))
			}

			if renderErr != nil {
				return nil, renderErr
			}
			res = AnyPlusAny(res, rr)
		}
	}

	// Apply RenderShape conversion if specified for the node
	if n.RenderShape != nil {
		convertedRes, convErr := convertToRenderShape(res, n.RenderShape)
		if convErr != nil {
			return nil, newComponentError(n, fmt.Errorf("convert to render shape: %w", convErr))
		}
		return convertedRes, nil
	}
	return res, nil
}

// renderText evaluates the interpolated expression for the given text node and stores the result in
// the destination node.
// If the text node is not an expression, the value is copied as is.
func (c *chtmlComponent) renderText(n *Node) (any, error) {
	res, err := n.Data.Value(&c.vm, c.env)
	if err != nil {
		return nil, newComponentError(n, fmt.Errorf("eval text: %w", err))
	}

	return res, nil
}

func (c *chtmlComponent) renderComment(n *Node) (*html.Node, error) {
	if c.renderComments {
		data, err := n.Data.Value(&c.vm, c.env)
		if err != nil {
			return nil, newComponentError(n, fmt.Errorf("eval comment: %w", err))
		}
		return &html.Node{
			Type: html.CommentNode,
			Data: fmt.Sprint(data),
		}, nil
	}
	return nil, nil
}

func (c *chtmlComponent) renderDocument(n *Node) (any, error) {
	var res any

	for child := n.FirstChild; child != nil; child = child.NextSibling {
		rr, err := c.render(child)
		if err != nil {
			return nil, err
		}
		if rr == nil {
			continue
		}

		if _, ok := rr.(Attribute); ok {
			// Silently ignore root-level c:attr outputs; no env mutation or output.
			continue
		} else {
			res = AnyPlusAny(res, rr)
		}
	}
	return res, nil
}

func (c *chtmlComponent) renderElement(n *Node) (any, error) {
	clone := &html.Node{
		Type:     html.ElementNode,
		DataAtom: n.DataAtom,
		Data:     n.Data.RawString(),
	}

	// eval attributes into values for the cloned node
	if err := c.renderAttrs(clone, n); err != nil {
		return nil, newComponentError(n, fmt.Errorf("eval attributes: %w", err))
	}

	var res any

	for child := n.FirstChild; child != nil; child = child.NextSibling {
		rr, err := c.render(child)
		if err != nil {
			return nil, err
		}
		if rr == nil {
			continue
		}
		if attr, ok := rr.(Attribute); ok {
			v, err := attr.Val.Value(&c.vm, c.env)
			if err != nil {
				return nil, newComponentError(n, fmt.Errorf("eval attr %q: %w", attr.Key, err))
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

	return clone, nil
}

// renderImport renders the imported component (<c:NAME>) and appends the result to the destination.
func (c *chtmlComponent) renderImport(n *Node) (any, error) {
	// Build variables for the imported component
	vars := make(map[string]any)
	for _, attr := range n.Attr {
		res, err := attr.Val.Value(&c.vm, c.env)
		if err != nil {
			return nil, newComponentError(n, fmt.Errorf("eval attr %q: %w", attr.Key, err))
		}
		snake := toSnakeCase(attr.Key)
		vars[snake] = res
	}

	if n.FirstChild != nil {
		vars["_"] = nil

		for child := n.FirstChild; child != nil; child = child.NextSibling {
			rr, err := c.render(child)
			if err != nil {
				return nil, err
			}
			if attr, ok := rr.(Attribute); ok {
				v, errAttr := attr.Val.Value(&c.vm, c.env)
				if errAttr != nil {
					return nil, newComponentError(n, fmt.Errorf("eval attr %q: %w", attr.Key, errAttr))
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
			return nil, newComponentError(n, fmt.Errorf("eval import name: %w", err))
		}
		impNameStr, ok := impName.(string)
		if !ok {
			return nil, newComponentError(n, fmt.Errorf("import name must be a string"))
		}
		imp := c.importer
		if impNameStr == "c:attr" {
			imp = &builtinImporter{}
		}
		if imp == nil {
			return nil, newComponentError(n, ErrImportNotAllowed)
		}
		comp, err = imp.Import(impNameStr[2:])
		if err != nil {
			return nil, newComponentError(n, fmt.Errorf("import %q: %w", impNameStr, err))
		}

		// save component for reuse:
		c.children[n] = append(c.children[n], comp)
	}

	// Decode import vars to the component's expected shapes (for flags, etc.)
	if ish := comp.InputShape(); ish != nil && ish.Kind == ShapeObject {
		for key, fieldShape := range ish.Fields {
			if key == "_" {
				continue
			}
			if raw, ok := vars[key]; ok {
				// Special handling: implied boolean flag when attribute present with no value
				if fieldShape == Bool {
					if str, isStr := raw.(string); isStr {
						if str == "" {
							raw = true
						} else {
							return nil, newComponentError(n, &DecodeError{Key: key, Err: fmt.Errorf("string value %q cannot be converted to bool, use ${true|false} syntax instead", str)})
						}
					}
				}
				if fieldShape != nil && fieldShape != Any {
					converted, convErr := convertToRenderShape(raw, fieldShape)
					if convErr != nil {
						return nil, newComponentError(n, &DecodeError{Key: key, Err: convErr})
					}
					vars[key] = converted
				}
			}
		}
	}

	rr, errRender := comp.Render(s)
	if errRender != nil {
		return nil, newComponentError(n, fmt.Errorf("render import %s: %w", n.Data.RawString(), errRender))
	}
	return rr, nil
}

// renderC renders the <c> special element: passthrough, var binding, loops, conditionals
func (c *chtmlComponent) renderC(n *Node) (any, error) {
	// Handle conditionals first
	cond, err := c.evalIf(n)
	if err != nil {
		return nil, err
	}
	if !cond.shouldRender {
		return nil, nil
	}

	defer c.bindVar(cond.bindVar, cond.bindValue)()

	// Determine var name if present (snake_case for consistency)
	var varName string
	for _, attr := range n.Attr {
		if strings.EqualFold(attr.Key, "var") {
			varName = toSnakeCase(attr.Val.RawString())
			break
		}
	}

	var agg any
	// Iterate (or single pass if no loop)
	seq, err := c.evalFor(n)
	if err != nil {
		return nil, err
	}
	for childComp := range seq {
		// Render children of <c>
		var iterRes any
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			rr, err := childComp.render(child)
			if err != nil {
				return nil, err
			}
			iterRes = AnyPlusAny(iterRes, rr)
		}
		agg = AnyPlusAny(agg, iterRes)
	}

	// If var present, bind aggregated content into env and suppress output
	if varName != "" {
		if _, exists := c.env[varName]; !exists || c.env[varName] == nil {
			// Perform cast validation and type conversion if explicit shape is provided
			finalValue := agg
			if n.VarShape != nil && n.VarShape.Kind != ShapeString && isWhitespaceString(finalValue) {
				finalValue = nil
			}
			if n.VarShape != nil {
				// Validate the shape (cast semantics)
				if err := validateShape(finalValue, n.VarShape, ""); err != nil {
					return nil, newComponentError(n, &CastError{
						Expected: n.VarShape,
						Actual:   finalValue,
						Err:      err,
					})
				}
				// Then perform type coercion
				converted, err := convertToRenderShape(finalValue, n.VarShape)
				if err != nil {
					return nil, newComponentError(n, fmt.Errorf("cannot convert %T to %s: %w", finalValue, n.VarShape.String(), err))
				}
				finalValue = converted
			}
			c.env[varName] = finalValue
		}
		return nil, nil
	}

	// Passthrough mode: return aggregated content (or nil if none)
	return agg, nil
}

// renderAttrs loops over the attributes of the source node and evaluates the expressions for them.
// If the attribute has no associated expression, it is copied as is.
// If the given element is an import, skip the evaluation and return immediately.
func (c *chtmlComponent) renderAttrs(dst *html.Node, n *Node) error {
	attrs := make([]html.Attribute, 0, len(n.Attr))

	for _, attr := range n.Attr {
		v, err := attr.Val.Value(&c.vm, c.env)
		if err != nil {
			return fmt.Errorf("eval attr %q: %w", attr.Key, err)
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

// condResult contains the result of evaluating a condition.
type condResult struct {
	shouldRender bool
	bindVar      string // Variable name to bind (empty if none)
	bindValue    any    // Value to bind
}

// evalIf evaluates the conditional expression (c:if, c:else-if, c:else) for the given node and
// marks it as hidden if the condition is false.
// Returns the result indicating whether to render and any variable to bind.
// For conditions with shape matching ("EXPR is SHAPE as IDENT"), also returns the bound variable info.
func (c *chtmlComponent) evalIf(n *Node) (condResult, error) {
	if n.Cond.IsEmpty() {
		return condResult{shouldRender: true}, nil // no condition, render by default
	}

	if _, ok := c.hidden[n]; ok {
		delete(c.hidden, n) // reset hidden state for the next rendering cycle
		return condResult{shouldRender: false}, nil
	}

	// Evaluate the expression
	res, err := n.Cond.Value(&c.vm, c.env)
	if err != nil {
		return condResult{}, newComponentError(n, fmt.Errorf("eval c:if: %w", err))
	}

	// Check if this is a shape matching condition
	if n.Cond.IsMatchCond() {
		return c.evalIfWithMatch(n, res)
	}

	// Regular condition - check truthiness
	render := isTruthy(res)

	if render {
		// mark next conditional as not rendered
		for next := n.NextCond; next != nil; next = next.NextCond {
			c.hidden[next] = struct{}{}
			c.closeChildren(next, 0)
		}
	} else {
		c.closeChildren(n, 0)
	}
	return condResult{shouldRender: render}, nil
}

// evalIfWithMatch handles conditions with shape matching ("EXPR is SHAPE as IDENT").
func (c *chtmlComponent) evalIfWithMatch(n *Node, res any) (condResult, error) {
	cond := n.Cond

	// Check if the value matches the shape
	matched := matchShape(res, cond.Shape())

	result := condResult{shouldRender: matched}

	if matched {
		// Prepare variable binding info if specified
		if cond.BindVar() != "" {
			result.bindVar = toSnakeCase(cond.BindVar())
			result.bindValue = res
		}

		// mark next conditional as not rendered
		for next := n.NextCond; next != nil; next = next.NextCond {
			c.hidden[next] = struct{}{}
			c.closeChildren(next, 0)
		}
	} else {
		c.closeChildren(n, 0)
	}

	return result, nil
}

// isTruthy returns true if the value is considered truthy for conditional rendering.
func isTruthy(res any) bool {
	switch v := res.(type) {
	case bool:
		return v
	case string:
		return v != ""
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return v != 0
	case float32, float64:
		return v != 0.0
	case nil:
		return false
	default:
		// check if the value is a non-empty slice
		rv := reflect.ValueOf(res)
		if rv.Kind() == reflect.Slice && rv.Len() == 0 {
			return false
		}
		// check if the value is a non-empty map
		if rv.Kind() == reflect.Map && rv.Len() == 0 {
			return false
		}
		return true
	}
}

// matchShape checks if a value structurally matches the expected shape.
// This is used for "EXPR is SHAPE" conditional matching.
// Unlike validateShape (which is for casting), this returns false for nil values
// since nil represents the absence of a value.
func matchShape(v any, shape *Shape) bool {
	// nil doesn't match any shape (design choice for pattern matching)
	if v == nil || isTypedNil(v) {
		return false
	}

	// nil shape or Any accepts everything (except nil, handled above)
	if shape == nil || shape == Any {
		return true
	}

	switch shape.Kind {
	case ShapeBool:
		_, ok := v.(bool)
		return ok

	case ShapeString:
		_, ok := v.(string)
		return ok

	case ShapeNumber:
		switch v.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
			return true
		default:
			return false
		}

	case ShapeArray:
		rv := reflect.ValueOf(v)
		if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
			return false
		}
		// For array matching, we don't validate each element shape
		// (that would be too strict for pattern matching)
		return true

	case ShapeObject:
		rv := reflect.ValueOf(v)
		// Dereference pointers to get the underlying type
		for rv.Kind() == reflect.Ptr {
			if rv.IsNil() {
				return false
			}
			rv = rv.Elem()
		}

		// Handle map[string]any
		if m, ok := v.(map[string]any); ok {
			if shape.Fields != nil {
				// Check that all required fields exist and match their shapes
				for k, fieldShape := range shape.Fields {
					val, exists := m[k]
					if !exists {
						// Missing field is not a match
						return false
					}
					if !matchShape(val, fieldShape) {
						return false
					}
				}
			}
			return true
		}

		// Handle structs and other object types
		if rv.Kind() == reflect.Struct || rv.Kind() == reflect.Map {
			return true
		}

		return false

	case ShapeHtml, ShapeHtmlAttr:
		// HTML shapes accept any value for matching purposes
		return true

	default:
		return true
	}
}

// evalFor evaluates the loop expression (c:for) for the given node and updates the environment
// with the loop variables.
// If no loop expression is present, the function return true only once (assuming that the node
// should be rendered by default).
func (c *chtmlComponent) evalFor(n *Node) (iter.Seq[*chtmlComponent], error) {
	if n.Loop.IsEmpty() {
		return func(yield func(*chtmlComponent) bool) {
			yield(c)
		}, nil
	}

	res, err := n.Loop.Value(&c.vm, c.env)
	if err != nil {
		c.closeChildren(n, 0)
		return nil, newComponentError(n, fmt.Errorf("eval c:for: %w", err))
	}
	v := reflect.ValueOf(res)

	if res == nil || !v.IsValid() {
		c.closeChildren(n, 0)
		return func(yield func(*chtmlComponent) bool) {}, nil
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
						// Internal error - shouldn't happen in normal usage
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
		}, nil
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
					if key.Kind() != reflect.String {
						// Skip non-string keys silently
						continue
					}
					loopEnv[n.LoopIdx] = key.String()
				}

				var loopComp *chtmlComponent
				if i < len(c.children[n]) {
					if childComp, ok := c.children[n][i].(*chtmlComponent); ok {
						loopComp = childComp
						loopComp.env = loopEnv
					} else {
						// Internal error - shouldn't happen in normal usage
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
		}, nil
	default:
		c.closeChildren(n, 0)
		return nil, newComponentError(n, fmt.Errorf("c:for expression must return slice, array, or map, got %v", v.Kind()))
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
	}
}

// bindVar temporarily binds a variable in the environment and returns a cleanup function.
// If name is empty, returns a no-op cleanup function.
// The cleanup function restores the previous value (or deletes the var if it didn't exist).
func (c *chtmlComponent) bindVar(name string, value any) func() {
	if name == "" {
		return func() {}
	}
	oldValue, existed := c.env[name]
	c.env[name] = value
	return func() {
		if existed {
			c.env[name] = oldValue
		} else {
			delete(c.env, name)
		}
	}
}

// fmtShapeError formats a shape validation error with path context.
func fmtShapeError(path, expected string, v any) error {
	if path == "" {
		return fmt.Errorf("expected %s, got %T", expected, v)
	}
	return fmt.Errorf("at %s: expected %s, got %T", path, expected, v)
}

// validateShape checks if a value structurally matches the expected shape.
// It returns an error if the value cannot be cast to the shape.
//
// Semantics:
//   - Missing fields in objects are allowed (will be zero-filled by convertToRenderShape)
//   - Extra fields in objects are allowed (structural subtyping)
//   - nil values are allowed for any shape (will be zero-filled)
//   - ShapeNumber accepts strings (actual parsing/validation happens in convertToRenderShape)
//   - ShapeString accepts any value (will be stringified)
//
// The path parameter tracks the location for nested error messages (e.g., "[0].field").
func validateShape(v any, shape *Shape, path string) error {
	if shape == nil || shape == Any {
		return nil // Any accepts everything
	}

	// Handle nil values - they're only valid if the shape allows them
	if v == nil || isTypedNil(v) {
		// nil is acceptable for any shape (will be zero-filled)
		return nil
	}

	switch shape.Kind {
	case ShapeBool:
		if _, ok := v.(bool); !ok {
			return fmtShapeError(path, "bool", v)
		}
		return nil

	case ShapeString:
		// String accepts any value (will be stringified)
		return nil

	case ShapeNumber:
		switch v.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
			return nil
		case time.Duration:
			// time.Duration is int64 under the hood
			return nil
		case string:
			// Strings are accepted here; actual numeric parsing and validation
			// happens in convertToRenderShape. Invalid strings like "hello"
			// will fail there, not here. This allows form inputs and JSON
			// string numbers to pass shape validation.
			return nil
		default:
			return fmtShapeError(path, "number", v)
		}

	case ShapeArray:
		rv := reflect.ValueOf(v)
		if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
			return fmtShapeError(path, "array", v)
		}
		// Validate each element
		for i := 0; i < rv.Len(); i++ {
			elem := rv.Index(i).Interface()
			elemPath := fmt.Sprintf("%s[%d]", path, i)
			if err := validateShape(elem, shape.Elem, elemPath); err != nil {
				return err
			}
		}
		return nil

	case ShapeObject:
		rv := reflect.ValueOf(v)
		// Dereference pointers to get the underlying type
		for rv.Kind() == reflect.Ptr {
			if rv.IsNil() {
				return nil // nil pointer is acceptable (will be zero-filled)
			}
			rv = rv.Elem()
		}
		// Handle map[string]any
		if m, ok := v.(map[string]any); ok {
			if shape.Fields != nil {
				// Validate each declared field
				for k, fieldShape := range shape.Fields {
					fieldPath := path
					if fieldPath == "" {
						fieldPath = k
					} else {
						fieldPath = fmt.Sprintf("%s.%s", path, k)
					}
					if val, exists := m[k]; exists {
						if err := validateShape(val, fieldShape, fieldPath); err != nil {
							return err
						}
					}
					// Missing fields are allowed (will be zero-filled)
				}
			}
			return nil
		}
		// Handle structs and pointers to structs - allow them to pass through
		if rv.Kind() == reflect.Struct {
			return nil
		}
		// Other object-like types are acceptable
		if rv.Kind() == reflect.Map {
			return nil
		}
		return fmtShapeError(path, "object", v)

	case ShapeHtml, ShapeHtmlAttr:
		// HTML shapes accept various types (will be converted)
		return nil

	default:
		return nil // Unknown shapes are permissive
	}
}

func convertToRenderShape(v any, shape *Shape) (any, error) {
	if shape == nil || shape == Any {
		return v, nil
	}

	if v == nil || isTypedNil(v) {
		v = zeroValueForShape(shape)
	}

	switch shape.Kind {
	case ShapeHtml:
		if node, ok := v.(*html.Node); ok {
			return node, nil
		}
		if v == nil {
			return (*html.Node)(nil), nil
		}
		return &html.Node{Type: html.TextNode, Data: repr(v)}, nil
	case ShapeString:
		if v == nil {
			return "", nil
		}
		return fmt.Sprint(v), nil
	case ShapeBool:
		switch vv := v.(type) {
		case bool:
			return vv, nil
		case nil:
			return false, nil
		default:
			return nil, fmt.Errorf("cannot convert type %T to bool", v)
		}
	case ShapeNumber:
		switch vv := v.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
			return vv, nil
		case time.Duration:
			// time.Duration is int64 under the hood
			return int64(vv), nil
		case string:
			// Accept numeric strings (e.g. from http forms) and convert to float64
			// Downstream code may convert to specific int/float types as needed.
			if vv == "" {
				return float64(0), nil
			}
			if i, err := strconv.ParseInt(vv, 10, 64); err == nil {
				return float64(i), nil
			}
			if f, err := strconv.ParseFloat(vv, 64); err == nil {
				return f, nil
			}
			return nil, fmt.Errorf("cannot convert string %q to number", vv)
		default:
			return nil, fmt.Errorf("cannot convert type %T to number", v)
		}
	case ShapeArray:
		if v == nil {
			return []any{}, nil
		}
		rv := reflect.ValueOf(v)
		if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
			return nil, fmt.Errorf("cannot convert type %T to array", v)
		}
		out := make([]any, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			elem := rv.Index(i).Interface()
			converted, err := convertToRenderShape(elem, shape.Elem)
			if err != nil {
				return nil, err
			}
			out[i] = converted
		}
		return out, nil
	case ShapeObject:
		if v == nil {
			return zeroValueForShape(shape), nil
		}
		// Handle whitespace strings as empty values for objects with named fields
		if isWhitespaceString(v) && shape.Fields != nil {
			return zeroValueForShape(shape), nil
		}
		if m, ok := v.(map[string]any); ok {
			if shape.Fields != nil {
				for k, fs := range shape.Fields {
					if val, exists := m[k]; exists {
						if conv, err := convertToRenderShape(val, fs); err == nil {
							m[k] = conv
						}
					} else {
						m[k] = zeroValueForShape(fs)
					}
				}
			}
			return m, nil
		}
		// Allow structs and other object types to pass through; downstream rendering will stringify/JSON them
		return v, nil
	default:
		return v, nil
	}
}

func isTypedNil(v any) bool {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Map, reflect.Func, reflect.Pointer, reflect.Interface, reflect.Chan:
		return rv.IsNil()
	default:
		return false
	}
}

func zeroValueForShape(shape *Shape) any {
	if shape == nil || shape == Any {
		return nil
	}

	switch shape.Kind {
	case ShapeBool:
		return false
	case ShapeString:
		return ""
	case ShapeNumber:
		return float64(0)
	case ShapeArray:
		return []any{}
	case ShapeObject:
		if shape.Fields == nil {
			return map[string]any{}
		}
		out := make(map[string]any, len(shape.Fields))
		for k, field := range shape.Fields {
			out[k] = zeroValueForShape(field)
		}
		return out
	case ShapeHtml:
		return (*html.Node)(nil)
	case ShapeHtmlAttr:
		return nil
	default:
		return nil
	}
}

func isWhitespaceString(v any) bool {
	str, ok := v.(string)
	if !ok {
		return false
	}
	return strings.TrimSpace(str) == ""
}
