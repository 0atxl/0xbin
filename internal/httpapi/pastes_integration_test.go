package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0atxl/0xbin/internal/paste"
	"github.com/0atxl/0xbin/internal/storage/sqlite"
)

func TestCreatePasteAccepts72HourExpiry(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	service := newIntegrationService(t, store, []string{"calmbrightotter"}, func() time.Time { return now })
	handler := NewHandler(testConfig(t), service)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/pastes", strings.NewReader(`{"mode":"plaintext","payload":{"version":1,"content":"three days"},"expiry":"72h"}`))
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusCreated, recorder.Body.String())
	}
	var response createPasteResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if want := now.Add(72 * time.Hour); !response.ExpiresAt.Equal(want) {
		t.Errorf("ExpiresAt = %v, want %v", response.ExpiresAt, want)
	}
}

func TestExpiredPastesAreNotReturnedThroughHTTP(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	createdAt := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	now := createdAt
	service := newIntegrationService(t, store, []string{
		"calmbrightotter",
		"quietquickwren",
		"swiftcleverfox",
	}, func() time.Time { return now })
	if _, err := service.CreatePlaintext(ctx, paste.CreatePlaintextInput{
		Payload: paste.PlaintextPayload{Version: paste.PlaintextVersion, Content: "plaintext secret"},
		Expiry:  "1h",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateEncrypted(ctx, paste.CreateEncryptedInput{
		Envelope: integrationEnvelope(),
		Expiry:   "1h",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreatePlaintext(ctx, paste.CreatePlaintextInput{
		Payload:       paste.PlaintextPayload{Version: paste.PlaintextVersion, Content: "burn secret"},
		Expiry:        "1h",
		BurnAfterRead: true,
	}); err != nil {
		t.Fatal(err)
	}

	now = createdAt.Add(2 * time.Hour)
	handler := NewHandler(testConfig(t), service)
	for _, test := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/pastes/calmbrightotter"},
		{http.MethodGet, "/api/v1/pastes/calmbrightotter/raw"},
		{http.MethodGet, "/api/v1/pastes/quietquickwren"},
		{http.MethodGet, "/api/v1/pastes/swiftcleverfox"},
		{http.MethodPost, "/api/v1/pastes/swiftcleverfox/consume"},
	} {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(test.method, test.path, nil))
			assertError(t, recorder, http.StatusNotFound, "not_found")
			if recorder.Header().Get("Cache-Control") != "no-store" {
				t.Error("expired response must not be cached")
			}
		})
	}

	for _, slug := range []string{"calmbrightotter", "quietquickwren", "swiftcleverfox"} {
		var count int
		if err := store.DB().QueryRowContext(ctx, "SELECT count(*) FROM pastes WHERE slug = ?", slug).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("expired row %q was cleaned during access; count = %d", slug, count)
		}
	}
}

func TestConcurrentHTTPConsumeHasExactlyOneWinner(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	service := newIntegrationService(t, store, []string{"quietbrightotter"}, func() time.Time { return now })
	created, err := service.CreatePlaintext(ctx, paste.CreatePlaintextInput{
		Payload:       paste.PlaintextPayload{Version: paste.PlaintextVersion, Content: "one winner"},
		Expiry:        "1h",
		BurnAfterRead: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(t)
	cfg.ConsumeRate.Count = 100
	handler := NewHandler(cfg, service)

	const contenders = 24
	type consumeResult struct {
		status int
		body   []byte
	}
	results := make(chan consumeResult, contenders)
	start := make(chan struct{})
	var group sync.WaitGroup
	for range contenders {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/pastes/"+created.Slug+"/consume", nil))
			results <- consumeResult{status: recorder.Code, body: recorder.Body.Bytes()}
		}()
	}
	close(start)
	group.Wait()
	close(results)

	successes := 0
	missing := 0
	for result := range results {
		switch result.status {
		case http.StatusOK:
			successes++
			var response pasteResponse
			if err := json.Unmarshal(result.body, &response); err != nil || response.Payload == nil || response.Payload.Content != "one winner" {
				t.Errorf("successful consume returned invalid body %q: %v", result.body, err)
			}
		case http.StatusNotFound:
			missing++
			var response Error
			if err := json.Unmarshal(result.body, &response); err != nil || response.Error.Code != "not_found" {
				t.Errorf("losing consume returned invalid body %q: %v", result.body, err)
			}
		default:
			t.Errorf("unexpected consume status %d: %s", result.status, result.body)
		}
	}
	if successes != 1 || missing != contenders-1 {
		t.Fatalf("consume results: successes = %d, not found = %d", successes, missing)
	}
	if _, err := store.GetActive(ctx, created.Slug, now); !errors.Is(err, paste.ErrNotFound) {
		t.Fatalf("consumed paste remains active: %v", err)
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/pastes/"+created.Slug+"/consume", nil))
	assertError(t, recorder, http.StatusNotFound, "not_found")
}

func newIntegrationService(t *testing.T, store paste.Store, slugs []string, now func() time.Time) *paste.Service {
	t.Helper()
	service, err := paste.NewService(store, &integrationSlugGenerator{slugs: slugs}, paste.DefaultExpiryPolicy(), paste.MaxContentBytes, now)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type integrationSlugGenerator struct {
	mu    sync.Mutex
	slugs []string
	next  int
}

func (g *integrationSlugGenerator) Generate() (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.next >= len(g.slugs) {
		return "", errors.New("no integration-test slug available")
	}
	slug := g.slugs[g.next]
	g.next++
	return slug, nil
}

func integrationEnvelope() paste.CiphertextEnvelope {
	return paste.CiphertextEnvelope{
		Version: paste.CryptoVersion, Algorithm: paste.CryptoAlgorithm,
		IV: "AAECAwQFBgcICQoL", Ciphertext: "AAECAwQFBgcICQoLDA0ODw",
	}
}
