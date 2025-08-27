package chtml

import "fmt"

type CAttr struct{}

var _ Component = &CAttr{}

func (c *CAttr) Render(s Scope) (any, error) {
	vars := s.Vars()
	if vars == nil {
		return nil, nil
	}

	// TODO: use UnmarshalScope
	name, ok := vars["name"]
	if !ok {
		return nil, fmt.Errorf("attr component requires a name attribute")
	}

	sname, ok := name.(string)
	if !ok {
		return nil, fmt.Errorf("attr component name attribute must be a string")
	}

	return Attribute{
		Namespace: "",
		Key:       sname,
		Val:       NewExprConst(vars["_"]),
	}, nil
}

func (c *CAttr) InputShape() *Shape  { return Object(map[string]*Shape{"name": String, "_": Any}) }
func (c *CAttr) OutputShape() *Shape { return HtmlAttr }
