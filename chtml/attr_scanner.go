package chtml

// scanAttributeSpans scans the raw start tag token to find attribute value positions
// Returns a map of attribute key to value span information
func scanAttributeSpans(raw []byte, baseOffset int, attrs []string) map[string]Span {
	result := make(map[string]Span, len(attrs))

	// Skip past the tag name and any whitespace
	pos := 0

	// Skip '<' and tag name
	if pos < len(raw) && raw[pos] == '<' {
		pos++
	}
	// Skip tag name
	for pos < len(raw) && !isAttrSpace(raw[pos]) && raw[pos] != '>' && raw[pos] != '/' {
		pos++
	}

	// Process each attribute in order
	attrIndex := 0
	for pos < len(raw) && attrIndex < len(attrs) {
		// Skip whitespace
		for pos < len(raw) && isAttrSpace(raw[pos]) {
			pos++
		}

		if pos >= len(raw) || raw[pos] == '>' || raw[pos] == '/' {
			break
		}

		// Find attribute name end
		for pos < len(raw) && raw[pos] != '=' && !isAttrSpace(raw[pos]) && raw[pos] != '>' && raw[pos] != '/' {
			pos++
		}

		// Skip any whitespace before '='
		for pos < len(raw) && isAttrSpace(raw[pos]) {
			pos++
		}

		// Check for '='
		if pos >= len(raw) || raw[pos] != '=' {
			// Attribute without value
			attrIndex++
			continue
		}
		pos++ // skip '='

		// Skip any whitespace after '='
		for pos < len(raw) && isAttrSpace(raw[pos]) {
			pos++
		}

		if pos >= len(raw) {
			break
		}

		// Check for quoted value
		valueStart := pos
		var valueEnd int

		if raw[pos] == '"' || raw[pos] == '\'' {
			quote := raw[pos]
			pos++ // skip opening quote
			valueStart = pos

			// Find closing quote
			for pos < len(raw) && raw[pos] != quote {
				if raw[pos] == '\\' && pos+1 < len(raw) {
					pos += 2 // Skip escaped character
				} else {
					pos++
				}
			}
			valueEnd = pos
			if pos < len(raw) {
				pos++ // skip closing quote
			}
		} else {
			// Unquoted value
			for pos < len(raw) && !isAttrSpace(raw[pos]) && raw[pos] != '>' && raw[pos] != '/' {
				pos++
			}
			valueEnd = pos
		}

		// Store the span for this attribute value
		if attrIndex < len(attrs) {
			result[attrs[attrIndex]] = Span{
				Offset: baseOffset + valueStart, // Absolute file offset
				Start:  valueStart,              // Position within token (for line/col calc)
				Length: valueEnd - valueStart,
			}
		}
		attrIndex++
	}

	return result
}

func isAttrSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f'
}
