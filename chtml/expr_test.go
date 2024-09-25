package chtml

import (
	"reflect"
	"testing"

	"github.com/expr-lang/expr/vm"
)

func TestInterpol(t *testing.T) {
	args := map[string]any{
		"foo": "bar",
	}
	tests := []struct {
		name    string
		s       string
		want    string
		wantErr bool
	}{
		{"empty", "", "", false},
		{"no_interpol", "foo", "foo", false},
		{"interpol1", "${foo}", "bar", false},
		{"interpol2", "${foo}bar", "barbar", false},
		{"interpol3", "foo${foo}", "foobar", false},
		{"interpol4", "foo${foo}bar", "foobarbar", false},
		{"interpol5", "foo${foo}bar${foo}", "foobarbarbar", false},
		{"interpol6", "foo${foo}bar${foo}baz", "foobarbarbarbaz", false},
		{"interpol7", "${foo", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prog, err := interpol(tt.s, args)
			if (err != nil) != tt.wantErr {
				t.Errorf("Interpol() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil && tt.wantErr {
				return
			}
			res, err := vm.Run(prog, args)
			if err != nil {
				t.Errorf("Interpol() error = %v", err)
				return
			}
			got := ""
			if res != nil {
				got = res.(string)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Interpol() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_parseLoopExpr(t *testing.T) {
	tests := []struct {
		name     string
		s        string
		wantV    string
		wantK    string
		wantExpr string
		wantErr  bool
	}{
		{"empty", "", "", "", "", true},
		{"basic", "x in y", "x", "", "y", false},
		{"kv", "x, idx in y", "x", "idx", "y", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotV, gotK, gotExpr, err := parseLoopExpr(tt.s)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseLoopExpr() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if gotV != tt.wantV {
				t.Errorf("parseLoopExpr() gotV = %v, want %v", gotV, tt.wantV)
			}
			if gotK != tt.wantK {
				t.Errorf("parseLoopExpr() gotK = %v, want %v", gotK, tt.wantK)
			}
			if gotExpr != tt.wantExpr {
				t.Errorf("parseLoopExpr() gotExpr = %v, want %v", gotExpr, tt.wantExpr)
			}
		})
	}
}
