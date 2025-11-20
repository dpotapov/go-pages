package chtml

import (
	"fmt"

	"github.com/expr-lang/expr/ast"
)

// Symbols maps identifier names to their shapes.
type Symbols map[string]*Shape

// TypeError is returned when static type rules are violated.
type TypeError struct {
	Msg        string
	Pos        int    // optional: position in expression, if available
	MemberName string // the specific member being accessed (e.g., "pool_id")
	ObjectExpr string // the object expression being accessed (e.g., "req.body")
}

func (e *TypeError) Error() string {
	return e.Msg
}

// Check performs static checking over an expr-lang AST and returns its resulting shape.
// The implementation is intentionally conservative and only infers shapes that are
// safe and obvious from literals and simple constructs.
func Check(root ast.Node, sym Symbols) (*Shape, error) {
	if root == nil {
		return Any, nil
	}
	return shapeOf(root, sym)
}

// shapeLiteralFromAST attempts to parse a shape literal from an expr AST node.
// Accepts:
//   - Atoms: identifiers "any", "bool", "string", "number", "html"
//   - Array literals with single element that is a shape literal: [T]
//   - Map literals with string or identifier keys: {k: T, ...}
//
// Returns (shape, true) if successfully parsed, otherwise (Any, false).
func shapeLiteralFromAST(n ast.Node) (*Shape, bool) {
	switch node := n.(type) {
	case *ast.IdentifierNode:
		switch node.Value {
		case "any":
			return Any, true
		case "bool":
			return Bool, true
		case "string":
			return String, true
		case "number":
			return Number, true
		case "html":
			return Html, true
		default:
			return Any, false
		}
	case *ast.ArrayNode:
		// Empty array [] means array of any
		if len(node.Nodes) == 0 {
			return ArrayOf(Any), true
		}
		// Single element specifies the element shape
		if len(node.Nodes) == 1 {
			if elem, ok := shapeLiteralFromAST(node.Nodes[0]); ok {
				return ArrayOf(elem), true
			}
		}
		return Any, false
	case *ast.MapNode:
		// {} or {k: T, ...}
		if len(node.Pairs) == 0 {
			return Object(nil), true
		}

		// Check for context-sensitive "_" interpretation:
		// { "_": shape } (single key) → map type with uniform values
		// { "_": shape, other: ... } → object with literal "_" field
		if len(node.Pairs) == 1 {
			p, ok := node.Pairs[0].(*ast.PairNode)
			if !ok {
				return Any, false
			}
			var key string
			switch k := p.Key.(type) {
			case *ast.StringNode:
				key = k.Value
			case *ast.IdentifierNode:
				key = k.Value
			default:
				return Any, false
			}

			// Single "_" key → map type
			if key == "_" {
				valueShape, ok := shapeLiteralFromAST(p.Value)
				if !ok {
					return Any, false
				}
				// Return map type: Fields=nil, Elem=valueShape
				return &Shape{
					Kind:   ShapeObject,
					Fields: nil,
					Elem:   valueShape,
				}, true
			}
		}

		// Regular object with named fields (including literal "_" if other keys present)
		fields := make(map[string]*Shape, len(node.Pairs))
		for _, pn := range node.Pairs {
			p, ok := pn.(*ast.PairNode)
			if !ok {
				return Any, false
			}
			var key string
			switch k := p.Key.(type) {
			case *ast.StringNode:
				key = k.Value
			case *ast.IdentifierNode:
				key = k.Value
			default:
				return Any, false
			}
			val, ok := shapeLiteralFromAST(p.Value)
			if !ok {
				return Any, false
			}
			fields[key] = val
		}
		return Object(fields), true
	default:
		return Any, false
	}
}

// shapeOf returns the inferred shape of the node and may return a TypeError when it detects
// statically invalid constructs. It still attempts to return a conservative
// shape to enable partial reasoning by callers.
func shapeOf(n ast.Node, sym Symbols) (*Shape, error) {
	switch node := n.(type) {
	case *ast.IntegerNode, *ast.FloatNode:
		return Number, nil
	case *ast.BoolNode:
		return Bool, nil
	case *ast.StringNode:
		return String, nil
	case *ast.ConstantNode:
		return ShapeFrom(node.Value), nil
	case *ast.NilNode:
		return Any, nil
	case *ast.IdentifierNode:
		if sym != nil {
			if s, ok := sym[node.Value]; ok && s != nil {
				return s, nil
			}
		}
		// Treat unknown identifiers as Any without error
		return Any, nil
	case *ast.MemberNode:
		obj, err := shapeOf(node.Node, sym)
		if err != nil {
			return Any, err
		}

		// Extract object expression for error context
		/*
			var objectExpr string
			switch objNode := node.Node.(type) {
			case *ast.IdentifierNode:
				objectExpr = objNode.Value
			case *ast.MemberNode:
				objectExpr = "nested object"
			default:
				objectExpr = "expression"
			}
		*/

		// Extract member name for error context
		/*
			var memberName string
			switch prop := node.Property.(type) {
			case *ast.StringNode:
				memberName = prop.Value
			case *ast.IdentifierNode:
				memberName = prop.Value
			}
		*/

		// Extract position information
		loc := node.Location()

		if obj == nil || obj.Kind != ShapeObject {
			// Extract member name for error context
			var memberName string
			switch prop := node.Property.(type) {
			case *ast.StringNode:
				memberName = prop.Value
			case *ast.IdentifierNode:
				memberName = prop.Value
			case *ast.IntegerNode:
				memberName = fmt.Sprintf("[%d]", prop.Value)
			case *ast.MemberNode:
				memberName = "nested property"
			default:
				memberName = fmt.Sprintf("<%T>", prop)
			}

			// Extract object expression for error context
			var objectExpr string
			switch objNode := node.Node.(type) {
			case *ast.IdentifierNode:
				objectExpr = objNode.Value
			case *ast.MemberNode:
				objectExpr = "nested object"
			default:
				objectExpr = "expression"
			}

			objShape := "nil"
			if obj != nil {
				objShape = obj.Kind.String()
			}

			// Special handling for array access
			if obj != nil && obj.Kind == ShapeArray {
				// Check if this is array indexing (IntegerNode) vs invalid member access
				switch node.Property.(type) {
				case *ast.IntegerNode:
					// Valid array indexing - return element shape
					elem := obj.Elem
					if elem == nil {
						elem = Any
					}
					return elem, nil
				default:
					// Invalid member access on array
					return Any, &TypeError{
						Msg:        fmt.Sprintf("cannot access member %s on array of shape %s", memberName, obj.Kind.String()),
						Pos:        loc.From,
						MemberName: memberName,
						ObjectExpr: objectExpr,
					}
				}
			}

			return Any, &TypeError{
				Msg:        fmt.Sprintf("cannot access member '%s' on %s of shape %s", memberName, objectExpr, objShape),
				Pos:        loc.From,
				MemberName: memberName,
				ObjectExpr: objectExpr,
			}
		}

		// Handle member access on objects
		switch prop := node.Property.(type) {
		case *ast.StringNode:
			// Try named fields first
			if obj.Fields != nil {
				if fs, ok := obj.Fields[prop.Value]; ok {
					return fs, nil
				}
			}
			// For map types (Elem != nil, Fields == nil), return uniform value type
			if obj.Elem != nil && obj.Fields == nil {
				return obj.Elem, nil
			}
			// Unshaped object (Fields == nil, Elem == nil)
			return Any, nil
		case *ast.IdentifierNode:
			// Try named fields first
			if obj.Fields != nil {
				if fs, ok := obj.Fields[prop.Value]; ok {
					return fs, nil
				}
			}
			// For map types (Elem != nil, Fields == nil), return uniform value type
			if obj.Elem != nil && obj.Fields == nil {
				return obj.Elem, nil
			}
			// Unshaped object (Fields == nil, Elem == nil)
			return Any, nil
		case *ast.MemberNode:
			// Handle nested member access like obj.nested.property
			return shapeOf(prop, sym)
		default:
			return Any, &TypeError{
				Msg: fmt.Sprintf("unsupported member property type for %s: %T", prop, prop),
				Pos: loc.From,
			}
		}
	case *ast.ArrayNode:
		if len(node.Nodes) == 0 {
			return ArrayOf(Any), nil
		}
		var elem *Shape
		for _, el := range node.Nodes {
			s, err := shapeOf(el, sym)
			if err != nil {
				return ArrayOf(Any), err
			}
			elem = elem.Merge(s)
		}
		if elem == nil {
			elem = Any
		}
		return ArrayOf(elem), nil
	case *ast.MapNode:
		if len(node.Pairs) == 0 {
			return Object(map[string]*Shape{}), nil
		}

		// Check for context-sensitive "_" interpretation:
		// { "_": shape } (single key) → map type with uniform values
		// { "_": shape, other: ... } → object with literal "_" field
		if len(node.Pairs) == 1 {
			p, ok := node.Pairs[0].(*ast.PairNode)
			if ok {
				var key string
				switch k := p.Key.(type) {
				case *ast.StringNode:
					key = k.Value
				case *ast.IdentifierNode:
					key = k.Value
				}

				// Single "_" key → map type
				if key == "_" {
					valueShape, err := shapeOf(p.Value, sym)
					if err != nil {
						return Any, err
					}
					// Return map type: Fields=nil, Elem=valueShape
					return &Shape{
						Kind:   ShapeObject,
						Fields: nil,
						Elem:   valueShape,
					}, nil
				}
			}
		}

		// Regular object with named fields (including literal "_" if other keys present)
		fields := make(map[string]*Shape, len(node.Pairs))
		for _, pn := range node.Pairs {
			p, ok := pn.(*ast.PairNode)
			if !ok {
				continue
			}
			var key string
			switch k := p.Key.(type) {
			case *ast.StringNode:
				key = k.Value
			case *ast.IdentifierNode:
				key = k.Value
			default:
				// non-string keys are not representable in our object shape → skip
				continue
			}
			val, err := shapeOf(p.Value, sym)
			if err != nil {
				return Object(fields), err
			}
			fields[key] = val
		}
		return Object(fields), nil
	case *ast.ConditionalNode:
		t, err := shapeOf(node.Exp1, sym)
		if err != nil {
			return Any, err
		}
		f, err := shapeOf(node.Exp2, sym)
		if err != nil {
			return Any, err
		}
		if t.Equal(f) {
			return t, nil
		}
		if t.Kind == ShapeHtml || f.Kind == ShapeHtml {
			return Html, nil
		}
		return Any, nil
	case *ast.UnaryNode:
		return shapeOf(node.Node, sym)
	case *ast.BinaryNode:
		// propagate inner errors but do not attempt to infer
		if _, err := shapeOf(node.Left, sym); err != nil {
			return Any, err
		}
		if _, err := shapeOf(node.Right, sym); err != nil {
			return Any, err
		}
		return Any, nil
	case *ast.CallNode:
		if id, ok := node.Callee.(*ast.IdentifierNode); ok {
			switch id.Value {
			case "cast":
				if len(node.Arguments) < 2 {
					return Any, &TypeError{
						Msg: "cast: missing shape argument",
						Pos: node.Location().From,
					}
				}
				if shp, ok := shapeLiteralFromAST(node.Arguments[1]); ok {
					if _, err := shapeOf(node.Arguments[0], sym); err != nil {
						return shp, err
					}
					return shp, nil
				}
				return Any, &TypeError{
					Msg: "cast: second argument must be a shape literal",
					Pos: node.Location().From,
				}
			case "type":
				if len(node.Arguments) < 1 {
					return Any, &TypeError{
						Msg: "type: missing argument",
						Pos: node.Location().From,
					}
				}
				return shapeOf(node.Arguments[0], sym)
			case "duration":
				return Number, nil
			case "formatDuration":
				if len(node.Arguments) < 1 {
					return Any, &TypeError{
						Msg: "formatDuration: missing argument",
						Pos: node.Location().From,
					}
				}
				return String, nil
			case "filter", "sort", "reverse", "unique", "take":
				if len(node.Arguments) < 1 {
					return Any, &TypeError{
						Msg: fmt.Sprintf("%s: missing array argument", id.Value),
						Pos: node.Location().From,
					}
				}
				// These functions return the same shape as their first argument (the array)
				return shapeOf(node.Arguments[0], sym)
			case "combine":
				if len(node.Arguments) == 1 {
					return shapeOf(node.Arguments[0], sym)
				}
				allString := true
				for _, a := range node.Arguments {
					s, err := shapeOf(a, sym)
					if err != nil {
						return Any, err
					}
					if s != String {
						allString = false
					}
				}
				if allString {
					return String, nil
				}
				return Any, nil
			}
		}
		for _, a := range node.Arguments {
			if _, err := shapeOf(a, sym); err != nil {
				return Any, err
			}
		}
		return Any, nil
	case *ast.BuiltinNode:
		switch node.Name {
		case "filter", "sort", "reverse", "unique", "take":
			if len(node.Arguments) < 1 {
				return Any, &TypeError{
					Msg: fmt.Sprintf("%s: missing array argument", node.Name),
					Pos: node.Location().From,
				}
			}
			// These functions return the same shape as their first argument (the array)
			return shapeOf(node.Arguments[0], sym)
		default:
			// Other built-ins return Any
			for _, a := range node.Arguments {
				if _, err := shapeOf(a, sym); err != nil {
					return Any, err
				}
			}
			return Any, nil
		}
	case *ast.VariableDeclaratorNode:
		// Handle let expressions: let x = value; expr
		// First, determine the shape of the value
		valueShape, err := shapeOf(node.Value, sym)
		if err != nil {
			return Any, err
		}

		// Create a new symbol table with the variable binding
		newSym := make(Symbols)
		if sym != nil {
			for k, v := range sym {
				newSym[k] = v
			}
		}
		newSym[node.Name] = valueShape

		// Evaluate the expression with the new binding
		return shapeOf(node.Expr, newSym)
	case *ast.SequenceNode:
		// Handle sequence of expressions: expr1; expr2; ...
		// Each expression may introduce new variable bindings
		currentSym := sym
		var lastShape *Shape
		var lastErr error

		for _, expr := range node.Nodes {
			if varDecl, ok := expr.(*ast.VariableDeclaratorNode); ok {
				// Variable declaration - update symbols
				valueShape, err := shapeOf(varDecl.Value, currentSym)
				if err != nil {
					return Any, err
				}

				// Create new symbol table with this binding
				newSym := make(Symbols)
				if currentSym != nil {
					for k, v := range currentSym {
						newSym[k] = v
					}
				}
				newSym[varDecl.Name] = valueShape
				currentSym = newSym

				// The shape of the declaration itself is the shape of the expression
				lastShape, lastErr = shapeOf(varDecl.Expr, currentSym)
			} else {
				// Regular expression
				lastShape, lastErr = shapeOf(expr, currentSym)
			}

			if lastErr != nil {
				return Any, lastErr
			}
		}

		if lastShape == nil {
			return Any, nil
		}
		return lastShape, nil
	default:
		return Any, nil
	}
}
