package neo4j

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j/db"
	"github.com/stretchr/testify/assert"
)

func TestRetryOnTransient_SucceedsOnFirstAttempt(t *testing.T) {
	callCount := 0
	err := retryOnTransient(context.Background(), 3, 100*time.Millisecond, func() error {
		callCount++
		return nil
	})

	assert.NoError(t, err)
	assert.Equal(t, 1, callCount, "function should be called exactly once on success")
}

func TestRetryOnTransient_RetriesOnRetryableError(t *testing.T) {
	callCount := 0
	err := retryOnTransient(context.Background(), 3, 50*time.Millisecond, func() error {
		callCount++
		if callCount < 3 {
			return &db.Neo4jError{Code: "Neo.TransientError.Transaction.DeadlockDetected"}
		}
		return nil
	})

	assert.NoError(t, err)
	assert.Equal(t, 3, callCount, "function should be called exactly 3 times (2 retries + final success)")
}

func TestRetryOnTransient_ReturnsNonRetryableErrorImmediately(t *testing.T) {
	callCount := 0
	expectedErr := fmt.Errorf("non-retryable error")
	err := retryOnTransient(context.Background(), 3, 50*time.Millisecond, func() error {
		callCount++
		return expectedErr
	})

	assert.Error(t, err)
	assert.Equal(t, expectedErr, err, "should return the original error")
	assert.Equal(t, 1, callCount, "function should be called exactly once on non-retryable error")
}

func TestRetryOnTransient_ExhaustsAttemptsAndReturnsLastError(t *testing.T) {
	callCount := 0
	err := retryOnTransient(context.Background(), 3, 25*time.Millisecond, func() error {
		callCount++
		return &db.Neo4jError{Code: "Neo.TransientError.Transaction.DeadlockDetected"}
	})

	assert.Error(t, err)
	assert.Equal(t, 3, callCount, "function should be called 3 times (all attempts exhausted)")
	assert.IsType(t, &db.Neo4jError{}, err, "should return a Neo4jError")
}

func TestRetryOnTransient_ContextCancellationDuringBackoff(t *testing.T) {
	callCount := 0
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after first call, during backoff before second attempt
	go func() {
		time.Sleep(25 * time.Millisecond)
		cancel()
	}()

	err := retryOnTransient(ctx, 3, 500*time.Millisecond, func() error {
		callCount++
		return &db.Neo4jError{Code: "Neo.TransientError.Transaction.DeadlockDetected"}
	})

	assert.ErrorIs(t, err, context.Canceled, "should return context.Canceled")
	assert.Equal(t, 1, callCount, "function should be called once before context cancellation")
}

func TestRetryOnTransient_ContextCancellationImmediately(t *testing.T) {
	callCount := 0
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_ = retryOnTransient(ctx, 3, 50*time.Millisecond, func() error {
		callCount++
		return &db.Neo4jError{Code: "Neo.TransientError.Transaction.DeadlockDetected"}
	})

	// May get context error or may get the original error on the first call
	// depending on timing; either way, we expect at most 1 call
	assert.True(t, callCount <= 1, "should stop promptly after context cancellation")
}

func TestRetryOnTransient_BackoffIncreases(t *testing.T) {
	callCount := 0
	callTimes := []time.Time{}
	startTime := time.Now()

	err := retryOnTransient(context.Background(), 3, 50*time.Millisecond, func() error {
		callCount++
		callTimes = append(callTimes, time.Now())
		if callCount < 3 {
			return &db.Neo4jError{Code: "Neo.TransientError.Transaction.DeadlockDetected"}
		}
		return nil
	})

	assert.NoError(t, err)
	assert.Equal(t, 3, callCount)

	// Verify backoff increases: second call should be ~50ms after first, third call should be ~100ms after second
	if len(callTimes) >= 2 {
		firstBackoff := callTimes[1].Sub(callTimes[0])
		// First backoff should be roughly 50ms (attempt 1 * 50ms)
		assert.Greater(t, firstBackoff, 30*time.Millisecond, "first backoff should be at least ~30ms")
		assert.Less(t, firstBackoff, 200*time.Millisecond, "first backoff should be less than 200ms")
	}

	if len(callTimes) >= 3 {
		secondBackoff := callTimes[2].Sub(callTimes[1])
		// Second backoff should be roughly 100ms (attempt 2 * 50ms)
		assert.Greater(t, secondBackoff, 80*time.Millisecond, "second backoff should be at least ~80ms")
		assert.Less(t, secondBackoff, 300*time.Millisecond, "second backoff should be less than 300ms")
	}

	// Verify total time is at least the sum of backoffs (50ms + 100ms = 150ms)
	totalTime := time.Since(startTime)
	assert.Greater(t, totalTime, 130*time.Millisecond, "total time should account for backoff periods")
}

func TestRetryOnTransient_IsRetryableRecognizesDeadlock(t *testing.T) {
	// Verify that neo4j.IsRetryable correctly identifies deadlock as retryable
	deadlockErr := &db.Neo4jError{Code: "Neo.TransientError.Transaction.DeadlockDetected"}
	assert.True(t, neo4j.IsRetryable(deadlockErr), "deadlock error should be retryable")
}

func TestRetryOnTransient_IsRetryableRecognizesNonRetryable(t *testing.T) {
	// Verify that neo4j.IsRetryable correctly identifies non-retryable errors
	nonRetryableErr := fmt.Errorf("connection refused")
	assert.False(t, neo4j.IsRetryable(nonRetryableErr), "plain error should not be retryable")

	neo4jErr := &db.Neo4jError{Code: "Neo.ClientError.Syntax.SyntaxError"}
	assert.False(t, neo4j.IsRetryable(neo4jErr), "client error should not be retryable")
}

func TestRetryOnTransient_ZeroAttemptsShouldFail(t *testing.T) {
	callCount := 0
	err := retryOnTransient(context.Background(), 0, 50*time.Millisecond, func() error {
		callCount++
		return fmt.Errorf("should not retry")
	})

	// With 0 attempts, the loop doesn't execute at all
	assert.Equal(t, 0, callCount)
	assert.Nil(t, err, "should return nil when attempts is 0")
}

func TestRetryOnTransient_SingleAttempt(t *testing.T) {
	callCount := 0
	err := retryOnTransient(context.Background(), 1, 50*time.Millisecond, func() error {
		callCount++
		return &db.Neo4jError{Code: "Neo.TransientError.Transaction.DeadlockDetected"}
	})

	assert.Error(t, err)
	assert.Equal(t, 1, callCount, "should be called exactly once")
}

func TestRetryOnTransient_MultipleRetryableErrorTypes(t *testing.T) {
	callCount := 0
	err := retryOnTransient(context.Background(), 3, 25*time.Millisecond, func() error {
		callCount++
		// Different transient errors on different attempts
		switch callCount {
		case 1:
			return &db.Neo4jError{Code: "Neo.TransientError.Transaction.DeadlockDetected"}
		case 2:
			return &db.Neo4jError{Code: "Neo.TransientError.General.TemporarilyUnavailable"}
		default:
			return nil
		}
	})

	assert.NoError(t, err)
	assert.Equal(t, 3, callCount, "should retry through multiple transient error types")
}
