package chtml

// Span represents a source location in a file
type Span struct {
	Offset int // Byte offset in the file
	Line   int // 1-based line number
	Column int // 1-based column number (in runes, not bytes)
	Length int // Length in bytes
	Start  int // Position within token (for internal calculations)
}

// Source represents a source location with optional file information
type Source struct {
	File string // File path (can be empty)
	Span Span   // Location within the file
}

// IsZero returns true if the span is uninitialized
func (s Span) IsZero() bool {
	return s.Offset == 0 && s.Line == 0 && s.Column == 0 && s.Length == 0
}

// End returns the end offset of the span
func (s Span) End() int {
	return s.Offset + s.Length
}