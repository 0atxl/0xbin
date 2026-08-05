package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0atxl/0xbin/internal/live"
	"github.com/0atxl/0xbin/internal/storage/sqlite"
	"github.com/coder/websocket"
)

type testLiveSlugGenerator struct {
	mu    sync.Mutex
	slugs []string
	next  int
}

func (generator *testLiveSlugGenerator) Generate() (string, error) {
	generator.mu.Lock()
	defer generator.mu.Unlock()
	if len(generator.slugs) == 0 {
		return "calmbrightotter", nil
	}
	index := generator.next
	if index >= len(generator.slugs) {
		index = len(generator.slugs) - 1
	}
	generator.next++
	return generator.slugs[index], nil
}

func newLiveTestHandler(t *testing.T, cfgURL string) (http.Handler, *sqlite.Store, *live.Hub) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	options := live.DefaultHubOptions()
	hub, err := live.NewHub(store, nil, options)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	cfg := testConfig(t)
	parsed, err := url.Parse(cfgURL)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	cfg.BaseURL = parsed
	handler := NewHandlerWithLive(cfg, nil, &LiveDependencies{Store: store, Hub: hub, Slugs: &testLiveSlugGenerator{slugs: []string{"calmbrightotter", "quietbrightotter"}}})
	return handler, store, hub
}

func TestLiveCreateBootstrapAndPasswordGate(t *testing.T) {
	handler, store, hub := newLiveTestHandler(t, "http://localhost:8080")
	defer store.Close()
	defer hub.Shutdown(context.Background(), time.Now().UTC())

	create := httptest.NewRecorder()
	handler.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/api/v1/live", strings.NewReader(`{"documents":[{"name":"main","language":"plaintext","content":"shared text"}]}`)))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", create.Code, create.Body.String())
	}
	var created liveCreateResponse
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.PasswordRequired {
		t.Fatal("unprotected room unexpectedly requires a password")
	}

	bootstrap := httptest.NewRecorder()
	handler.ServeHTTP(bootstrap, httptest.NewRequest(http.MethodGet, "/api/v1/live/calmbrightotter", nil))
	if bootstrap.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d: %s", bootstrap.Code, bootstrap.Body.String())
	}
	var snapshot liveRoomResponse
	if err := json.Unmarshal(bootstrap.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Documents) != 1 || snapshot.Documents[0].Content != "shared text" || snapshot.Documents[0].Revision != 0 || snapshot.MetadataRevision != 0 {
		t.Fatalf("unexpected bootstrap snapshot: %+v", snapshot)
	}
	if !strings.Contains(bootstrap.Body.String(), `"metadata_revision":0`) {
		t.Fatalf("bootstrap omitted zero metadata revision: %s", bootstrap.Body.String())
	}
	for _, header := range []string{"Cache-Control", "X-Robots-Tag", "X-Content-Type-Options"} {
		if bootstrap.Header().Get(header) == "" {
			t.Errorf("bootstrap missing %s", header)
		}
	}

	protectedCreate := httptest.NewRecorder()
	handler.ServeHTTP(protectedCreate, httptest.NewRequest(http.MethodPost, "/api/v1/live", strings.NewReader(`{"password":"correct horse","documents":[{"name":"main","language":"plaintext","content":"secret text"}]}`)))
	if protectedCreate.Code != http.StatusCreated {
		t.Fatalf("protected create status = %d: %s", protectedCreate.Code, protectedCreate.Body.String())
	}
	if !strings.Contains(protectedCreate.Body.String(), `"password_required":true`) || strings.Contains(protectedCreate.Body.String(), "correct horse") {
		t.Fatalf("protected create response leaked password or missed gate: %s", protectedCreate.Body.String())
	}
	cookies := protectedCreate.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("unexpected creator session cookie: %+v", cookies)
	}

	var protected liveCreateResponse
	if err := json.Unmarshal(protectedCreate.Body.Bytes(), &protected); err != nil {
		t.Fatal(err)
	}
	locked := httptest.NewRecorder()
	handler.ServeHTTP(locked, httptest.NewRequest(http.MethodGet, "/api/v1/live/"+protected.Slug, nil))
	assertError(t, locked, http.StatusUnauthorized, "password_required")
	if strings.Contains(locked.Body.String(), "secret text") {
		t.Fatal("locked bootstrap leaked room content")
	}

	wrong := httptest.NewRecorder()
	wrongRequest := httptest.NewRequest(http.MethodPost, "/api/v1/live/"+protected.Slug+"/unlock", strings.NewReader(`{"password":"wrong"}`))
	handler.ServeHTTP(wrong, wrongRequest)
	assertError(t, wrong, http.StatusUnauthorized, "invalid_password")
	if strings.Contains(wrong.Body.String(), "secret text") {
		t.Fatal("wrong password response leaked room content")
	}

	unlocked := httptest.NewRecorder()
	unlockRequest := httptest.NewRequest(http.MethodPost, "/api/v1/live/"+protected.Slug+"/unlock", strings.NewReader(`{"password":"correct horse"}`))
	handler.ServeHTTP(unlocked, unlockRequest)
	if unlocked.Code != http.StatusOK || !strings.Contains(unlocked.Body.String(), "secret text") || strings.Contains(unlocked.Body.String(), `"password_required":true`) {
		t.Fatalf("unlock response = %d: %s", unlocked.Code, unlocked.Body.String())
	}
}

func TestLiveWebSocketBridgesHubChanges(t *testing.T) {
	placeholder := httptest.NewUnstartedServer(http.NotFoundHandler())
	cfgURL := "http://" + placeholder.Listener.Addr().String()
	handler, store, hub := newLiveTestHandler(t, cfgURL)
	placeholder.Config.Handler = handler
	placeholder.Start()
	defer placeholder.Close()
	defer store.Close()
	defer hub.Shutdown(context.Background(), time.Now().UTC())

	response, err := placeholder.Client().Post(placeholder.URL+"/api/v1/live", "application/json", strings.NewReader(`{"documents":[{"name":"main","language":"plaintext","content":""}]}`))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	wsURL := "ws" + strings.TrimPrefix(placeholder.URL, "http") + "/api/v1/live/calmbrightotter/ws"
	conn, handshake, err := websocket.Dial(context.Background(), wsURL, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{placeholder.URL}}})
	if err != nil {
		if handshake != nil {
			t.Fatalf("%v (status %d)", err, handshake.StatusCode)
		}
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := conn.Write(context.Background(), websocket.MessageText, []byte(`{"type":"join","session_id":"client-one"}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.Read(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(context.Background(), websocket.MessageText, []byte(`{"type":"push_changes","operation_id":"op-one","document_id":"main","base_version":0,"changes":[[0,"hello"]]}`)); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var event map[string]any
	if err := json.Unmarshal(data, &event); err != nil {
		t.Fatal(err)
	}
	if event["type"] != "changes" || event["revision"] != float64(1) {
		t.Fatalf("unexpected change event: %s", data)
	}
	if _, exists := event["document"]; exists {
		t.Fatalf("change event included a full document: %s", data)
	}
	if got, ok := event["changes"].([]any); !ok || len(got) != 1 {
		t.Fatalf("change event omitted compact changes: %s", data)
	}
}
