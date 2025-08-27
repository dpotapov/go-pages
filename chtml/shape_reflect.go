package chtml

import (
	"reflect"
	"strings"
)

// ShapeOf constructs a Shape from the static type parameter T using reflection.
func ShapeOf[T any]() *Shape {
	var zero T
	return shapeFromValue(reflect.ValueOf(zero))
}

// ShapeFrom infers a Shape from a runtime value (using its dynamic type).
func ShapeFrom(v any) *Shape {
	if v == nil {
		return nil
	}
	// If caller passed a *Shape, return it as-is.
	if s, ok := v.(*Shape); ok {
		return s
	}
	return shapeFromValue(reflect.ValueOf(v))
}

func shapeFromValue(rv reflect.Value) *Shape {
	if !rv.IsValid() {
		return Any
	}
	return shapeFromType(rv.Type(), make(map[reflect.Type]*Shape))
}

func shapeFromType(rt reflect.Type, seen map[reflect.Type]*Shape) *Shape {
	// Resolve pointers
	for rt.Kind() == reflect.Pointer {
		rt = rt.Elem()
	}

	if s, ok := seen[rt]; ok {
		return s
	}

	switch rt.Kind() {
	case reflect.Bool:
		return Bool
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return Number
	case reflect.String:
		return String
	case reflect.Interface:
		return Any
	case reflect.Slice, reflect.Array:
		return ArrayOf(shapeFromType(rt.Elem(), seen))
	case reflect.Map:
		// Dynamic key/value bag; return unshaped object
		return Object(nil)
	case reflect.Struct:
		// Special cases for common stdlib types
		if rt.PkgPath() == "time" && rt.Name() == "Time" {
			return Number
		}
		// Build object shape from exported fields
		obj := make(map[string]*Shape)
		res := Object(obj)
		seen[rt] = res
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			if f.PkgPath != "" { // unexported
				continue
			}
			name := fieldName(f)
			if name == "-" || name == "" {
				continue
			}
			obj[name] = shapeFromType(f.Type, seen)
		}
		return res
	default:
		return Any
	}
}

func fieldName(f reflect.StructField) string {
	// Prefer expr tag, then json, else snake_case of field name
	if v := f.Tag.Get("expr"); v != "" {
		return v
	}
	if v := f.Tag.Get("json"); v != "" {
		// json tag may include options (e.g., ",omitempty")
		if idx := strings.IndexByte(v, ','); idx >= 0 {
			v = v[:idx]
		}
		return v
	}
	return toSnakeCase(f.Name)
}
