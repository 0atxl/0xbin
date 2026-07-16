// Package cleanup reclaims expired paste rows without affecting access control.
package cleanup

import (
	"context"
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

// Worker periodically reclaims expired rows in bounded passes.
type Worker struct {
	store      Store
	interval   time.Duration
	timeout    time.Duration
	batchSize  int
	maxBatches int
	now        func() time.Time
	logger     *slog.Logger
}

// NewWorker validates cleanup limits and constructs a cancellation-aware worker.
func NewWorker(store Store, interval, timeout time.Duration, batchSize, maxBatches int, now func() time.Time, logger *slog.Logger) (*Worker, error) {
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
	return &Worker{store: store, interval: interval, timeout: timeout, batchSize: batchSize, maxBatches: maxBatches, now: now, logger: logger}, nil
}

// RunOnce performs one bounded cleanup pass. A failure is returned for callers
// to observe, while the periodic loop continues after later ticks.
func (w *Worker) RunOnce(parent context.Context) error {
	started := time.Now()
	ctx, cancel := context.WithTimeout(parent, w.timeout)
	defer cancel()

	var deleted int64
	for batch := 0; batch < w.maxBatches; batch++ {
		count, err := w.store.DeleteExpiredBatch(ctx, w.now().UTC(), w.batchSize)
		if err != nil {
			w.logger.Warn("expired paste cleanup failed", "deleted", deleted, "duration", time.Since(started), "error", err)
			return fmt.Errorf("delete expired batch: %w", err)
		}
		deleted += count
		if count < int64(w.batchSize) {
			break
		}
	}
	w.logger.Info("expired paste cleanup completed", "deleted", deleted, "duration", time.Since(started))
	return nil
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
