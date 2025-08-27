package chtml

import (
	"fmt"
	"time"
)

// CastFunction implements cast(v, shape) at runtime.
// For v1 it is a no-op pass-through (static checking enforces shapes).
func CastFunction(args ...any) (any, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("cast expects 2 args (value, shape)")
	}
	// Return the value unchanged; conversions (if any) are handled by renderer paths.
	return args[0], nil
}

// TypeFunction returns a runtime description of the value's shape.
// For v1, return a coarse placeholder; future versions can return a structured value.
func TypeFunction(args ...any) (any, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("type expects 1 arg")
	}
	// Minimal implementation: return AnyShape marker used by renderer/parse.
	return AnyShape, nil
}

// DurationFunction parses a duration string (e.g., "3s", "150ms") and returns
// its numeric value in nanoseconds as an int64. Also accepts time.Duration.
func DurationFunction(args ...any) (any, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("duration expects 1 arg")
	}
	switch v := args[0].(type) {
	case string:
		if v == "" {
			return int64(0), nil
		}
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("parse duration: %w", err)
		}
		return int64(d), nil
	case time.Duration:
		return int64(v), nil
	default:
		return nil, fmt.Errorf("duration expects string or time.Duration, got %T", v)
	}
}
