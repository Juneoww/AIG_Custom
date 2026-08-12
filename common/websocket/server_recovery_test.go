package websocket

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRunCompletedResultRecoveryRunsImmediatelyAndContinuesAfterFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time, 1)
	calls := make(chan int, 3)
	done := make(chan struct{})
	count := 0
	go func() {
		runCompletedResultRecovery(ctx, ticks, func(context.Context) error {
			count++
			calls <- count
			if count == 1 {
				return errors.New("engine secret sentinel")
			}
			return nil
		})
		close(done)
	}()

	require.Equal(t, 1, <-calls, "startup recovery must run without waiting for a tick")
	ticks <- time.Now()
	require.Equal(t, 2, <-calls, "a failed pass must not stop later recovery passes")
	cancel()
	<-done
}

func TestStartCompletedResultRecoveryStopsInjectedTickerOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ticker := &recoveryTestTicker{ticks: make(chan time.Time)}
	done := make(chan struct{})
	go func() {
		startCompletedResultRecovery(ctx, time.Second, func(time.Duration) recoveryTicker { return ticker }, func(context.Context) error { return nil })
		close(done)
	}()
	cancel()
	<-done
	require.True(t, ticker.stopped)
}

type recoveryTestTicker struct {
	ticks   chan time.Time
	stopped bool
}

func (ticker *recoveryTestTicker) Chan() <-chan time.Time { return ticker.ticks }
func (ticker *recoveryTestTicker) Stop()                  { ticker.stopped = true }
