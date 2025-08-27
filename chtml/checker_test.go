package chtml

import (
	"testing"

	"github.com/expr-lang/expr/ast"
	"github.com/stretchr/testify/require"
)

func Test_shapeLiteralFromAST(t *testing.T) {
	t.Run("identifiers", func(t *testing.T) {
		cases := []struct {
			n  string
			id string
			sh *Shape
		}{
			{"any", "any", Any},
			{"bool", "bool", Bool},
			{"string", "string", String},
			{"number", "number", Number},
			{"html", "html", Html},
		}
		for _, tc := range cases {
			sh, ok := shapeLiteralFromAST(&ast.IdentifierNode{Value: tc.id})
			require.True(t, ok, tc.n)
			require.True(t, sh.Equal(tc.sh), tc.n)
		}
		// Unknown identifier → not a shape literal
		_, ok := shapeLiteralFromAST(&ast.IdentifierNode{Value: "foo"})
		require.False(t, ok)
	})

	t.Run("array", func(t *testing.T) {
		// Valid one-element array shape
		sh, ok := shapeLiteralFromAST(&ast.ArrayNode{Nodes: []ast.Node{
			&ast.IdentifierNode{Value: "string"},
		}})
		require.True(t, ok)
		require.True(t, sh.Equal(ArrayOf(String)))

		// Invalid: empty
		_, ok = shapeLiteralFromAST(&ast.ArrayNode{Nodes: nil})
		require.False(t, ok)

		// Invalid: multiple elements
		_, ok = shapeLiteralFromAST(&ast.ArrayNode{Nodes: []ast.Node{
			&ast.IdentifierNode{Value: "string"},
			&ast.IdentifierNode{Value: "number"},
		}})
		require.False(t, ok)
	})

	t.Run("map", func(t *testing.T) {
		// Valid map with string and identifier keys
		sh, ok := shapeLiteralFromAST(&ast.MapNode{Pairs: []ast.Node{
			&ast.PairNode{Key: &ast.StringNode{Value: "a"}, Value: &ast.IdentifierNode{Value: "number"}},
			&ast.PairNode{Key: &ast.IdentifierNode{Value: "b"}, Value: &ast.IdentifierNode{Value: "string"}},
		}})
		require.True(t, ok)
		require.True(t, sh.Equal(Object(map[string]*Shape{
			"a": Number,
			"b": String,
		})))

		// Empty map is valid shape literal for empty object
		sh, ok = shapeLiteralFromAST(&ast.MapNode{Pairs: nil})
		require.True(t, ok)
		require.True(t, sh.Equal(Object(nil)))

		// Invalid key type
		_, ok = shapeLiteralFromAST(&ast.MapNode{Pairs: []ast.Node{
			&ast.PairNode{Key: &ast.IntegerNode{Value: 1}, Value: &ast.IdentifierNode{Value: "string"}},
		}})
		require.False(t, ok)
	})
}

func Test_shapeOf_PrimitivesAndIdentifiers(t *testing.T) {
	// Primitives
	shp, err := shapeOf(&ast.IntegerNode{Value: 1}, nil)
	require.NoError(t, err)
	require.True(t, shp.Equal(Number))
	shp, err = shapeOf(&ast.FloatNode{Value: 1.2}, nil)
	require.NoError(t, err)
	require.True(t, shp.Equal(Number))
	shp, err = shapeOf(&ast.BoolNode{Value: true}, nil)
	require.NoError(t, err)
	require.True(t, shp.Equal(Bool))
	shp, err = shapeOf(&ast.StringNode{Value: "x"}, nil)
	require.NoError(t, err)
	require.True(t, shp.Equal(String))
	shp, err = shapeOf(&ast.NilNode{}, nil)
	require.NoError(t, err)
	require.True(t, shp.Equal(Any))
	shp, err = shapeOf(&ast.ConstantNode{Value: 123}, nil)
	require.NoError(t, err)
	require.True(t, shp.Equal(Number))
	shp, err = shapeOf(&ast.ConstantNode{Value: "foo"}, nil)
	require.NoError(t, err)
	require.True(t, shp.Equal(String))

	// Identifiers
	syms := Symbols{"s": String}
	shp, err = shapeOf(&ast.IdentifierNode{Value: "s"}, syms)
	require.NoError(t, err)
	require.True(t, shp.Equal(String))
	// Undeclared identifier now returns Any without error
	shp, err = shapeOf(&ast.IdentifierNode{Value: "u"}, syms)
	require.NoError(t, err)
	require.True(t, shp.Equal(Any))
}

func Test_shapeOf_Member_Array_Map(t *testing.T) {
	// Member access on a known object
	obj := Object(map[string]*Shape{
		"a": Number,
		"b": ArrayOf(String),
	})
	syms := Symbols{"obj": obj}
	got, err := shapeOf(&ast.MemberNode{
		Node:     &ast.IdentifierNode{Value: "obj"},
		Property: &ast.IdentifierNode{Value: "b"},
	}, syms)
	require.NoError(t, err)
	require.True(t, got.Equal(ArrayOf(String)))
	// Missing field → Any without error
	got, err = shapeOf(&ast.MemberNode{
		Node:     &ast.IdentifierNode{Value: "obj"},
		Property: &ast.IdentifierNode{Value: "zzz"},
	}, syms)
	require.NoError(t, err)
	require.True(t, got.Equal(Any))

	// Array inference
	got, err = shapeOf(&ast.ArrayNode{Nodes: []ast.Node{
		&ast.StringNode{Value: "a"},
		&ast.StringNode{Value: "b"},
	}}, nil)
	require.NoError(t, err)
	require.True(t, got.Equal(ArrayOf(String)))
	got, err = shapeOf(&ast.ArrayNode{Nodes: []ast.Node{
		&ast.StringNode{Value: "a"},
		&ast.IntegerNode{Value: 2},
	}}, nil)
	require.NoError(t, err)
	require.True(t, got.Equal(ArrayOf(Any)))
	got, err = shapeOf(&ast.ArrayNode{Nodes: nil}, nil)
	require.NoError(t, err)
	require.True(t, got.Equal(ArrayOf(Any)))

	// Map inference
	got, err = shapeOf(&ast.MapNode{Pairs: []ast.Node{
		&ast.PairNode{Key: &ast.StringNode{Value: "n"}, Value: &ast.IntegerNode{Value: 1}},
		&ast.PairNode{Key: &ast.IdentifierNode{Value: "s"}, Value: &ast.StringNode{Value: "x"}},
	}}, nil)
	require.NoError(t, err)
	require.True(t, got.Equal(Object(map[string]*Shape{
		"n": Number,
		"s": String,
	})))
}

func Test_shapeOf_ConditionalAndCalls(t *testing.T) {
	// Conditional: same branches
	got, err := shapeOf(&ast.ConditionalNode{Exp1: &ast.StringNode{Value: "a"}, Exp2: &ast.StringNode{Value: "b"}}, nil)
	require.NoError(t, err)
	require.True(t, got.Equal(String))
	// Conditional: html dominates
	got, err = shapeOf(&ast.ConditionalNode{Exp1: &ast.StringNode{Value: "a"}, Exp2: &ast.ConstantNode{Value: Html}}, nil)
	require.NoError(t, err)
	require.True(t, got.Equal(Html))
	// Conditional: different non-html → Any
	got, err = shapeOf(&ast.ConditionalNode{Exp1: &ast.IntegerNode{Value: 1}, Exp2: &ast.StringNode{Value: "b"}}, nil)
	require.NoError(t, err)
	require.True(t, got.Equal(Any))

	// Calls
	// cast(x, string) → string
	got, err = shapeOf(&ast.CallNode{
		Callee: &ast.IdentifierNode{Value: "cast"},
		Arguments: []ast.Node{
			&ast.IdentifierNode{Value: "x"},
			&ast.IdentifierNode{Value: "string"},
		},
	}, nil)
	require.NoError(t, err)
	require.True(t, got.Equal(String))
	// type("a") → string
	got, err = shapeOf(&ast.CallNode{
		Callee:    &ast.IdentifierNode{Value: "type"},
		Arguments: []ast.Node{&ast.StringNode{Value: "a"}},
	}, nil)
	require.NoError(t, err)
	require.True(t, got.Equal(String))
	// duration("1s") → number
	got, err = shapeOf(&ast.CallNode{
		Callee:    &ast.IdentifierNode{Value: "duration"},
		Arguments: []ast.Node{&ast.StringNode{Value: "1s"}},
	}, nil)
	require.NoError(t, err)
	require.True(t, got.Equal(Number))
	// combine("a", "b") → string; combine("a", 1) → any
	got, err = shapeOf(&ast.CallNode{
		Callee: &ast.IdentifierNode{Value: "combine"},
		Arguments: []ast.Node{
			&ast.StringNode{Value: "a"},
			&ast.StringNode{Value: "b"},
		},
	}, nil)
	require.NoError(t, err)
	require.True(t, got.Equal(String))
	got, err = shapeOf(&ast.CallNode{
		Callee: &ast.IdentifierNode{Value: "combine"},
		Arguments: []ast.Node{
			&ast.StringNode{Value: "a"},
			&ast.IntegerNode{Value: 1},
		},
	}, nil)
	require.NoError(t, err)
	require.True(t, got.Equal(Any))
}

func Test_Check(t *testing.T) {
	// Nil root
	sh, err := Check(nil, nil)
	require.NoError(t, err)
	require.True(t, sh.Equal(Any))

	// Direct AST
	syms := Symbols{"obj": Object(map[string]*Shape{"name": String})}
	sh, err = Check(&ast.MemberNode{Node: &ast.IdentifierNode{Value: "obj"}, Property: &ast.IdentifierNode{Value: "name"}}, syms)
	require.NoError(t, err)
	require.True(t, sh.Equal(String))
}
