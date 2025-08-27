package chtml

import (
	"testing"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// compileExpr compiles an expr string to an AST node with the same options
// used by NewExpr/NewExprInterpol for fairness.
func compileExpr(t *testing.T, s string) ast.Node {
	t.Helper()
	prog, err := expr.Compile(s,
		expr.DisableBuiltin("type"),
		expr.DisableBuiltin("duration"),
		expr.Function("cast", CastFunction),
		expr.Function("type", TypeFunction),
		expr.Function("duration", DurationFunction),
	)
	require.NoError(t, err)
	return prog.Node()
}

func TestCheck_Expressions_Table(t *testing.T) {
	tests := []struct {
		name    string
		expr    string
		syms    Symbols
		want    *Shape
		wantErr string
	}{
		// Literals
		{"true is bool", "true", nil, Bool, ""},
		{"string literal", `"x"`, nil, String, ""},
		{"number literal", "42", nil, Number, ""},

		// Identifiers
		{"known identifier", "x", Symbols{"x": String}, String, ""},
		{"unknown identifier is Any", "y", Symbols{}, Any, ""},

		// Arrays and maps
		{"array infers elem merge", `["a", 1]`, nil, ArrayOf(Any), ""},
		{"empty array Any elem", `[]`, nil, ArrayOf(Any), ""},
		{"map mixed keys result object", `{a: 1, b: "x"}`, nil, Object(map[string]*Shape{"a": Number, "b": String}), ""},
		{"empty map object", `{}`, nil, Object(map[string]*Shape{}), ""},

		// Member access
		{"member field ok", `obj.name`, Symbols{"obj": Object(map[string]*Shape{"name": String})}, String, ""},
		{"member missing field Any", `obj.missing`, Symbols{"obj": Object(map[string]*Shape{"name": String})}, Any, ""},
		{"member on non-object error", `x.bar`, Symbols{"x": Number}, Any, "member access on non-object"},
		{"member on any error", `a.bar`, Symbols{"a": Any}, Any, "member access on non-object"},
		// When base is undefined, treat as Any (but still non-object for member access)
		{"member base undefined is Any", `foo.bar`, Symbols{}, Any, "member access on non-object"},

		// Conditional
		{"conditional same", `true ? "a" : "b"`, nil, String, ""},
		{"conditional different Any", `true ? 1 : "b"`, nil, Any, ""},

		// Calls handled specially by checker
		{"cast to string", `cast(x, string)`, Symbols{"x": Any}, String, ""},
		{"cast wrong literal error", `cast(x, y)`, Symbols{"x": Any, "y": String}, Any, "shape literal"},
		{"type returns arg shape", `type("a")`, nil, String, ""},
		{"duration returns number", `duration("1s")`, nil, Number, ""},
		{"combine strings string", `combine("a", "b")`, nil, String, ""},
		{"combine mixed Any", `combine("a", 1)`, nil, Any, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := compileExpr(t, tt.expr)
			got, err := Check(n, tt.syms)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			assert.True(t, got.Equal(tt.want), "got %v want %v", got, tt.want)
		})
	}
}
