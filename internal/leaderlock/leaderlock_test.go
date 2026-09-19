package leaderlock

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeGate is a minimal Gate implementation used to prove the interface is
// small enough for controller tests (Task 4) to fake without any Postgres
// dependency.
type fakeGate struct {
	leader bool
}

func (f fakeGate) IsLeader() bool { return f.leader }

func TestFakeGateSatisfiesInterface(t *testing.T) {
	var g Gate = fakeGate{leader: true}
	assert.True(t, g.IsLeader())

	g = fakeGate{leader: false}
	assert.False(t, g.IsLeader())
}

// runAndWait starts Run in a goroutine and returns a channel closed when Run
// returns, so tests can assert the goroutine actually exits (no leak) after
// ctx is cancelled.
func runAndWait(g *PostgresGate, ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		g.Run(ctx)
		close(done)
	}()
	return done
}

func TestPostgresGate_AcquireSetsLeaderTrue(t *testing.T) {
	var calls atomic.Int32
	var unlockCalls atomic.Int32

	g := &PostgresGate{retry: 5 * time.Millisecond}
	g.tryLock = func(ctx context.Context) (bool, error) {
		calls.Add(1)
		return true, nil
	}
	g.unlock = func() { unlockCalls.Add(1) }

	ctx, cancel := context.WithCancel(context.Background())
	done := runAndWait(g, ctx)

	require.Eventually(t, g.IsLeader, time.Second, 2*time.Millisecond, "expected gate to become leader")
	assert.GreaterOrEqual(t, calls.Load(), int32(1))

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run goroutine did not exit after ctx cancellation (goroutine leak)")
	}

	assert.False(t, g.IsLeader(), "leader flag must be cleared on shutdown")
	assert.Equal(t, int32(1), unlockCalls.Load(), "unlock must be called exactly once on shutdown")
}

func TestPostgresGate_TryLockErrorKeepsFollowerAndRetries(t *testing.T) {
	var calls atomic.Int32
	var unlockCalls atomic.Int32

	g := &PostgresGate{retry: 5 * time.Millisecond}
	g.tryLock = func(ctx context.Context) (bool, error) {
		calls.Add(1)
		return false, errors.New("connection lost")
	}
	g.unlock = func() { unlockCalls.Add(1) }

	ctx, cancel := context.WithCancel(context.Background())
	done := runAndWait(g, ctx)

	// Give the loop a few ticks to retry so we can observe the call count
	// increasing while the gate stays a follower throughout.
	require.Eventually(t, func() bool { return calls.Load() >= 3 }, time.Second, 2*time.Millisecond,
		"expected tryLock to be retried after errors")
	assert.False(t, g.IsLeader(), "gate must not become leader when tryLock errors")

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run goroutine did not exit after ctx cancellation (goroutine leak)")
	}

	assert.False(t, g.IsLeader())
	assert.Equal(t, int32(1), unlockCalls.Load())
}

func TestPostgresGate_LosesLeadershipOnSubsequentError(t *testing.T) {
	var calls atomic.Int32

	g := &PostgresGate{retry: 5 * time.Millisecond}
	g.tryLock = func(ctx context.Context) (bool, error) {
		n := calls.Add(1)
		if n == 1 {
			return true, nil
		}
		return false, errors.New("connection lost")
	}
	g.unlock = func() {}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := runAndWait(g, ctx)
	defer func() {
		cancel()
		<-done
	}()

	require.Eventually(t, g.IsLeader, time.Second, 2*time.Millisecond, "expected initial acquisition to succeed")
	require.Eventually(t, func() bool { return !g.IsLeader() }, time.Second, 2*time.Millisecond,
		"expected leadership to be cleared after a later tryLock error (connection loss)")
}

func TestPostgresGate_CtxCancelReleasesLockEvenWhileLeader(t *testing.T) {
	var unlockCalls atomic.Int32

	g := &PostgresGate{retry: 5 * time.Millisecond}
	g.tryLock = func(ctx context.Context) (bool, error) { return true, nil }
	g.unlock = func() { unlockCalls.Add(1) }

	ctx, cancel := context.WithCancel(context.Background())
	done := runAndWait(g, ctx)

	require.Eventually(t, g.IsLeader, time.Second, 2*time.Millisecond)

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run goroutine did not exit after ctx cancellation (goroutine leak)")
	}

	assert.False(t, g.IsLeader())
	assert.Equal(t, int32(1), unlockCalls.Load())
}

func TestNew_WiresFieldsWithoutTouchingDB(t *testing.T) {
	// New must not perform any I/O itself (no real *sql.DB is available in
	// this unit test) -- it only wires up the gate and its injectable seams,
	// which real SQL execution deferred until Run/tryLock is actually
	// invoked.
	g := New(nil, CertDistributorLockKey, 30*time.Second)

	require.NotNil(t, g)
	assert.Equal(t, CertDistributorLockKey, g.key)
	assert.Equal(t, 30*time.Second, g.retry)
	assert.NotNil(t, g.tryLock)
	assert.NotNil(t, g.unlock)
	assert.False(t, g.IsLeader())
}

func TestCertDistributorLockKey_IsStable(t *testing.T) {
	// This constant must never change once deployed: changing it would
	// silently split leader election across two different advisory locks.
	// Pin the expected value so an accidental edit fails CI loudly.
	assert.Equal(t, int64(0x6667_63_01), CertDistributorLockKey)
}
