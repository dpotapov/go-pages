package chtml

import (
	"sort"
	"strings"
)

// ShapeKind enumerates the supported abstract shapes.
type ShapeKind int

const (
	ShapeAny ShapeKind = iota
	ShapeBool
	ShapeString
	ShapeNumber
	ShapeArray
	ShapeObject
	ShapeHtml
	ShapeHtmlAttr
)

// Shape describes the static shape of a value.
type Shape struct {
	Kind   ShapeKind
	Elem   *Shape            // element shape if Kind == ShapeArray
	Fields map[string]*Shape // field shapes if Kind == ShapeObject
}

// Predefined scalar shapes.
var (
	Any      = &Shape{Kind: ShapeAny}
	Bool     = &Shape{Kind: ShapeBool}
	String   = &Shape{Kind: ShapeString}
	Number   = &Shape{Kind: ShapeNumber}
	Html     = &Shape{Kind: ShapeHtml}
	HtmlAttr = &Shape{Kind: ShapeHtmlAttr}
)

// ArrayOf returns an array shape of elem.
func ArrayOf(elem *Shape) *Shape { return &Shape{Kind: ShapeArray, Elem: elem} }

// Object returns an object shape with given fields.
func Object(fields map[string]*Shape) *Shape { return &Shape{Kind: ShapeObject, Fields: fields} }

// Equal reports whether two shapes are structurally equal.
func (s *Shape) Equal(other *Shape) bool {
	if s == other {
		return true
	}
	if s == nil || other == nil {
		return false
	}
	if s.Kind != other.Kind {
		return false
	}
	switch s.Kind {
	case ShapeArray:
		return s.Elem.Equal(other.Elem)
	case ShapeObject:
		if len(s.Fields) != len(other.Fields) {
			return false
		}
		for k, sv := range s.Fields {
			ov, ok := other.Fields[k]
			if !ok || !sv.Equal(ov) {
				return false
			}
		}
		return true
	default:
		return true
	}
}

// Merge combines two shapes into a single shape, following conservative rules.
// If kinds match, arrays become array of Any, objects merge field shapes recursively.
// If kinds differ, HTML dominates; otherwise Any is returned.
func (s *Shape) Merge(other *Shape) *Shape {
	if s == nil {
		return other
	}
	if other == nil {
		return s
	}
	if s.Kind == other.Kind {
		switch s.Kind {
		case ShapeArray:
			return ArrayOf(Any)
		case ShapeObject:
			if s.Fields == nil {
				return other
			}
			if other.Fields == nil {
				return s
			}
			out := make(map[string]*Shape, len(s.Fields)+len(other.Fields))
			for k, v := range s.Fields {
				out[k] = v
			}
			for k, v := range other.Fields {
				if prev, ok := out[k]; ok {
					out[k] = prev.Merge(v)
				} else {
					out[k] = v
				}
			}
			return Object(out)
		default:
			return s
		}
	}
	if s.Kind == ShapeHtml || other.Kind == ShapeHtml {
		return Html
	}
	return Any
}

// String implements fmt.Stringer for ShapeKind.
func (k ShapeKind) String() string {
	switch k {
	case ShapeAny:
		return "any"
	case ShapeBool:
		return "bool"
	case ShapeString:
		return "string"
	case ShapeNumber:
		return "number"
	case ShapeArray:
		return "array"
	case ShapeObject:
		return "object"
	case ShapeHtml:
		return "html"
	case ShapeHtmlAttr:
		return "html_attr"
	default:
		return "unknown"
	}
}

// String implements fmt.Stringer for Shape.
func (s *Shape) String() string {
	return s.stringWithVisited(make(map[*Shape]bool))
}

func (s *Shape) stringWithVisited(visited map[*Shape]bool) string {
	if s == nil {
		return "nil"
	}
	if visited[s] {
		return "<cycle>"
	}
	visited[s] = true

	switch s.Kind {
	case ShapeArray:
		// Default to any when element shape is unknown.
		elem := "any"
		if s.Elem != nil {
			elem = s.Elem.stringWithVisited(visited)
		}
		return "[" + elem + "]"
	case ShapeObject:
		// Handle map types (Fields=nil, Elem!=nil)
		if s.Elem != nil && s.Fields == nil {
			return "{_:" + s.Elem.stringWithVisited(visited) + "}"
		}

		// Handle regular objects
		if len(s.Fields) == 0 {
			return "{}"
		}
		keys := make([]string, 0, len(s.Fields))
		for k := range s.Fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString("{")
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(k)
			b.WriteByte(':')
			field := s.Fields[k]
			if field == nil {
				b.WriteString("any")
			} else {
				b.WriteString(field.stringWithVisited(visited))
			}
		}
		b.WriteByte('}')
		return b.String()
	default:
		return s.Kind.String()
	}
}
