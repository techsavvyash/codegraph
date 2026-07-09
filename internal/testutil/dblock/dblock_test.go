package dblock

import (
	"testing"
	"time"
)

// TestAcquireIsExclusive proves a second Acquire blocks until the first
// holder releases. flock locks attach to the open file description, so two
// separate opens conflict even within one process.
func TestAcquireIsExclusive(t *testing.T) {
	release1 := Acquire()

	acquired2 := make(chan func(), 1)
	go func() {
		acquired2 <- Acquire()
	}()

	select {
	case <-acquired2:
		t.Fatal("second Acquire succeeded while first lock was still held")
	case <-time.After(300 * time.Millisecond):
		// Expected: still blocked.
	}

	release1()

	select {
	case release2 := <-acquired2:
		release2()
	case <-time.After(5 * time.Second):
		t.Fatal("second Acquire did not proceed after first release")
	}
}
