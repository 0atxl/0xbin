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

	"github.com/0atxl/0xbin/internal/config"
	"github.com/0atxl/0xbin/internal/httpapi"
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

	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen on %q: %w", cfg.ListenAddr, err)
	}

	server := httpapi.NewServer(cfg)
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
