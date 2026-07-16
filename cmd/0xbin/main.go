// Command 0xbin starts the 0xbin service.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/0atxl/0xbin/internal/cleanup"
	"github.com/0atxl/0xbin/internal/config"
	"github.com/0atxl/0xbin/internal/httpapi"
	"github.com/0atxl/0xbin/internal/paste"
	"github.com/0atxl/0xbin/internal/slug"
	"github.com/0atxl/0xbin/internal/storage/sqlite"
)

func main() {
	if err := run(); err != nil {
		slog.Error("0xbin stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		return err
	}
	store, err := sqlite.Open(context.Background(), cfg.DataDir)
	if err != nil {
		return err
	}
	defer store.Close()
	expiries, err := expiryPolicy(cfg)
	if err != nil {
		return err
	}
	pastes, err := paste.NewService(store, slug.NewDefaultGenerator(), expiries, cfg.MaxPasteBytes, time.Now)
	if err != nil {
		return err
	}
	worker, err := cleanup.NewWorker(store, cleanup.DefaultInterval, cleanup.DefaultTimeout, cleanup.DefaultBatchSize, cleanup.DefaultMaxBatches, time.Now, slog.Default())
	if err != nil {
		return err
	}
	cleanupCtx, stopCleanup := context.WithCancel(context.Background())
	_ = worker.RunOnce(cleanupCtx)
	cleanupDone := make(chan struct{})
	go func() {
		worker.Run(cleanupCtx)
		close(cleanupDone)
	}()
	defer func() {
		stopCleanup()
		<-cleanupDone
	}()

	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen on %q: %w", cfg.ListenAddr, err)
	}

	server := httpapi.NewServer(cfg, pastes, store.Ping)
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(listener)
	}()

	slog.Info("0xbin listening", "address", listener.Addr().String())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, httpapi.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}

	if err := <-serveErr; err != nil && !errors.Is(err, httpapi.ErrServerClosed) {
		return err
	}
	return nil
}

func expiryPolicy(cfg config.Config) (paste.ExpiryPolicy, error) {
	allowed := make(map[string]time.Duration, len(cfg.AllowedExpiryIDs))
	for index, identifier := range cfg.AllowedExpiryIDs {
		allowed[identifier] = cfg.AllowedExpiries[index]
	}
	return paste.NewExpiryPolicy(allowed)
}
