package cleanup

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func TestRunOnceReclaimsBatchesUpToSafetyCap(t *testing.T) {
	store := &fakeStore{counts: []int64{2, 2, 2}}
	worker := newTestWorker(t, store, time.Hour, time.Second, 2, 2)
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.calls != 2 {
		t.Fatalf("DeleteExpiredBatch calls = %d, want 2", store.calls)
	}
}

func TestRunOnceStopsWhenBatchIsNotFull(t *testing.T) {
	store := &fakeStore{counts: []int64{2, 1}}
	worker := newTestWorker(t, store, time.Hour, time.Second, 2, 10)
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.calls != 2 {
		t.Fatalf("DeleteExpiredBatch calls = %d, want 2", store.calls)
	}
}

func TestRunOnceReclaimsLiveRoomsInIndependentBoundedBatches(t *testing.T) {
	store := &fakeStore{counts: []int64{0}, roomCounts: []int64{2, 2, 1}}
	worker := newTestWorker(t, store, time.Hour, time.Second, 2, 2)
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.calls != 1 || store.roomCalls != 2 {
		t.Fatalf("cleanup calls = pastes %d, live rooms %d; want 1, 2", store.calls, store.roomCalls)
	}
}

func TestRunOnceSkipsLiveRoomCleanupWhenDisabled(t *testing.T) {
	store := &fakeStore{counts: []int64{1}, roomCounts: []int64{1}}
	worker, err := NewWorkerWithLiveRooms(store, time.Hour, time.Second, 2, 2, func() time.Time { return time.Unix(0, 0).UTC() }, slog.New(slog.NewTextHandler(io.Discard, nil)), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.calls != 1 || store.roomCalls != 0 {
		t.Fatalf("cleanup calls = pastes %d, live rooms %d; want 1, 0", store.calls, store.roomCalls)
	}
}

func TestRunOnceStillReclaimsLiveRoomsWhenPasteCleanupFails(t *testing.T) {
	pasteErr := errors.New("paste cleanup unavailable")
	store := &fakeStore{err: pasteErr, roomCounts: []int64{1}}
	worker := newTestWorker(t, store, time.Hour, time.Second, 2, 2)
	if err := worker.RunOnce(context.Background()); !errors.Is(err, pasteErr) {
		t.Fatalf("RunOnce() error = %v, want %v", err, pasteErr)
	}
	if store.roomCalls != 1 {
		t.Fatalf("live room cleanup calls = %d, want 1", store.roomCalls)
	}
}

func TestRunOncePropagatesCancellationAndStorageFailure(t *testing.T) {
	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		worker := newTestWorker(t, &fakeStore{}, time.Hour, time.Second, 1, 1)
		if err := worker.RunOnce(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("RunOnce() error = %v, want context canceled", err)
		}
	})
	t.Run("storage failure", func(t *testing.T) {
		store := &fakeStore{err: errors.New("database unavailable")}
		worker := newTestWorker(t, store, time.Hour, time.Second, 1, 1)
		if err := worker.RunOnce(context.Background()); !errors.Is(err, store.err) {
			t.Fatalf("RunOnce() error = %v, want %v", err, store.err)
		}
	})
}

func TestRunStopsOnCancellation(t *testing.T) {
	worker := newTestWorker(t, &fakeStore{}, time.Hour, time.Second, 1, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		worker.Run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop after cancellation")
	}
}

func newTestWorker(t *testing.T, store Store, interval, timeout time.Duration, batchSize, maxBatches int) *Worker {
	t.Helper()
	worker, err := NewWorker(store, interval, timeout, batchSize, maxBatches, func() time.Time { return time.Unix(0, 0).UTC() }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

type fakeStore struct {
	mu         sync.Mutex
	counts     []int64
	calls      int
	err        error
	roomCounts []int64
	roomCalls  int
	roomErr    error
}

func (s *fakeStore) DeleteExpiredRooms(ctx context.Context, _ time.Time, _ int) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.roomCalls++
	if s.roomErr != nil {
		return 0, s.roomErr
	}
	if len(s.roomCounts) == 0 {
		return 0, nil
	}
	count := s.roomCounts[0]
	s.roomCounts = s.roomCounts[1:]
	return count, nil
}

func (s *fakeStore) DeleteExpiredBatch(ctx context.Context, _ time.Time, _ int) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return 0, s.err
	}
	if len(s.counts) == 0 {
		return 0, nil
	}
	count := s.counts[0]
	s.counts = s.counts[1:]
	return count, nil
}
