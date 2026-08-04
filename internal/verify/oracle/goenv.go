package oracle

import "os"

// cleanGoEnv mirrors internal/ingest/resolve's cleanGoEnv: it strips
// GOFLAGS/GOWORK from the inherited process environment before handing it to
// go/packages, so an ambient monorepo workspace file or -mod=readonly flag
// set for the outer codegraph module cannot leak into (and break) loading of
// the target project passed via --project. Duplicated rather than imported
// because the source is unexported in internal/ingest/resolve and this
// package must stay independently buildable per the RFC-013 file-ownership
// split.
func cleanGoEnv() []string {
	base := os.Environ()
	out := make([]string, 0, len(base)+2)
	for _, kv := range base {
		if hasEnvPrefix(kv, "GOFLAGS=") || hasEnvPrefix(kv, "GOWORK=") {
			continue
		}
		out = append(out, kv)
	}
	out = append(out, "GOFLAGS=", "GOWORK=off")
	return out
}

func hasEnvPrefix(kv, prefix string) bool {
	return len(kv) >= len(prefix) && kv[:len(prefix)] == prefix
}
