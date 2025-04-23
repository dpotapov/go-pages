package pages

import (
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
)

// DecodeForm parses url.Values into a nested map[string]any structure.
// It handles dot notation for nested objects (e.g., "project.id") and
// bracket notation for arrays (e.g., "apps[0].name").
// Errors during parsing are logged if a logger is provided.
func DecodeForm(values url.Values, logger *slog.Logger) map[string]any {
	result := make(map[string]any)

	for key, vals := range values {
		if len(vals) == 0 {
			continue
		}
		// We typically only care about the first value for form decoding
		value := vals[0]
		if err := assignValue(result, key, value); err != nil {
			if logger != nil {
				// Log the error with context about the key/value pair
				logger.Warn("Failed to assign form value",
					slog.String("key", key),
					slog.Any("value", value),
					slog.Any("error", err),
				)
			}
			// Continue processing other keys even if one fails
		}
	}

	return result
}

func assignValue(data map[string]any, path string, value any) error {
	parts := strings.Split(path, ".")
	current := any(data)

	for i, part := range parts {
		isArray, key, index := parsePart(part)
		isLastPart := i == len(parts)-1

		switch c := current.(type) {
		case map[string]any:
			if isArray {
				// Need to create/access an array within the map
				if _, ok := c[key]; !ok {
					c[key] = make([]any, index+1) // Initialize slice large enough
				}
				slice, ok := c[key].([]any)
				if !ok {
					return fmt.Errorf("expected slice at key '%s', found %T", key, c[key])
				}
				// Ensure slice is large enough
				if index >= len(slice) {
					newSlice := make([]any, index+1)
					copy(newSlice, slice)
					slice = newSlice
					c[key] = slice
				}

				if isLastPart {
					slice[index] = value
				} else {
					// Ensure the element at index is a map for further nesting
					if slice[index] == nil {
						slice[index] = make(map[string]any)
					}
					nestedMap, ok := slice[index].(map[string]any)
					if !ok {
						return fmt.Errorf("expected map at index %d of key '%s', found %T", index, key, slice[index])
					}
					current = nestedMap
				}
			} else {
				// Access/create a map key
				if isLastPart {
					c[key] = value
				} else {
					if _, ok := c[key]; !ok {
						c[key] = make(map[string]any)
					}
					nestedMap, ok := c[key].(map[string]any)
					if !ok {
						return fmt.Errorf("expected map at key '%s', found %T", key, c[key])
					}
					current = nestedMap
				}
			}
		case []any:
			// This case should ideally not be hit directly if the structure is built correctly
			// from the root map, but added for robustness. It implies trying to access
			// a slice element without a preceding map key.
			return fmt.Errorf("attempted to access slice directly with part '%s'", part)
		default:
			// Error: trying to navigate into a non-container type (e.g., string, int)
			return fmt.Errorf("cannot navigate into type %T with part '%s'", current, part)
		}
	}
	return nil // Success
}

// parsePart extracts the key, index (if applicable), and determines if it's an array part.
// It only considers a part an array access if it strictly matches the pattern `key[index]`,
// where `index` is a non-negative integer and `]` is the last character.
// Otherwise, the entire part is treated as a simple key.
// Example: "apps[0]" -> true, "apps", 0
// Example: "project" -> false, "project", -1
// Example: "apps[0].name" -> false, "apps[0].name", -1 (when called on this part specifically)
// Example: "key[abc]" -> false, "key[abc]", -1
// Example: "key[]" -> false, "key[]", -1
func parsePart(part string) (isArray bool, key string, index int) {
	bracketStart := strings.Index(part, "[")
	if bracketStart == -1 {
		return false, part, -1 // No opening bracket, definitely a simple key
	}

	bracketEnd := strings.Index(part, "]")
	// Ensure ']' exists and is the *last* character of the part.
	// Also ensure brackets are not empty (e.g., "key[]").
	if bracketEnd != len(part)-1 || bracketEnd == bracketStart+1 {
		// Malformed: ']' not last, or empty brackets '[]'. Treat as simple key.
		return false, part, -1
	}

	indexStr := part[bracketStart+1 : bracketEnd]
	idx, err := strconv.Atoi(indexStr)
	if err != nil || idx < 0 {
		// Invalid index (non-integer or negative). Treat as simple key.
		return false, part, -1
	}

	// Valid array part found
	key = part[:bracketStart]
	return true, key, idx
}
