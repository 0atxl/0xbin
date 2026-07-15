// Package httpapi provides the public HTTP boundary for 0xbin.
package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"net"
	"net/http"

	"github.com/0atxl/0xbin/internal/config"
)

// ErrServerClosed is returned by Serve after a graceful shutdown.
var ErrServerClosed = http.ErrServerClosed

// Server owns the configured HTTP server lifecycle.
type Server struct {
	server *http.Server
}

// NewServer creates the HTTP server. Database readiness is deliberately not
// wired until Step 2.
func NewServer(cfg config.Config, readiness ...func(context.Context) error) *Server {
	var ready func(context.Context) error
	if len(readiness) > 0 {
		ready = readiness[0]
	}
	return &Server{server: &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           NewHandler(ready),
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}}
}

// Serve accepts HTTP traffic on listener.
func (s *Server) Serve(listener net.Listener) error {
	return s.server.Serve(listener)
}

// Shutdown gracefully stops accepting requests and waits for active requests.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

// NewHandler creates the root router and its foundational middleware.
func NewHandler(readiness ...func(context.Context) error) http.Handler {
	var ready func(context.Context) error
	if len(readiness) > 0 {
		ready = readiness[0]
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", live)
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) { notReady(w, r, ready) })
	mux.HandleFunc("/api/", apiNotFound)
	mux.HandleFunc("/api", apiNotFound)
	mux.HandleFunc("/", notFound)
	return requestID(recoverPanics(mux))
}

func live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func notReady(w http.ResponseWriter, r *http.Request, ready func(context.Context) error) {
	if ready != nil && ready(r.Context()) == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	writeError(w, http.StatusServiceUnavailable, "service_not_ready", "Service is not ready", requestIDFromContext(r.Context()))
}

func apiNotFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, "not_found", "Not found", requestIDFromContext(r.Context()))
}

func notFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, "not_found", "Not found", requestIDFromContext(r.Context()))
}

type requestIDKey struct{}

func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

func newRequestID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "unavailable"
	}
	return base64.RawURLEncoding.EncodeToString(bytes[:])
}

func requestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

func recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deferred := &responseWriter{ResponseWriter: w}
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.Error("recovered panic")
				if !deferred.wroteHeader {
					writeError(deferred, http.StatusInternalServerError, "internal_error", "Internal server error", requestIDFromContext(r.Context()))
				}
			}
		}()
		next.ServeHTTP(deferred, r)
	})
}

type responseWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

func (w *responseWriter) WriteHeader(status int) {
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseWriter) Write(body []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}
