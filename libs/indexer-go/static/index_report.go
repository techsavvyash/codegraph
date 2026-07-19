package static

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// indexVerbose gates the deep, line-by-line indexing diagnostics (the SCIP debug
// dump, per-import DEPENDS_ON resolution, per-batch node merges, detector
// internals). It is process-scoped: set once at the start of IndexProject from
// the CLI --verbose flag. Parallel index runs live in separate processes, and
// the polyglot sub-indexers run sequentially within one process, so a
// package-level flag is safe here. Default (false) prints only the phased
// summary block produced by indexReport.
var indexVerbose bool

func setIndexVerbose(v bool) { indexVerbose = v }

// vprintf prints only when verbose output is enabled.
func vprintf(format string, args ...any) {
	if indexVerbose {
		fmt.Printf(format, args...)
	}
}

// vprintln prints only when verbose output is enabled.
func vprintln(args ...any) {
	if indexVerbose {
		fmt.Println(args...)
	}
}

const (
	reportWidth       = 74
	reportLabelWidth  = 10
	reportDetailWidth = 52 // fits "DB=… Cache=… Evt=… Ext=… GRPC=… HTTP=… Outbox=…"
)

// indexReport accumulates per-stage timing plus a one-line detail for a single
// service index run and renders them as a categorised summary block with a
// duration column and a total. Phase lines stream as each stage finishes so a
// long run still shows live progress; finish() prints the wall-clock total and
// any warning count. All deeper diagnostics go through vprintf (verbose only).
type indexReport struct {
	service   string
	started   bool
	curLabel  string
	curStart  time.Time
	warnings  int
	startWall time.Time
}

func newIndexReport(service string) *indexReport {
	return &indexReport{service: service}
}

// start prints the top border once, on the first phase or warning.
func (r *indexReport) start() {
	if r == nil || r.started {
		return
	}
	r.started = true
	r.startWall = time.Now()
	fmt.Printf("\n%s\n", topBorder("━━ "+r.service+" "))
}

// begin marks the start of a phase; end closes it and prints its line.
func (r *indexReport) begin(label string) {
	if r == nil {
		return
	}
	r.start()
	r.curLabel = label
	r.curStart = time.Now()
}

// end closes the current phase, printing "<label> <detail> <duration>".
func (r *indexReport) end(detail string) {
	if r == nil || r.curLabel == "" {
		return
	}
	d := time.Since(r.curStart)
	fmt.Printf(" %s %s %7s\n",
		padRight(r.curLabel, reportLabelWidth),
		padRight(truncateRunes(detail, reportDetailWidth), reportDetailWidth),
		fmtDur(d))
	r.curLabel = ""
}

// warn prints a warning line inside the block and counts it for the footer.
func (r *indexReport) warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if r != nil {
		r.start()
		r.warnings++
	}
	fmt.Printf("   ⚠ %s\n", msg)
}

// finish prints the footer: wall-clock total and any warning count.
func (r *indexReport) finish() {
	if r == nil || !r.started {
		return
	}
	fmt.Printf("%s\n", strings.Repeat("─", reportWidth))
	tail := ""
	if r.warnings > 0 {
		tail = fmt.Sprintf("   ⚠ %d warning(s)", r.warnings)
	}
	fmt.Printf(" ✓ %s indexed in %s%s\n", r.service, fmtDur(time.Since(r.startWall)), tail)
	fmt.Printf("%s\n", strings.Repeat("━", reportWidth))
}

// topBorder pads prefix with box-drawing dashes to reportWidth display columns.
func topBorder(prefix string) string {
	pad := max(reportWidth-utf8.RuneCountInString(prefix), 0)
	return prefix + strings.Repeat("━", pad)
}

// padRight pads s with spaces to width display columns (rune-aware).
func padRight(s string, width int) string {
	pad := width - utf8.RuneCountInString(s)
	if pad <= 0 {
		return s
	}
	return s + strings.Repeat(" ", pad)
}

// truncateRunes shortens s to at most max display columns, adding an ellipsis.
func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	if max <= 1 {
		return string([]rune(s)[:max])
	}
	return string([]rune(s)[:max-1]) + "…"
}

// fmtDur renders a duration compactly: "1.2s" at or above a second, else "340ms".
func fmtDur(d time.Duration) string {
	if d >= time.Second {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dms", d.Milliseconds())
}
