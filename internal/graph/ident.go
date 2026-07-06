package neo4j

import (
	"strings"
)

// Ident sanitizes a string for safe interpolation into Cypher as a label or
// relationship type. Plain identifiers ([A-Za-z_][A-Za-z0-9_]*) pass through;
// anything else is backtick-quoted with embedded backticks doubled.
func Ident(s string) string {
	if s == "" {
		return "``"
	}

	// Check if it's a plain identifier
	isPlain := true
	for i, r := range s {
		if i == 0 {
			// First character must be letter or underscore
			if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r == '_') {
				isPlain = false
				break
			}
		} else {
			// Subsequent characters must be letter, digit, or underscore
			if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
				isPlain = false
				break
			}
		}
	}

	if isPlain {
		return s
	}

	// Quote with backticks and double any embedded backticks
	escaped := strings.ReplaceAll(s, "`", "``")
	return "`" + escaped + "`"
}
