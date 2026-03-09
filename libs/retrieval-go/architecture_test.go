package retrieval

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestArchitecture_NoConfidenceThresholdLogic ensures the retrieval layer does not
// contain confidence threshold or scoring-decision logic, which belongs in the
// inference layer. The retrieval layer may pass through raw scores as data but
// must never compare them against thresholds to filter or classify candidates.
func TestArchitecture_NoConfidenceThresholdLogic(t *testing.T) {
	// Patterns that indicate threshold/scoring decisions leaked into retrieval.
	// These look for comparisons against confidence values or threshold variables.
	forbidden := []*regexp.Regexp{
		// confidence threshold comparisons: confidence < 0.5, conf > threshold, etc.
		regexp.MustCompile(`(?i)confidence\s*[<>]=?\s*\d`),
		regexp.MustCompile(`(?i)\bmin[_]?confidence\b`),
		regexp.MustCompile(`(?i)\bconfidence[_]?threshold\b`),
		regexp.MustCompile(`(?i)\bthreshold\b.*\bconfidence\b`),
		// calibration functions belong in inference
		regexp.MustCompile(`(?i)\bcalibrate\b`),
		// filtering by confidence score
		regexp.MustCompile(`(?i)if\s+.*\.Confidence\s*[<>]`),
	}

	goFiles, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("failed to glob Go files: %v", err)
	}

	for _, f := range goFiles {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}

		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("failed to read %s: %v", f, err)
		}

		lines := strings.Split(string(data), "\n")
		for lineNum, line := range lines {
			for _, pat := range forbidden {
				if pat.MatchString(line) {
					t.Errorf(
						"%s:%d: retrieval layer must not contain confidence threshold logic (matched %q):\n  %s",
						f, lineNum+1, pat.String(), strings.TrimSpace(line),
					)
				}
			}
		}
	}
}
