package httpapi

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/0atxl/0xbin/internal/config"
)

func TestHealthEndpoints(t *testing.T) {
	t.Parallel()
	handler := NewHandler()

	t.Run("liveness", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health/live", nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
		}
		if got := recorder.Header().Get("X-Request-ID"); got == "" {
			t.Error("missing X-Request-ID")
		}
	})

	t.Run("readiness is reserved", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
		assertError(t, recorder, http.StatusServiceUnavailable, "service_not_ready")
	})
}

func TestUnknownAPIRouteReturnsJSONError(t *testing.T) {
	t.Parallel()
	recorder := httptest.NewRecorder()
	NewHandler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/unknown", nil))
	assertError(t, recorder, http.StatusNotFound, "not_found")
}

func TestRecoveryReturnsStableError(t *testing.T) {
	t.Parallel()
	recorder := httptest.NewRecorder()
	handler := requestID(recoverPanics(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("test panic")
	})))
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/test", nil))
	assertError(t, recorder, http.StatusInternalServerError, "internal_error")
}

func TestServerShutdownHonorsContext(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	server := NewServer(cfg)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()

	deadline := time.Now().Add(time.Second)
	for {
		connection, dialErr := net.DialTimeout("tcp", listener.Addr().String(), 10*time.Millisecond)
		if dialErr == nil {
			_ = connection.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not start: %v", dialErr)
		}
		time.Sleep(time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if err := <-serveErr; err != ErrServerClosed {
		t.Fatalf("Serve() error = %v, want %v", err, ErrServerClosed)
	}
}

func assertError(t *testing.T, recorder *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if recorder.Code != wantStatus {
		t.Fatalf("status = %d, want %d", recorder.Code, wantStatus)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	var response Error
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if response.Error.Code != wantCode {
		t.Errorf("error code = %q, want %q", response.Error.Code, wantCode)
	}
	if response.Error.RequestID == "" {
		t.Error("error response missing request ID")
	}
}

func testConfig(t *testing.T) config.Config {
	t.Helper()
	cfg, err := config.Load(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
