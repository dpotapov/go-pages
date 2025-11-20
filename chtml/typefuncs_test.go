package chtml

import (
	"testing"
	"time"
)

func TestFormatDurationFunction(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected string
		wantErr  bool
	}{
		{
			name:     "nanoseconds for 7h 15m 34s",
			input:    int64(7*time.Hour + 15*time.Minute + 34*time.Second),
			expected: "7h 15m 34s",
			wantErr:  false,
		},
		{
			name:     "nanoseconds for 1h 30m",
			input:    int64(1*time.Hour + 30*time.Minute),
			expected: "1h 30m",
			wantErr:  false,
		},
		{
			name:     "nanoseconds for 30 minutes",
			input:    int64(30 * time.Minute),
			expected: "30m",
			wantErr:  false,
		},
		{
			name:     "zero nanoseconds",
			input:    int64(0),
			expected: "0s",
			wantErr:  false,
		},
		{
			name:     "nanoseconds for 45 seconds",
			input:    int64(45 * time.Second),
			expected: "45s",
			wantErr:  false,
		},
		{
			name:     "hours converted to nanoseconds (7.259445495555555)",
			input:    int64(7.259445495555555 * float64(time.Hour)),
			expected: "7h 15m 34s",
			wantErr:  false,
		},
		{
			name:     "int type nanoseconds",
			input:    int(7*time.Hour + 15*time.Minute + 34*time.Second),
			expected: "7h 15m 34s",
			wantErr:  false,
		},
		{
			name:     "float64 nanoseconds (truncated)",
			input:    float64(7*time.Hour + 15*time.Minute + 34*time.Second),
			expected: "7h 15m 34s",
			wantErr:  false,
		},
		{
			name:     "invalid type",
			input:    "not a number",
			expected: "",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := FormatDurationFunction(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("FormatDurationFunction() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil && tt.wantErr {
				return
			}
			if result != tt.expected {
				t.Errorf("FormatDurationFunction() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		expected string
	}{
		{
			name:     "7h 15m 34s",
			duration: 7*time.Hour + 15*time.Minute + 34*time.Second,
			expected: "7h 15m 34s",
		},
		{
			name:     "1h 30m",
			duration: 1*time.Hour + 30*time.Minute,
			expected: "1h 30m",
		},
		{
			name:     "30 minutes",
			duration: 30 * time.Minute,
			expected: "30m",
		},
		{
			name:     "zero duration",
			duration: 0,
			expected: "0s",
		},
		{
			name:     "1 minute",
			duration: 1 * time.Minute,
			expected: "1m",
		},
		{
			name:     "45 seconds",
			duration: 45 * time.Second,
			expected: "45s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatDuration(tt.duration)
			if result != tt.expected {
				t.Errorf("formatDuration() = %v, want %v", result, tt.expected)
			}
		})
	}
}
