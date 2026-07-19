package static

import (
	"testing"
	"time"
)

func TestFmtDur(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{340 * time.Millisecond, "340ms"},
		{0, "0ms"},
		{999 * time.Millisecond, "999ms"},
		{time.Second, "1.0s"},
		{1500 * time.Millisecond, "1.5s"},
		{9700 * time.Millisecond, "9.7s"},
	}
	for _, c := range cases {
		if got := fmtDur(c.d); got != c.want {
			t.Errorf("fmtDur(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestPadRightAndTruncate_RuneAware(t *testing.T) {
	// Multibyte content (→, ·) must be padded/truncated by display columns, not bytes.
	detail := "556 docs → 351 files"
	if got := padRight(detail, 24); len([]rune(got)) != 24 {
		t.Errorf("padRight rune length = %d, want 24", len([]rune(got)))
	}
	// Already at/over width: returned unchanged.
	if got := padRight("abcdef", 3); got != "abcdef" {
		t.Errorf("padRight over-width = %q, want unchanged", got)
	}
	long := "DB=579 Cache=43 Evt=13 Ext=31 GRPC=1 HTTP=3 Outbox=16"
	trunc := truncateRunes(long, 10)
	if r := []rune(trunc); len(r) != 10 || r[len(r)-1] != '…' {
		t.Errorf("truncateRunes = %q (len %d), want 10 runes ending in ellipsis", trunc, len(r))
	}
	if got := truncateRunes("short", 10); got != "short" {
		t.Errorf("truncateRunes under-width = %q, want unchanged", got)
	}
}

func TestDetectorDetail(t *testing.T) {
	if got := detectorDetail(nil); got != "no stats" {
		t.Errorf("detectorDetail(nil) = %q, want %q", got, "no stats")
	}
	s := newIndexStats("account")
	s.written["DBCall"] = 579
	s.written["CacheCall"] = 43
	s.written["EventType"] = 13
	s.written["ExternalCall"] = 31
	s.written["GRPCCall"] = 1
	s.written["HTTPCall"] = 3
	s.written["OutboxCall"] = 16
	want := "DB=579 Cache=43 Evt=13 Ext=31 GRPC=1 HTTP=3 Outbox=16"
	if got := detectorDetail(s); got != want {
		t.Errorf("detectorDetail = %q, want %q", got, want)
	}
}

func TestIndexReport_NilSafe(t *testing.T) {
	// Methods on a nil *indexReport must not panic (hybrid path passes nil).
	var r *indexReport
	r.begin("x")
	r.end("y")
	r.finish()
	// warn on nil still prints (warning must never be swallowed) but must not panic.
	r.warn("boom %d", 1)
}
