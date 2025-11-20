package chtml

import (
	"fmt"
	"strings"
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

// FormatDurationFunction formats nanoseconds into a human-readable duration string.
// For example, 26134000000000 nanoseconds (7.259445495555555 hours) becomes "7h 15m 34s".
// It accepts int64 (nanoseconds) or any integer type (treated as nanoseconds).
// For hours, multiply by 3600000000000 (nanoseconds per hour):
//
//	formatDuration(int(item.total_hours * 3600000000000))
func FormatDurationFunction(args ...any) (any, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("formatDuration expects 1 arg")
	}

	var d time.Duration
	switch v := args[0].(type) {
	case int64:
		d = time.Duration(v)
	case int:
		d = time.Duration(v)
	case int32:
		d = time.Duration(v)
	case int16:
		d = time.Duration(v)
	case int8:
		d = time.Duration(v)
	case uint64:
		d = time.Duration(v)
	case uint:
		d = time.Duration(v)
	case uint32:
		d = time.Duration(v)
	case uint16:
		d = time.Duration(v)
	case uint8:
		d = time.Duration(v)
	case float64:
		// Accept float64 but treat as nanoseconds (for convenience when multiplying)
		d = time.Duration(int64(v))
	case float32:
		d = time.Duration(int64(v))
	default:
		return nil, fmt.Errorf("formatDuration expects number (nanoseconds), got %T", v)
	}

	return formatDuration(d), nil
}

// formatDuration formats a time.Duration into a human-readable string with spaces.
// Example: "7h 15m 34s" instead of "7h15m34s"
func formatDuration(d time.Duration) string {
	if d == 0 {
		return "0s"
	}

	var parts []string

	hours := int64(d.Hours())
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
		d -= time.Duration(hours) * time.Hour
	}

	minutes := int64(d.Minutes())
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
		d -= time.Duration(minutes) * time.Minute
	}

	seconds := int64(d.Seconds())
	if seconds > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%ds", seconds))
	}

	return strings.Join(parts, " ")
}
