// Package cleanup reclaims expired paste rows without affecting access control.
package cleanup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

const (
	DefaultInterval   = time.Minute
	DefaultTimeout    = 5 * time.Second
	DefaultBatchSize  = 100
	DefaultMaxBatches = 10
)

// Store is the small storage boundary required by the cleanup worker.
type Store interface {
	DeleteExpiredBatch(context.Context, time.Time, int) (int64, error)
}

// LiveRoomStore is optional because a disabled live-sharing service must not
// perform live-only background work.
type LiveRoomStore interface {
	DeleteExpiredRooms(context.Context, time.Time, int) (int64, error)
}

// Worker periodically reclaims expired rows in bounded passes.
type Worker struct {
	store      Store
	liveStore  LiveRoomStore
	interval   time.Duration
	timeout    time.Duration
	batchSize  int
	maxBatches int
	now        func() time.Time
	logger     *slog.Logger
}

// NewWorker validates cleanup limits and constructs a cancellation-aware worker.
func NewWorker(store Store, interval, timeout time.Duration, batchSize, maxBatches int, now func() time.Time, logger *slog.Logger) (*Worker, error) {
	return newWorker(store, interval, timeout, batchSize, maxBatches, now, logger, true)
}

// NewWorkerWithLiveRooms optionally enables expiry cleanup for persisted live
// rooms while retaining ordinary paste cleanup in every deployment mode.
func NewWorkerWithLiveRooms(store Store, interval, timeout time.Duration, batchSize, maxBatches int, now func() time.Time, logger *slog.Logger, cleanLiveRooms bool) (*Worker, error) {
	return newWorker(store, interval, timeout, batchSize, maxBatches, now, logger, cleanLiveRooms)
}

func newWorker(store Store, interval, timeout time.Duration, batchSize, maxBatches int, now func() time.Time, logger *slog.Logger, cleanLiveRooms bool) (*Worker, error) {
	if store == nil || now == nil {
		return nil, fmt.Errorf("store and clock are required")
	}
	if interval <= 0 || timeout <= 0 {
		return nil, fmt.Errorf("cleanup interval and timeout must be positive")
	}
	if batchSize < 1 || maxBatches < 1 {
		return nil, fmt.Errorf("cleanup batch size and maximum batches must be positive")
	}
	if logger == nil {
		logger = slog.Default()
	}
	worker := &Worker{store: store, interval: interval, timeout: timeout, batchSize: batchSize, maxBatches: maxBatches, now: now, logger: logger}
	if cleanLiveRooms {
		liveStore, ok := store.(LiveRoomStore)
		if !ok {
			return nil, fmt.Errorf("live room cleanup requires a live room store")
		}
		worker.liveStore = liveStore
	}
	return worker, nil
}

// RunOnce performs one bounded cleanup pass. A failure is returned for callers
// to observe, while the periodic loop continues after later ticks.
func (w *Worker) RunOnce(parent context.Context) error {
	started := time.Now()
	ctx, cancel := context.WithTimeout(parent, w.timeout)
	defer cancel()

	now := w.now().UTC()
	pastes, pasteErr := w.deleteBatches(ctx, func(ctx context.Context, limit int) (int64, error) {
		return w.store.DeleteExpiredBatch(ctx, now, limit)
	})
	var rooms int64
	var roomErr error
	if w.liveStore != nil {
		rooms, roomErr = w.deleteBatches(ctx, func(ctx context.Context, limit int) (int64, error) {
			return w.liveStore.DeleteExpiredRooms(ctx, now, limit)
		})
	}
	if pasteErr != nil || roomErr != nil {
		err := errors.Join(pasteErr, roomErr)
		w.logger.Warn("expired content cleanup failed", "pastes_deleted", pastes, "live_rooms_deleted", rooms, "duration", time.Since(started), "error", err)
		return err
	}
	w.logger.Info("expired content cleanup completed", "pastes_deleted", pastes, "live_rooms_deleted", rooms, "duration", time.Since(started))
	return nil
}

func (w *Worker) deleteBatches(ctx context.Context, deleteBatch func(context.Context, int) (int64, error)) (int64, error) {
	var deleted int64
	for batch := 0; batch < w.maxBatches; batch++ {
		count, err := deleteBatch(ctx, w.batchSize)
		if err != nil {
			return deleted, fmt.Errorf("delete expired batch: %w", err)
		}
		deleted += count
		if count < int64(w.batchSize) {
			break
		}
	}
	return deleted, nil
}

// Run waits for scheduled cleanup passes until the shutdown context is done.
// It stops its ticker before returning.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = w.RunOnce(ctx)
		}
	}
}
