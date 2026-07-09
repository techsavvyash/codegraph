// Package dblock serializes test packages that share the development Neo4j
// database. `go test ./...` runs packages in parallel; without this lock the
// harness, integration, and MCP handler suites interleave their seed/delete
// cycles against the same database and corrupt each other's fixtures.
//
// Each such package acquires the lock in TestMain and holds it for the whole
// package run, so package order is arbitrary but access is exclusive.
package dblock

import (
	"fmt"
	"os"
	"path/filepath"
)

// Acquire blocks until this process holds the exclusive cross-package
// database test lock and returns the function that releases it.
func Acquire() (release func()) {
	path := filepath.Join(os.TempDir(), "codegraph-neo4j-test.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o666)
	if err != nil {
		panic(fmt.Sprintf("dblock: open %s: %v", path, err))
	}
	if err := flockExclusive(f); err != nil {
		f.Close()
		panic(fmt.Sprintf("dblock: lock %s: %v", path, err))
	}
	return func() {
		if err := flockRelease(f); err != nil {
			f.Close()
			panic(fmt.Sprintf("dblock: unlock %s: %v", path, err))
		}
		f.Close()
	}
}
