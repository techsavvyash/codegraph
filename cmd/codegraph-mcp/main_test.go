package main

import (
	"os"
	"testing"

	"github.com/context-maximiser/code-graph/internal/testutil/dblock"
)

// TestMain holds the cross-package database lock for the whole run so the
// handler tests never interleave with test/harness or test/integration on
// the shared Neo4j instance (see internal/testutil/dblock).
func TestMain(m *testing.M) {
	release := dblock.Acquire()
	code := m.Run()
	release()
	os.Exit(code)
}

func TestParseTimeoutMs(t *testing.T) {
	tests := []struct {
		name     string
		args     map[string]interface{}
		expected int
	}{
		{
			name:     "missing timeout_ms defaults to 10000",
			args:     map[string]interface{}{},
			expected: 10000,
		},
		{
			name:     "nil args defaults to 10000",
			args:     nil,
			expected: 10000,
		},
		{
			name:     "timeout_ms below minimum clamped to 100",
			args:     map[string]interface{}{"timeout_ms": float64(50)},
			expected: 100,
		},
		{
			name:     "timeout_ms at minimum boundary",
			args:     map[string]interface{}{"timeout_ms": float64(100)},
			expected: 100,
		},
		{
			name:     "timeout_ms in valid range",
			args:     map[string]interface{}{"timeout_ms": float64(5000)},
			expected: 5000,
		},
		{
			name:     "timeout_ms at maximum boundary",
			args:     map[string]interface{}{"timeout_ms": float64(120000)},
			expected: 120000,
		},
		{
			name:     "timeout_ms above maximum clamped to 120000",
			args:     map[string]interface{}{"timeout_ms": float64(500000)},
			expected: 120000,
		},
		{
			name:     "timeout_ms zero clamped to 100",
			args:     map[string]interface{}{"timeout_ms": float64(0)},
			expected: 100,
		},
		{
			name:     "timeout_ms as int (edge case)",
			args:     map[string]interface{}{"timeout_ms": float64(3000)},
			expected: 3000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseTimeoutMs(tt.args)
			if result != tt.expected {
				t.Errorf("parseTimeoutMs(%v) = %d, expected %d", tt.args, result, tt.expected)
			}
		})
	}
}
