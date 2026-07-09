//go:build !unix

package dblock

import (
	"os"
	"time"
)

// Non-unix platforms have no flock(2); emulate the exclusive lock with an
// O_EXCL sentinel file so cross-package serialization still holds.
const sentinelSuffix = ".held"

func flockExclusive(f *os.File) error {
	sentinel := f.Name() + sentinelSuffix
	for {
		h, err := os.OpenFile(sentinel, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o666)
		if err == nil {
			h.Close()
			return nil
		}
		if !os.IsExist(err) {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func flockRelease(f *os.File) error {
	return os.Remove(f.Name() + sentinelSuffix)
}
