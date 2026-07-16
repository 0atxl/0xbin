package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/0atxl/0xbin/internal/paste"
)

func TestPlaintextCreateContract(t *testing.T) {
	created := testPaste()
	service := &fakePasteService{created: created}
	handler := NewHandler(testConfig(t), service)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/pastes", strings.NewReader(`{"mode":"plaintext","payload":{"version":1,"title":"Example","language":"go","content":"package main\n"},"expiry":"1h"}`))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusCreated, recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Error("create response must not be cached")
	}
	if service.input.Expiry != "1h" || service.input.Payload.Content != "package main\n" || service.input.BurnAfterRead {
		t.Fatalf("service input = %#v", service.input)
	}
	var response createPasteResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Slug != created.Slug || response.URL != "http://localhost:8080/quietbrightotter" || !response.ExpiresAt.Equal(created.ExpiresAt) {
		t.Fatalf("response = %#v", response)
	}
}

func TestPlaintextCreateErrors(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		serviceErr error
		status     int
		code       string
	}{
		{name: "malformed JSON", body: `{`, status: http.StatusBadRequest, code: "invalid_request"},
		{name: "unknown field", body: `{"mode":"plaintext","payload":{"version":1,"content":"x"},"expiry":"1h","extra":true}`, status: http.StatusBadRequest, code: "invalid_request"},
		{name: "encrypted mode", body: `{"mode":"encrypted","payload":{"version":1,"content":"x"},"expiry":"1h"}`, status: http.StatusBadRequest, code: "invalid_request"},
		{name: "content too large", body: `{"mode":"plaintext","payload":{"version":1,"content":"x"},"expiry":"1h"}`, serviceErr: paste.ErrPayloadTooLarge, status: http.StatusRequestEntityTooLarge, code: "payload_too_large"},
		{name: "temporary failure", body: `{"mode":"plaintext","payload":{"version":1,"content":"x"},"expiry":"1h"}`, serviceErr: errors.New("database unavailable"), status: http.StatusServiceUnavailable, code: "service_unavailable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakePasteService{createErr: test.serviceErr}
			recorder := httptest.NewRecorder()
			NewHandler(testConfig(t), service).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/pastes", strings.NewReader(test.body)))
			assertError(t, recorder, test.status, test.code)
		})
	}
}

func TestPlaintextGetAndRawContract(t *testing.T) {
	result := testPaste()
	handler := NewHandler(testConfig(t), &fakePasteService{result: result})

	t.Run("get", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/pastes/quietbrightotter", nil))
		if recorder.Code != http.StatusOK || recorder.Header().Get("Cache-Control") != "no-store" || recorder.Header().Get("X-Robots-Tag") != "noindex, nofollow, noarchive" {
			t.Fatalf("status/headers = %d %#v", recorder.Code, recorder.Header())
		}
		var response pasteResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Payload == nil || response.Payload.Content != result.Payload.Content {
			t.Fatalf("content = %q", response.Payload.Content)
		}
	})

	t.Run("raw", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/pastes/quietbrightotter/raw", nil))
		if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "text/plain; charset=utf-8" || recorder.Header().Get("X-Content-Type-Options") != "nosniff" || recorder.Body.String() != result.Payload.Content {
			t.Fatalf("status/headers/body = %d %#v %q", recorder.Code, recorder.Header(), recorder.Body.String())
		}
	})
}

func TestEncryptedCreateAndRetrieveContract(t *testing.T) {
	created := testEncryptedPaste()
	service := &fakePasteService{encryptedCreated: created, result: created}
	handler := NewHandler(testConfig(t), service)
	body := `{"mode":"encrypted","payload":{"version":1,"algorithm":"A256GCM","iv":"AAECAwQFBgcICQoL","ciphertext":"AAECAwQFBgcICQoLDA0ODw"},"expiry":"1h"}`
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/pastes", strings.NewReader(body)))
	if recorder.Code != http.StatusCreated || service.encryptedCreateCalls != 1 {
		t.Fatalf("create status/calls = %d/%d: %s", recorder.Code, service.encryptedCreateCalls, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/pastes/quietbrightotter", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("get status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var response pasteResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.IsEncrypted || response.Envelope == nil || *response.Envelope != *created.Envelope || response.Payload != nil || response.CryptoVersion == nil || *response.CryptoVersion != paste.CryptoVersion {
		t.Fatalf("encrypted response = %#v", response)
	}
}

func TestEncryptedCreateRejectsInvalidEnvelopeAndKey(t *testing.T) {
	tests := []string{
		`{"mode":"encrypted","payload":{"version":2,"algorithm":"A256GCM","iv":"AAECAwQFBgcICQoL","ciphertext":"AAECAwQFBgcICQoLDA0ODw"},"expiry":"1h"}`,
		`{"mode":"encrypted","payload":{"version":1,"algorithm":"A256GCM","iv":"AAECAwQFBgcICQoL","ciphertext":"AAECAwQFBgcICQoLDA0ODw","key":"AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"},"expiry":"1h"}`,
		`{"mode":"encrypted","payload":{"version":1,"algorithm":"A256GCM","iv":"AAECAwQFBgcICQoL","ciphertext":"AAECAwQFBgcICQoLDA0ODw"},"expiry":"1h","key":"AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"}`,
	}
	for _, body := range tests {
		service := &fakePasteService{}
		recorder := httptest.NewRecorder()
		NewHandler(testConfig(t), service).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/pastes", strings.NewReader(body)))
		assertError(t, recorder, http.StatusBadRequest, "invalid_request")
		if service.encryptedCreateCalls != 0 {
			t.Fatal("server accepted an encryption key")
		}
	}
}

func TestRawEncryptedPasteIsNotFound(t *testing.T) {
	service := &fakePasteService{result: testEncryptedPaste()}
	recorder := httptest.NewRecorder()
	NewHandler(testConfig(t), service).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/pastes/quietbrightotter/raw", nil))
	assertError(t, recorder, http.StatusNotFound, "not_found")
}

func TestBurnGetDoesNotExposeOrConsumeContent(t *testing.T) {
	result := testPaste()
	result.BurnAfterRead = true
	service := &fakePasteService{result: result}
	handler := NewHandler(testConfig(t), service)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/pastes/quietbrightotter", nil))
	if recorder.Code != http.StatusOK || service.consumeCalls != 0 || strings.Contains(recorder.Body.String(), result.Payload.Content) {
		t.Fatalf("GET status/calls/body = %d/%d/%q", recorder.Code, service.consumeCalls, recorder.Body.String())
	}
	var confirmation burnConfirmationResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &confirmation); err != nil || !confirmation.BurnAfterRead || confirmation.IsEncrypted {
		t.Fatalf("burn confirmation = %#v, %v", confirmation, err)
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/pastes/quietbrightotter/raw", nil))
	assertError(t, recorder, http.StatusNotFound, "not_found")
}

func TestConsumeContractAndNotFound(t *testing.T) {
	result := testPaste()
	result.BurnAfterRead = true
	service := &fakePasteService{consumed: result}
	handler := NewHandler(testConfig(t), service)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/pastes/quietbrightotter/consume", nil))
	var response pasteResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || service.consumeCalls != 1 || response.Payload == nil || response.Payload.Content != result.Payload.Content {
		t.Fatalf("consume status/calls/body = %d/%d/%q", recorder.Code, service.consumeCalls, recorder.Body.String())
	}
	service.consumeErr = paste.ErrNotFound
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/pastes/quietbrightotter/consume", nil))
	assertError(t, recorder, http.StatusNotFound, "not_found")
}

func TestPlaintextGetCollapsesInvalidMissingAndExpired(t *testing.T) {
	for _, path := range []string{"/api/v1/pastes/INVALID", "/api/v1/pastes/missingbrightotter", "/api/v1/pastes/expirebrightotter"} {
		t.Run(path, func(t *testing.T) {
			service := &fakePasteService{getErr: paste.ErrNotFound}
			recorder := httptest.NewRecorder()
			NewHandler(testConfig(t), service).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
			assertError(t, recorder, http.StatusNotFound, "not_found")
			if recorder.Header().Get("Cache-Control") != "no-store" {
				t.Error("not-found paste response must not be cached")
			}
		})
	}
}

func TestPlaintextCreateBoundsRequestBody(t *testing.T) {
	service := &fakePasteService{}
	recorder := httptest.NewRecorder()
	body := `{"mode":"plaintext","payload":{"version":1,"content":"` + strings.Repeat("x", (1<<20)+(4<<10)+1) + `"},"expiry":"1h"}`
	NewHandler(testConfig(t), service).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/pastes", strings.NewReader(body)))
	assertError(t, recorder, http.StatusRequestEntityTooLarge, "payload_too_large")
	if service.createCalls != 0 {
		t.Fatalf("CreatePlaintext calls = %d, want 0", service.createCalls)
	}
}

func TestRateLimitReturnsRetryAfter(t *testing.T) {
	cfg := testConfig(t)
	cfg.CreateRate.Count = 1
	cfg.CreateRate.Window = time.Hour
	handler := NewHandler(cfg, &fakePasteService{created: testPaste()})
	body := `{"mode":"plaintext","payload":{"version":1,"content":"x"},"expiry":"1h"}`
	for attempt := 0; attempt < 2; attempt++ {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/pastes", strings.NewReader(body)))
		if attempt == 0 && recorder.Code != http.StatusCreated {
			t.Fatalf("first status = %d", recorder.Code)
		}
		if attempt == 1 {
			assertError(t, recorder, http.StatusTooManyRequests, "rate_limited")
			if recorder.Header().Get("Retry-After") == "" {
				t.Fatal("rate-limited response is missing Retry-After")
			}
		}
	}
}

func TestUntrustedForwardedIPCannotRotateRateLimitIdentity(t *testing.T) {
	cfg := testConfig(t)
	cfg.CreateRate.Count = 1
	cfg.CreateRate.Window = time.Hour
	handler := NewHandler(cfg, &fakePasteService{created: testPaste()})
	body := `{"mode":"plaintext","payload":{"version":1,"content":"x"},"expiry":"1h"}`
	for _, forwarded := range []string{"203.0.113.1", "203.0.113.2"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/pastes", strings.NewReader(body))
		request.RemoteAddr = "198.51.100.8:1234"
		request.Header.Set("X-Forwarded-For", forwarded)
		handler.ServeHTTP(recorder, request)
		if forwarded == "203.0.113.1" && recorder.Code != http.StatusCreated {
			t.Fatalf("first status = %d", recorder.Code)
		}
		if forwarded == "203.0.113.2" {
			assertError(t, recorder, http.StatusTooManyRequests, "rate_limited")
		}
	}
}

type fakePasteService struct {
	created              paste.Paste
	encryptedCreated     paste.Paste
	result               paste.Paste
	input                paste.CreatePlaintextInput
	createErr            error
	getErr               error
	consumed             paste.Paste
	consumeErr           error
	consumeCalls         int
	createCalls          int
	encryptedCreateCalls int
}

func (s *fakePasteService) CreateEncrypted(_ context.Context, input paste.CreateEncryptedInput) (paste.Paste, error) {
	s.encryptedCreateCalls++
	if s.createErr != nil {
		return paste.Paste{}, s.createErr
	}
	return s.encryptedCreated, nil
}

func (s *fakePasteService) CreatePlaintext(_ context.Context, input paste.CreatePlaintextInput) (paste.Paste, error) {
	s.createCalls++
	s.input = input
	if s.createErr != nil {
		return paste.Paste{}, s.createErr
	}
	return s.created, nil
}

func (s *fakePasteService) GetActive(context.Context, string) (paste.Paste, error) {
	if s.getErr != nil {
		return paste.Paste{}, s.getErr
	}
	return s.result, nil
}

func (s *fakePasteService) Consume(context.Context, string) (paste.Paste, error) {
	s.consumeCalls++
	if s.consumeErr != nil {
		return paste.Paste{}, s.consumeErr
	}
	return s.consumed, nil
}

func testPaste() paste.Paste {
	created := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	return paste.Paste{Slug: "quietbrightotter", Payload: paste.PlaintextPayload{Version: 1, Title: "Example", Language: "go", Content: "package main\n"}, ContentSize: 13, CreatedAt: created, ExpiresAt: created.Add(time.Hour)}
}

func testEncryptedPaste() paste.Paste {
	created := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	envelope := paste.CiphertextEnvelope{Version: paste.CryptoVersion, Algorithm: paste.CryptoAlgorithm, IV: "AAECAwQFBgcICQoL", Ciphertext: "AAECAwQFBgcICQoLDA0ODw"}
	return paste.Paste{Slug: "quietbrightotter", Envelope: &envelope, IsEncrypted: true, CryptoVersion: paste.CryptoVersion, ContentSize: 16, CreatedAt: created, ExpiresAt: created.Add(time.Hour)}
}
