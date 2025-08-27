package chtml

import (
	"github.com/expr-lang/expr/ast"
)

// Symbols maps identifier names to their shapes.
type Symbols map[string]*Shape

// TypeError is returned when static type rules are violated.
type TypeError struct {
	Msg string
	Pos int // optional: position in expression, if available
}

func (e *TypeError) Error() string { return e.Msg }

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
		// Expect exactly one element representing the element shape
		if len(node.Nodes) != 1 {
			return Any, false
		}
		if elem, ok := shapeLiteralFromAST(node.Nodes[0]); ok {
			return ArrayOf(elem), true
		}
		return Any, false
	case *ast.MapNode:
		// {} or {k: T, ...}
		if len(node.Pairs) == 0 {
			return Object(nil), true
		}
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
		if obj == nil || obj.Kind != ShapeObject {
			return Any, &TypeError{Msg: "member access on non-object"}
		}
		if obj.Fields == nil {
			return Any, &TypeError{Msg: "member access on unshaped object"}
		}
		switch prop := node.Property.(type) {
		case *ast.StringNode:
			if fs, ok := obj.Fields[prop.Value]; ok {
				return fs, nil
			}
			return Any, nil
		case *ast.IdentifierNode:
			if fs, ok := obj.Fields[prop.Value]; ok {
				return fs, nil
			}
			return Any, nil
		default:
			return Any, &TypeError{Msg: "unsupported member property type"}
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
					return Any, &TypeError{Msg: "cast: missing shape argument"}
				}
				if shp, ok := shapeLiteralFromAST(node.Arguments[1]); ok {
					if _, err := shapeOf(node.Arguments[0], sym); err != nil {
						return shp, err
					}
					return shp, nil
				}
				return Any, &TypeError{Msg: "cast: second argument must be a shape literal"}
			case "type":
				if len(node.Arguments) < 1 {
					return Any, &TypeError{Msg: "type: missing argument"}
				}
				return shapeOf(node.Arguments[0], sym)
			case "duration":
				return Number, nil
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
	default:
		return Any, nil
	}
}
