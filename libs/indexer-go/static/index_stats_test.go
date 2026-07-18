package static

import (
	"strings"
	"testing"
)

// TestIndexStats_CoverageWarning verifies the P3-1 flagship alarm: a wrapper class imported
// by ≥1 file but producing 0 nodes yields a WARN, while a class that produced nodes does not.
func TestIndexStats_CoverageWarning(t *testing.T) {
	s := newIndexStats("payin")

	// Simulate two files importing apiclient and one importing the cache wrapper.
	s.recordFileImports(map[string]string{"apiclient": apiClientImportPath})
	s.recordFileImports(map[string]string{"httpclient": apiClientImportPath})
	s.recordFileImports(map[string]string{"cache": cacheImportPath})

	// Cache produced a node; apiclient produced none (the P0-1 shape).
	s.written = map[string]int{"CacheCall": 3, "HTTPCall": 0}

	warns := s.coverageWarnings()
	if len(warns) != 1 {
		t.Fatalf("coverageWarnings() = %d warnings, want 1\n%v", len(warns), warns)
	}
	if !strings.Contains(warns[0], "apiclient") || !strings.Contains(warns[0], "HTTPCall") {
		t.Errorf("warning should name apiclient→HTTPCall, got %q", warns[0])
	}
	if !strings.Contains(warns[0], "2 file(s)") {
		t.Errorf("warning should report 2 importing files, got %q", warns[0])
	}
}

// TestIndexStats_NoWarningWhenWritten confirms no false alarm when every imported class
// produced at least one node, and none when nothing was imported.
func TestIndexStats_NoWarningWhenWritten(t *testing.T) {
	s := newIndexStats("settlement")
	s.recordFileImports(map[string]string{"apiclient": apiClientImportPath})
	s.recordFileImports(map[string]string{"q": queueClientPkgPath})
	s.written = map[string]int{"HTTPCall": 1, "OutboxCall": 4}

	if warns := s.coverageWarnings(); len(warns) != 0 {
		t.Errorf("coverageWarnings() = %v, want none (both classes wrote nodes)", warns)
	}

	empty := newIndexStats("empty")
	if warns := empty.coverageWarnings(); len(warns) != 0 {
		t.Errorf("coverageWarnings() = %v, want none (nothing imported)", warns)
	}
}

// TestIndexStats_ExternalWrapperImport confirms an external wrapper import counts once per
// file (even with several external imports) and triggers the alarm when no node is written.
func TestIndexStats_ExternalWrapperImport(t *testing.T) {
	s := newIndexStats("account")
	// One file importing two different external wrappers should count once.
	s.recordFileImports(map[string]string{
		"kms": "github.com/tazapay/grpc-framework/client/kms",
		"sms": "github.com/tazapay/grpc-framework/client/sms",
	})
	if got := s.importFiles["external wrappers"]; got != 1 {
		t.Fatalf("external wrappers import count = %d, want 1 (once per file)", got)
	}
	s.written = map[string]int{"ExternalCall": 0}
	warns := s.coverageWarnings()
	if len(warns) != 1 || !strings.Contains(warns[0], "external wrappers") {
		t.Errorf("want one external-wrappers warning, got %v", warns)
	}
}

// TestIndexStats_CaptureWritten confirms buffer node counts are snapshotted correctly.
func TestIndexStats_CaptureWritten(t *testing.T) {
	b := newCallNodeBuffer("scope-1")
	b.addHTTPCall("h1", map[string]any{"x": 1})
	b.addHTTPCall("h2", map[string]any{"x": 2})
	b.addDBCall("d1", map[string]any{"x": 1})

	s := newIndexStats("svc")
	s.captureWritten(b)

	if s.written["HTTPCall"] != 2 {
		t.Errorf("HTTPCall written = %d, want 2", s.written["HTTPCall"])
	}
	if s.written["DBCall"] != 1 {
		t.Errorf("DBCall written = %d, want 1", s.written["DBCall"])
	}
	if s.written["OutboxCall"] != 0 {
		t.Errorf("OutboxCall written = %d, want 0", s.written["OutboxCall"])
	}
}
