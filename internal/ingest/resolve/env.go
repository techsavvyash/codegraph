package resolve

import "os"

// processEnviron returns the current process environment. Split out from
// cleanGoEnv purely so tests can exercise the GOFLAGS/GOWORK-stripping logic
// against a synthetic environment without touching real process state.
func processEnviron() []string {
	return os.Environ()
}
