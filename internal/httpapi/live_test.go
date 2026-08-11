package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0atxl/0xbin/internal/config"
	"github.com/0atxl/0xbin/internal/live"
	"github.com/0atxl/0xbin/internal/livecollab"
	"github.com/0atxl/0xbin/internal/ratelimit"
	"github.com/0atxl/0xbin/internal/storage/sqlite"
	"github.com/coder/websocket"
)

type testLiveSlugGenerator struct {
	mu    sync.Mutex
	slugs []string
	next  int
}

type failLiveCommitsStore struct {
	live.RoomStore
	mu        sync.Mutex
	remaining int
}

type failLiveLockStore struct {
	live.RoomStore
}

func (store *failLiveLockStore) SetRoomLocked(context.Context, string, bool, time.Time) error {
	return errors.New("injected live lock failure")
}

func (store *failLiveCommitsStore) CommitChange(ctx context.Context, commit live.ChangeCommit, now time.Time) error {
	store.mu.Lock()
	fail := store.remaining > 0
	if store.remaining > 0 {
		store.remaining--
	}
	store.mu.Unlock()
	if fail {
		return errors.New("injected live commit failure")
	}
	return store.RoomStore.CommitChange(ctx, commit, now)
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
	return newLiveTestHandlerWithConfig(t, cfgURL, func(*config.Config) {})
}

func newLiveTestHandlerWithConfig(t *testing.T, cfgURL string, configure func(*config.Config)) (http.Handler, *sqlite.Store, *live.Hub) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(t)
	configure(&cfg)
	parsed, err := url.Parse(cfgURL)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	cfg.BaseURL = parsed
	options := live.DefaultHubOptions()
	options.MaxTabs = cfg.LiveMaxTabs
	options.MaxBytes = cfg.LiveMaxBytes
	options.MaxWriters = cfg.LiveMaxWriters
	options.MaxViewers = cfg.LiveMaxViewers
	options.MaxParticipants = cfg.LiveMaxParticipants
	options.MaxMessageBytes = cfg.LiveMaxMessageBytes
	options.HeartbeatInterval = cfg.LiveHeartbeatInterval
	options.ReconnectGrace = cfg.LiveReconnectGrace
	options.ParticipantTimeout = cfg.LiveParticipantTimeout
	hub, err := live.NewHub(store, nil, options)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
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
	creatorCookie := cookieNamed(create.Result().Cookies(), liveCreatorCookie)
	if creatorCookie == nil || !creatorCookie.HttpOnly || creatorCookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("creator cookie = %+v", creatorCookie)
	}
	creatorCapability, err := live.ParseCreatorCapability(creatorCookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	persistedRoom, err := store.GetRoomSnapshot(context.Background(), created.Slug, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if !creatorCapability.MatchesRoomHash(created.Slug, persistedRoom.CreatorTokenHash) {
		t.Fatal("stored creator hash does not match the creation cookie")
	}
	if bytes.Equal(persistedRoom.CreatorTokenHash, []byte(creatorCookie.Value)) || strings.Contains(create.Body.String(), creatorCookie.Value) {
		t.Fatal("raw creator token escaped the HttpOnly cookie boundary")
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
	if snapshot.MaxBytes != 1<<20 || snapshot.MaxDocumentBytes != snapshot.MaxBytes || snapshot.MaxTabs != 8 || snapshot.MaxWriters != 10 || snapshot.MaxViewers != 100 || snapshot.MaxParticipants != 110 || snapshot.RoomLifetimeSeconds != int64((24*time.Hour).Seconds()) {
		t.Fatalf("operator live limits = %+v", snapshot)
	}
	if strings.Contains(bootstrap.Body.String(), `"participants"`) {
		t.Fatalf("pre-join bootstrap unexpectedly included transient participants: %s", bootstrap.Body.String())
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
	if len(cookies) != 2 || cookieNamed(cookies, liveSessionCookie) == nil || cookieNamed(cookies, liveCreatorCookie) == nil || !cookieNamed(cookies, liveSessionCookie).HttpOnly || !cookieNamed(cookies, liveCreatorCookie).HttpOnly || cookieNamed(cookies, liveSessionCookie).SameSite != http.SameSiteStrictMode || cookieNamed(cookies, liveCreatorCookie).SameSite != http.SameSiteStrictMode {
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
	if strings.Contains(unlocked.Body.String(), `"participants"`) {
		t.Fatalf("pre-join unlock unexpectedly included transient participants: %s", unlocked.Body.String())
	}
}

func TestLiveBootstrapReturnsAcceptedOperationsForHTTPReconciliation(t *testing.T) {
	handler, store, hub := newLiveTestHandler(t, "http://localhost:8080")
	defer store.Close()
	defer hub.Shutdown(context.Background(), time.Now().UTC())

	create := httptest.NewRecorder()
	handler.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/api/v1/live", strings.NewReader(`{"documents":[{"name":"main","language":"plaintext","content":"hello"}]}`)))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", create.Code, create.Body.String())
	}
	now := time.Now().UTC()
	joined, err := hub.Join(context.Background(), "calmbrightotter", "reconcile-session", now)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := livecollab.ParseChangeSetJSON([]byte(`[5,[0,"!"]]`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := joined.Session.SubmitDocument(context.Background(), live.DocumentOperation{
		OperationID: "committed-without-ack", ClientID: "reconcile-client",
		DocumentID: "main", BaseVersion: 0,
		Changes: changes,
	}, now); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/live/calmbrightotter", nil)
	request.Header.Set("X-0xbin-Live-Client-ID", "reconcile-client")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d: %s", response.Code, response.Body.String())
	}
	var snapshot liveRoomResponse
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot.AcceptedOperationIDs, []string{"committed-without-ack"}) {
		t.Fatalf("accepted operation IDs = %#v", snapshot.AcceptedOperationIDs)
	}
	if len(snapshot.Documents) != 1 || snapshot.Documents[0].Content != "hello!" || snapshot.Documents[0].Revision != 1 {
		t.Fatalf("authoritative bootstrap = %#v", snapshot.Documents)
	}
}

func TestLiveConfigExposesConfiguredPublicLimits(t *testing.T) {
	for _, maxBytes := range []int64{512 << 10, 2 << 20} {
		t.Run(strconv.FormatInt(maxBytes, 10), func(t *testing.T) {
			handler, store, hub := newLiveTestHandlerWithConfig(t, "http://localhost:8080", func(cfg *config.Config) {
				cfg.LiveMaxBytes = maxBytes
			})
			defer store.Close()
			defer hub.Shutdown(context.Background(), time.Now().UTC())

			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/live/config", nil))
			if response.Code != http.StatusOK {
				t.Fatalf("config status = %d: %s", response.Code, response.Body.String())
			}
			var got liveConfigResponse
			if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.MaxBytes != maxBytes || got.MaxDocumentBytes != got.MaxBytes || got.MaxTabs != 8 || got.MaxWriters != 10 || got.MaxViewers != 100 || got.MaxParticipants != 110 || got.RoomLifetimeSeconds != int64((24*time.Hour).Seconds()) {
				t.Fatalf("public live config = %+v", got)
			}
		})
	}
}

func TestLiveCreateUsesConfiguredDocumentAndRoomLimits(t *testing.T) {
	tests := []struct {
		name      string
		maxBytes  int64
		documents []int
		want      int
	}{
		{name: "smaller configured document limit accepts boundary", maxBytes: 512 << 10, documents: []int{512 << 10}, want: http.StatusCreated},
		{name: "smaller configured document limit rejects overflow", maxBytes: 512 << 10, documents: []int{(512 << 10) + 1}, want: http.StatusRequestEntityTooLarge},
		{name: "smaller configured aggregate limit rejects individually valid documents", maxBytes: 512 << 10, documents: []int{(256 << 10) + 1, (256 << 10) + 1}, want: http.StatusRequestEntityTooLarge},
		{name: "larger configured limit accepts a document above former default", maxBytes: 2 << 20, documents: []int{(1 << 20) + 1}, want: http.StatusCreated},
		{name: "larger configured document limit rejects overflow", maxBytes: 2 << 20, documents: []int{(2 << 20) + 1}, want: http.StatusRequestEntityTooLarge},
		{name: "larger configured aggregate limit rejects individually valid documents", maxBytes: 2 << 20, documents: []int{(1 << 20) + 1, (1 << 20) + 1}, want: http.StatusRequestEntityTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, store, hub := newLiveTestHandlerWithConfig(t, "http://localhost:8080", func(cfg *config.Config) {
				cfg.LiveMaxBytes = test.maxBytes
			})
			defer store.Close()
			defer hub.Shutdown(context.Background(), time.Now().UTC())
			documents := make([]string, 0, len(test.documents))
			for index, size := range test.documents {
				documents = append(documents, `{"name":`+strconv.Quote("tab-"+strconv.Itoa(index))+`,"language":"plaintext","content":`+strconv.Quote(strings.Repeat("x", size))+`}`)
			}
			body := `{"documents":[` + strings.Join(documents, ",") + `]}`
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/live", strings.NewReader(body)))
			if response.Code != test.want {
				t.Fatalf("create status = %d: %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestLiveOperationErrorsClassifyRecoveryPaths(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		code   string
		status string
	}{
		{name: "persistence is retryable", err: live.ErrPersistence, code: "service_unavailable", status: "retryable"},
		{name: "revision conflict resynchronizes", err: livecollab.ErrRevisionConflict, code: "resync_required", status: "resync_required"},
		{name: "invalid request is terminal", err: live.ErrOperationConflict, code: "invalid_request", status: "validation"},
		{name: "legacy participant removal is unsupported", err: errLiveUnsupportedOperation, code: "unsupported_operation", status: "validation"},
		{name: "content or tab limit is overload", err: live.ErrRoomLimit, code: "room_limit_reached", status: "overloaded"},
		{name: "room limit is overload", err: live.ErrParticipantLimit, code: "room_limit_reached", status: "overloaded"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code, status := classifyLiveOperationError(test.err)
			if code != test.code || status != test.status {
				t.Fatalf("classification = (%q, %q), want (%q, %q)", code, status, test.code, test.status)
			}
		})
	}
}

func TestLiveBootstrapEscapesStoredHostileText(t *testing.T) {
	handler, store, hub := newLiveTestHandler(t, "http://localhost:8080")
	defer store.Close()
	defer hub.Shutdown(context.Background(), time.Now().UTC())
	const hostile = `<img src=x onerror="window.__liveXSS=true"><script>window.__liveXSS=true</script>`
	create := httptest.NewRecorder()
	handler.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/api/v1/live", strings.NewReader(`{"documents":[{"name":"main","language":"plaintext","content":`+strconv.Quote(hostile)+`}]}`)))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", create.Code, create.Body.String())
	}
	bootstrap := httptest.NewRecorder()
	handler.ServeHTTP(bootstrap, httptest.NewRequest(http.MethodGet, "/api/v1/live/calmbrightotter", nil))
	if bootstrap.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d: %s", bootstrap.Code, bootstrap.Body.String())
	}
	if strings.Contains(bootstrap.Body.String(), "<script>") || strings.Contains(bootstrap.Body.String(), "<img ") {
		t.Fatalf("live bootstrap emitted hostile HTML directly: %s", bootstrap.Body.String())
	}
	var snapshot liveRoomResponse
	if err := json.Unmarshal(bootstrap.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Documents) != 1 || snapshot.Documents[0].Content != hostile {
		t.Fatalf("stored hostile content did not round-trip as data: %#v", snapshot.Documents)
	}
}

func TestLivePasswordWorkIsBoundForCreationAndVerification(t *testing.T) {
	api := &liveAPI{passwordSlots: make(chan struct{}, 1)}
	api.passwordSlots <- struct{}{}
	defer func() { <-api.passwordSlots }()

	if _, err := api.hashPassword("correct horse"); !errors.Is(err, errLivePasswordBusy) {
		t.Fatalf("protected creation error = %v, want busy", err)
	}
	if _, err := api.verifyPassword("correct horse", "not-a-password-hash"); !errors.Is(err, errLivePasswordBusy) {
		t.Fatalf("password verification error = %v, want busy", err)
	}
}

func TestLiveSessionCookiesRemainScopedToEachRoom(t *testing.T) {
	baseURL, err := url.Parse("https://0xbin.test")
	if err != nil {
		t.Fatal(err)
	}
	api := &liveAPI{baseURL: baseURL, sessions: newLiveSessionStore()}
	now := time.Now().UTC()
	first := httptest.NewRecorder()
	api.setSessionCookie(first, "calmbrightotter", now)
	second := httptest.NewRecorder()
	api.setSessionCookie(second, "quietbrightotter", now)
	firstCookie := first.Result().Cookies()[0]
	secondCookie := second.Result().Cookies()[0]
	if firstCookie.Path != "/api/v1/live/calmbrightotter" || secondCookie.Path != "/api/v1/live/quietbrightotter" {
		t.Fatalf("room cookie paths = %q, %q", firstCookie.Path, secondCookie.Path)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	firstURL := baseURL.ResolveReference(&url.URL{Path: "/api/v1/live/calmbrightotter"})
	secondURL := baseURL.ResolveReference(&url.URL{Path: "/api/v1/live/quietbrightotter"})
	jar.SetCookies(baseURL, []*http.Cookie{firstCookie, secondCookie})
	firstRequest := httptest.NewRequest(http.MethodGet, firstURL.String(), nil)
	for _, cookie := range jar.Cookies(firstURL) {
		firstRequest.AddCookie(cookie)
	}
	secondRequest := httptest.NewRequest(http.MethodGet, secondURL.String(), nil)
	for _, cookie := range jar.Cookies(secondURL) {
		secondRequest.AddCookie(cookie)
	}
	if !api.sessionAuthorized(firstRequest, "calmbrightotter", now) || !api.sessionAuthorized(secondRequest, "quietbrightotter", now) {
		t.Fatal("independent protected-room sessions were not both authorized")
	}
}

func TestLiveCreatorCredentialSurvivesRestartAndRejectsInvalidCookies(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	store, err := sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	capability, err := live.NewCreatorCapability()
	if err != nil {
		t.Fatal(err)
	}
	room := live.RoomSnapshot{
		Slug: "calmbrightotter", CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour),
		CreatorTokenHash: capability.HashForRoom("calmbrightotter"),
		Documents:        []live.DocumentSnapshot{{ID: "main", Name: "main", Language: "plaintext", Content: "", UpdatedAt: now}},
	}
	if err := store.CreateRoom(ctx, room); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	reopened, err := store.GetRoomSnapshot(ctx, room.Slug, now)
	if err != nil {
		t.Fatal(err)
	}
	baseURL, err := url.Parse("https://0xbin.test")
	if err != nil {
		t.Fatal(err)
	}
	api := &liveAPI{baseURL: baseURL, sessions: newLiveSessionStore()}
	accessToken := api.sessions.put(room.Slug, now.Add(liveSessionLifetime))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/live/"+room.Slug, nil)
	request.AddCookie(&http.Cookie{Name: liveSessionCookie, Value: accessToken})
	request.AddCookie(&http.Cookie{Name: liveCreatorCookie, Value: capability.CookieValue()})
	later := now.Add(liveSessionLifetime + time.Minute)
	if api.sessionAuthorized(request, room.Slug, later) {
		t.Fatal("ordinary access session survived its configured lifetime")
	}
	got, ok := api.creatorCapability(request, reopened, later)
	if !ok {
		t.Fatal("creator credential should survive access expiry and store reopen")
	}
	hub, err := live.NewHub(store, nil, live.DefaultHubOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Shutdown(ctx, later)
	joined, err := hub.JoinWithCreator(ctx, room.Slug, "creator-after-renewal", got, later)
	if err != nil || !joined.Session.IsCreator() {
		t.Fatalf("creator reconnect = %#v, %v", joined, err)
	}

	wrong, err := live.NewCreatorCapability()
	if err != nil {
		t.Fatal(err)
	}
	for name, token := range map[string]string{"missing": "", "malformed": "not-base64", "wrong": wrong.CookieValue()} {
		t.Run(name, func(t *testing.T) {
			candidate := httptest.NewRequest(http.MethodGet, "/api/v1/live/"+room.Slug, nil)
			if token != "" {
				candidate.AddCookie(&http.Cookie{Name: liveCreatorCookie, Value: token})
			}
			if _, ok := api.creatorCapability(candidate, reopened, later); ok {
				t.Fatalf("%s creator cookie acquired authority", name)
			}
		})
	}
	crossRoom := reopened
	crossRoom.Slug = "quietbrightotter"
	if _, ok := api.creatorCapability(request, crossRoom, later); ok {
		t.Fatal("creator cookie crossed room boundary")
	}
	expired := reopened
	expired.ExpiresAt = later
	if _, ok := api.creatorCapability(request, expired, later); ok {
		t.Fatal("creator cookie survived room expiry")
	}
}

func TestConcurrentLiveRoomCreationDoesNotCrossBindCreatorTokens(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	slugs := []string{"calmbrightotter", "quietquickwren", "briskcleverfox", "steadykindbadger", "gentleswiftotter", "brightcalmwren", "cleverquietfox", "kindsteadybadger"}
	type createdRoom struct {
		slug       string
		capability live.CreatorCapability
	}
	created := make(chan createdRoom, len(slugs))
	errorsSeen := make(chan error, len(slugs))
	var wait sync.WaitGroup
	for _, slugValue := range slugs {
		slugValue := slugValue
		wait.Add(1)
		go func() {
			defer wait.Done()
			capability, err := live.NewCreatorCapability()
			if err != nil {
				errorsSeen <- err
				return
			}
			snapshot := live.RoomSnapshot{CreatedAt: now, ExpiresAt: now.Add(time.Hour), Documents: []live.DocumentSnapshot{{ID: "main", Name: "main", Language: "plaintext", UpdatedAt: now}}}
			gotSlug, err := slugInsertRoom(ctx, &testLiveSlugGenerator{slugs: []string{slugValue}}, store, snapshot, capability)
			if err != nil {
				errorsSeen <- err
				return
			}
			created <- createdRoom{slug: gotSlug, capability: capability}
		}()
	}
	wait.Wait()
	close(created)
	close(errorsSeen)
	for err := range errorsSeen {
		t.Fatal(err)
	}
	rooms := make([]createdRoom, 0, len(slugs))
	for result := range created {
		rooms = append(rooms, result)
	}
	if len(rooms) != len(slugs) {
		t.Fatalf("created rooms = %d, want %d", len(rooms), len(slugs))
	}
	for _, result := range rooms {
		snapshot, err := store.GetRoomSnapshot(ctx, result.slug, now)
		if err != nil {
			t.Fatal(err)
		}
		if !result.capability.MatchesRoomHash(result.slug, snapshot.CreatorTokenHash) {
			t.Fatalf("creator token was not bound to %q", result.slug)
		}
		for _, other := range rooms {
			if other.slug != result.slug && other.capability.MatchesRoomHash(result.slug, snapshot.CreatorTokenHash) {
				t.Fatalf("creator token for %q acquired authority over %q", other.slug, result.slug)
			}
		}
	}
}

func TestLiveProtectedRoomSessionsCoexistInOneBrowser(t *testing.T) {
	placeholder := httptest.NewUnstartedServer(http.NotFoundHandler())
	handler, store, hub := newLiveTestHandler(t, "http://"+placeholder.Listener.Addr().String())
	placeholder.Config.Handler = handler
	placeholder.Start()
	defer placeholder.Close()
	defer store.Close()
	defer hub.Shutdown(context.Background(), time.Now().UTC())

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	for _, name := range []string{"first", "second"} {
		response, err := client.Post(placeholder.URL+"/api/v1/live", "application/json", strings.NewReader(`{"password":"correct horse","documents":[{"name":"`+name+`","language":"plaintext","content":"secret"}]}`))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("protected room %q create status = %d", name, response.StatusCode)
		}
	}
	for _, slugValue := range []string{"calmbrightotter", "quietbrightotter"} {
		response, err := client.Get(placeholder.URL + "/api/v1/live/" + slugValue)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("protected room %q bootstrap status = %d, want 200", slugValue, response.StatusCode)
		}
	}
}

func TestLiveWebSocketRejectsInvalidOriginUnauthorizedAccessAndOversizedFrames(t *testing.T) {
	placeholder := httptest.NewUnstartedServer(http.NotFoundHandler())
	handler, store, hub := newLiveTestHandler(t, "http://"+placeholder.Listener.Addr().String())
	placeholder.Config.Handler = handler
	placeholder.Start()
	defer placeholder.Close()
	defer store.Close()
	defer hub.Shutdown(context.Background(), time.Now().UTC())

	protected, err := placeholder.Client().Post(placeholder.URL+"/api/v1/live", "application/json", strings.NewReader(`{"password":"correct horse","documents":[{"name":"main","language":"plaintext","content":""}]}`))
	if err != nil {
		t.Fatal(err)
	}
	protected.Body.Close()
	if protected.StatusCode != http.StatusCreated {
		t.Fatalf("protected create status = %d", protected.StatusCode)
	}
	wsURL := "ws" + strings.TrimPrefix(placeholder.URL, "http") + "/api/v1/live/calmbrightotter/ws"
	_, handshake, err := websocket.Dial(context.Background(), wsURL, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{"http://attacker.invalid"}}})
	if err == nil || handshake == nil || handshake.StatusCode != http.StatusForbidden {
		t.Fatalf("invalid Origin dial = %v, status = %v", err, handshake)
	}

	unauthorized, handshake, err := websocket.Dial(context.Background(), wsURL, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{placeholder.URL}}})
	if err == nil || unauthorized != nil || handshake == nil || handshake.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized websocket dial = %v, connection = %v, status = %v", err, unauthorized, handshake)
	}

	created, err := placeholder.Client().Post(placeholder.URL+"/api/v1/live", "application/json", strings.NewReader(`{"documents":[{"name":"main","language":"plaintext","content":""}]}`))
	if err != nil {
		t.Fatal(err)
	}
	created.Body.Close()
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("unprotected create status = %d", created.StatusCode)
	}
	wsURL = "ws" + strings.TrimPrefix(placeholder.URL, "http") + "/api/v1/live/quietbrightotter/ws"
	missingSession := dialLivePeer(t, wsURL, placeholder.URL, nil)
	writeLiveMessage(t, missingSession, `{"type":"join","client_id":"legacy-client-only","metadata_revision":0,"document_revisions":[{"document_id":"main","revision":0}]}`)
	readCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	_, _, err = missingSession.Read(readCtx)
	cancel()
	if websocket.CloseStatus(err) != websocket.StatusPolicyViolation || !strings.Contains(err.Error(), "invalid participant credential") {
		t.Fatalf("missing session ID close = %v, status = %d", err, websocket.CloseStatus(err))
	}
	for _, test := range []struct {
		name   string
		field  string
		value  string
		reason string
	}{
		{"empty connection", "connection_id", "", "invalid connection ID"},
		{"connection control", "connection_id", "connection\nvalue", "invalid connection ID"},
		{"long client", "client_id", strings.Repeat("c", live.MaxClientIDBytes+1), "invalid client ID"},
		{"empty preferred name", "preferred_name", "", "invalid preferred name"},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := dialLivePeer(t, wsURL, placeholder.URL, nil)
			payload, err := json.Marshal(map[string]any{
				"type": "join", "session_id": "valid-session", test.field: test.value,
				"metadata_revision":  0,
				"document_revisions": []map[string]any{{"document_id": "main", "revision": 0}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := candidate.Write(context.Background(), websocket.MessageText, payload); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			_, _, err = candidate.Read(ctx)
			cancel()
			if websocket.CloseStatus(err) != websocket.StatusPolicyViolation || !strings.Contains(err.Error(), test.reason) {
				t.Fatalf("close = %v, status = %d", err, websocket.CloseStatus(err))
			}
		})
	}

	peer := dialLivePeer(t, wsURL, placeholder.URL, nil)
	defer peer.CloseNow()
	joinLivePeer(t, peer, "oversized-frame")
	if err := peer.Write(context.Background(), websocket.MessageText, []byte(strings.Repeat("x", (64<<10)+1))); err != nil {
		t.Fatal(err)
	}
	readCtx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _, err = peer.Read(readCtx)
	if websocket.CloseStatus(err) != websocket.StatusMessageTooBig {
		t.Fatalf("oversized frame close = %v, status = %d", err, websocket.CloseStatus(err))
	}
}

func TestLiveWebSocketAdditiveJoinIdentityAndLegacyFallback(t *testing.T) {
	placeholder := httptest.NewUnstartedServer(http.NotFoundHandler())
	handler, store, hub := newLiveTestHandler(t, "http://"+placeholder.Listener.Addr().String())
	placeholder.Config.Handler = handler
	placeholder.Start()
	defer placeholder.Close()
	defer store.Close()
	defer hub.Shutdown(context.Background(), time.Now().UTC())

	created, err := placeholder.Client().Post(placeholder.URL+"/api/v1/live", "application/json", strings.NewReader(`{"documents":[{"name":"main","language":"plaintext","content":""}]}`))
	if err != nil {
		t.Fatal(err)
	}
	created.Body.Close()
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", created.StatusCode)
	}
	wsURL := "ws" + strings.TrimPrefix(placeholder.URL, "http") + "/api/v1/live/calmbrightotter/ws"
	legacy := dialLivePeer(t, wsURL, placeholder.URL, nil)
	defer legacy.CloseNow()
	legacyJoined := joinLivePeer(t, legacy, "legacy-session")
	legacyParticipant, _ := legacyJoined["participant"].(map[string]any)
	if legacyJoined["connection_id"] != "legacy-session" || legacyParticipant["connection_count"] != float64(1) || legacyParticipant["access_class"] != "collaborator" || legacyParticipant["can_edit"] != true || legacyParticipant["role"] != "writer" {
		t.Fatalf("legacy joined event = %#v", legacyJoined)
	}

	modern := dialLivePeer(t, wsURL, placeholder.URL, nil)
	defer modern.CloseNow()
	writeLiveMessage(t, modern, `{"type":"join","session_id":"browser-credential","connection_id":"connection-one","client_id":"operation-client-one","preferred_name":"Quiet Otter","metadata_revision":0,"document_revisions":[{"document_id":"main","revision":0}]}`)
	modernJoined := readLiveEvent(t, modern, "joined")
	modernParticipant, _ := modernJoined["participant"].(map[string]any)
	if modernJoined["connection_id"] != "connection-one" || modernParticipant["nickname"] != "Quiet Otter" || modernParticipant["connection_count"] != float64(1) || modernParticipant["access_class"] != "collaborator" || modernParticipant["can_edit"] != true || modernParticipant["role"] != "writer" {
		t.Fatalf("modern joined event = %#v", modernJoined)
	}
	presence := readLiveEvent(t, legacy, "presence_joined")
	if presence["connection_id"] != "connection-one" {
		t.Fatalf("connection-specific presence = %#v", presence)
	}
	writeLiveMessage(t, modern, `{"type":"push_changes","operation_id":"modern-edit","document_id":"main","base_version":0,"changes":[[0,"hello"]]}`)
	accepted := readLiveEvent(t, modern, "changes")
	if accepted["client_id"] != "operation-client-one" || accepted["revision"] != float64(1) {
		t.Fatalf("connection-scoped operation event = %#v", accepted)
	}
	writeLiveMessage(t, modern, `{"type":"push_changes","operation_id":"wrong-client-edit","client_id":"different-client","document_id":"main","base_version":1,"changes":[[5,[0,"!"]]]}`)
	rejected := readLiveEvent(t, modern, "error")
	if rejected["code"] != "invalid_request" || rejected["status"] != "validation" {
		t.Fatalf("mismatched operation client response = %#v", rejected)
	}

	writeLiveMessage(t, modern, `{"type":"retired_operation"}`)
	unsupported := readLiveEvent(t, modern, "error")
	if unsupported["code"] != "unsupported_operation" || unsupported["status"] != "validation" {
		t.Fatalf("unsupported operation response = %#v", unsupported)
	}
}

func TestLiveWebSocketGroupsConnectionsAndPublishesAggregatePresence(t *testing.T) {
	placeholder := httptest.NewUnstartedServer(http.NotFoundHandler())
	handler, store, hub := newLiveTestHandler(t, "http://"+placeholder.Listener.Addr().String())
	placeholder.Config.Handler = handler
	placeholder.Start()
	defer placeholder.Close()
	defer store.Close()
	defer hub.Shutdown(context.Background(), time.Now().UTC())

	created, err := placeholder.Client().Post(placeholder.URL+"/api/v1/live", "application/json", strings.NewReader(`{"documents":[{"name":"main","language":"plaintext","content":"hello"},{"name":"notes","language":"plaintext","content":"notes"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if created.StatusCode != http.StatusCreated {
		created.Body.Close()
		t.Fatalf("create status = %d", created.StatusCode)
	}
	created.Body.Close()
	bootstrap, err := placeholder.Client().Get(placeholder.URL + "/api/v1/live/calmbrightotter")
	if err != nil {
		t.Fatal(err)
	}
	defer bootstrap.Body.Close()
	var room liveRoomResponse
	if err := json.NewDecoder(bootstrap.Body).Decode(&room); err != nil {
		t.Fatal(err)
	}
	if len(room.Documents) != 2 {
		t.Fatalf("created documents = %#v", room.Documents)
	}
	mainID, notesID := room.Documents[0].ID, room.Documents[1].ID
	wsURL := "ws" + strings.TrimPrefix(placeholder.URL, "http") + "/api/v1/live/calmbrightotter/ws"
	observer := dialLivePeer(t, wsURL, placeholder.URL, nil)
	defer observer.CloseNow()
	joinLivePeer(t, observer, "observer")

	first := dialLivePeer(t, wsURL, placeholder.URL, nil)
	defer first.CloseNow()
	writeLiveMessage(t, first, `{"type":"join","session_id":"shared-browser","connection_id":"tab-one","client_id":"client-one","metadata_revision":0,"document_revisions":[{"document_id":"`+mainID+`","revision":0},{"document_id":"`+notesID+`","revision":0}]}`)
	firstJoined := readLiveEvent(t, first, "joined")
	firstParticipant, _ := firstJoined["participant"].(map[string]any)
	if firstParticipant["connection_count"] != float64(1) {
		t.Fatalf("first connection participant = %#v", firstParticipant)
	}
	readLiveEvent(t, observer, "presence_joined")

	second := dialLivePeer(t, wsURL, placeholder.URL, nil)
	defer second.CloseNow()
	writeLiveMessage(t, second, `{"type":"join","session_id":"shared-browser","connection_id":"tab-two","client_id":"client-two","metadata_revision":0,"document_revisions":[{"document_id":"`+mainID+`","revision":0},{"document_id":"`+notesID+`","revision":0}]}`)
	secondJoined := readLiveEvent(t, second, "joined")
	secondParticipant, _ := secondJoined["participant"].(map[string]any)
	if secondParticipant["id"] != firstParticipant["id"] || secondParticipant["connection_count"] != float64(2) {
		t.Fatalf("second grouped connection = %#v", secondJoined)
	}
	joinedUpdate := readLiveEvent(t, first, "presence_joined")
	joinedParticipant, _ := joinedUpdate["participant"].(map[string]any)
	if joinedUpdate["connection_id"] != "tab-two" || joinedParticipant["connection_count"] != float64(2) {
		t.Fatalf("grouped presence join = %#v", joinedUpdate)
	}
	readLiveEvent(t, observer, "presence_joined")

	writeLiveMessage(t, first, `{"type":"presence","current_tab":"`+notesID+`","document_id":"`+notesID+`","revision":0,"anchor":1,"head":3}`)
	firstPresence := readLiveEvent(t, observer, "presence_updated")
	firstPresenceParticipant, _ := firstPresence["participant"].(map[string]any)
	firstCursors, _ := firstPresenceParticipant["cursors"].([]any)
	if firstPresence["connection_id"] != "tab-one" || len(firstCursors) != 1 {
		t.Fatalf("first connection presence = %#v", firstPresence)
	}
	writeLiveMessage(t, second, `{"type":"presence","current_tab":"`+mainID+`","document_id":"`+mainID+`","revision":0,"anchor":2,"head":4}`)
	secondPresence := readLiveEvent(t, observer, "presence_updated")
	secondPresenceParticipant, _ := secondPresence["participant"].(map[string]any)
	secondCursors, _ := secondPresenceParticipant["cursors"].([]any)
	if secondPresence["connection_id"] != "tab-two" || secondPresenceParticipant["current_tab"] != mainID || len(secondCursors) != 2 {
		t.Fatalf("second connection presence = %#v", secondPresence)
	}

	if err := first.Close(websocket.StatusNormalClosure, ""); err != nil {
		t.Fatal(err)
	}
	left := readLiveEvent(t, second, "presence_left")
	leftParticipant, _ := left["participant"].(map[string]any)
	leftCursors, _ := leftParticipant["cursors"].([]any)
	if left["connection_id"] != "tab-one" || leftParticipant["status"] != "connected" || leftParticipant["connection_count"] != float64(1) || len(leftCursors) != 1 {
		t.Fatalf("single connection close = %#v", left)
	}
	observerLeft := readLiveEvent(t, observer, "presence_left")
	if observerLeft["connection_id"] != "tab-one" {
		t.Fatalf("observer single connection close = %#v", observerLeft)
	}
	writeLiveMessage(t, second, `{"type":"push_changes","operation_id":"remaining-edit","document_id":"`+mainID+`","base_version":0,"changes":[5,[0,"!"]]}`)
	remainingEdit := readLiveEvent(t, second, "changes")
	if remainingEdit["client_id"] != "client-two" || remainingEdit["revision"] != float64(1) {
		t.Fatalf("remaining connection edit = %#v", remainingEdit)
	}

	if err := second.Close(websocket.StatusNormalClosure, ""); err != nil {
		t.Fatal(err)
	}
	finalLeft := readLiveEvent(t, observer, "presence_left")
	finalParticipant, _ := finalLeft["participant"].(map[string]any)
	if finalLeft["connection_id"] != "tab-two" || finalParticipant["status"] != "connection_lost" || finalParticipant["connection_count"] != float64(0) {
		t.Fatalf("final connection close = %#v", finalLeft)
	}
}

func TestLiveWebSocketRejectsNinthConnectionWithoutEvictingExistingTabs(t *testing.T) {
	placeholder := httptest.NewUnstartedServer(http.NotFoundHandler())
	handler, store, hub := newLiveTestHandler(t, "http://"+placeholder.Listener.Addr().String())
	placeholder.Config.Handler = handler
	placeholder.Start()
	defer placeholder.Close()
	defer store.Close()
	defer hub.Shutdown(context.Background(), time.Now().UTC())

	created, err := placeholder.Client().Post(placeholder.URL+"/api/v1/live", "application/json", strings.NewReader(`{"documents":[{"name":"main","language":"plaintext","content":""}]}`))
	if err != nil {
		t.Fatal(err)
	}
	created.Body.Close()
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", created.StatusCode)
	}
	wsURL := "ws" + strings.TrimPrefix(placeholder.URL, "http") + "/api/v1/live/calmbrightotter/ws"
	connections := make([]*websocket.Conn, 0, 8)
	for index := 1; index <= 8; index++ {
		connection := dialLivePeer(t, wsURL, placeholder.URL, nil)
		connections = append(connections, connection)
		connectionID := "tab-" + strconv.Itoa(index)
		writeLiveMessage(t, connection, `{"type":"join","session_id":"bounded-browser","connection_id":"`+connectionID+`","client_id":"client-`+strconv.Itoa(index)+`","metadata_revision":0,"document_revisions":[{"document_id":"main","revision":0}]}`)
		joined := readLiveEvent(t, connection, "joined")
		participant, _ := joined["participant"].(map[string]any)
		if participant["connection_count"] != float64(index) {
			t.Fatalf("connection %d join = %#v", index, joined)
		}
	}
	defer func() {
		for _, connection := range connections {
			connection.CloseNow()
		}
	}()

	overflow := dialLivePeer(t, wsURL, placeholder.URL, nil)
	defer overflow.CloseNow()
	writeLiveMessage(t, overflow, `{"type":"join","session_id":"bounded-browser","connection_id":"tab-9","client_id":"client-9","metadata_revision":0,"document_revisions":[{"document_id":"main","revision":0}]}`)
	readCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _, err = overflow.Read(readCtx)
	if websocket.CloseStatus(err) != websocket.StatusTryAgainLater || !strings.Contains(err.Error(), "connection limit reached") {
		t.Fatalf("ninth connection close = %v, status = %d", err, websocket.CloseStatus(err))
	}

	writeLiveMessage(t, connections[0], `{"type":"heartbeat"}`)
	writeLiveMessage(t, connections[0], `{"type":"push_changes","operation_id":"still-active","document_id":"main","base_version":0,"changes":[[0,"ok"]]}`)
	accepted := readLiveEvent(t, connections[0], "changes")
	if accepted["revision"] != float64(1) {
		t.Fatalf("existing connection after overflow = %#v", accepted)
	}
}

func TestDecodeLiveWireMessageRejectsMalformedAndUnknownFields(t *testing.T) {
	for _, payload := range [][]byte{
		[]byte(`{"type":"unknown"}`),
		[]byte(`{"type":"join","unexpected":true}`),
		[]byte(`{"type":"join"} {"type":"join"}`),
	} {
		if _, err := decodeLiveWireMessage(payload); err == nil {
			t.Errorf("decodeLiveWireMessage(%s) succeeded", payload)
		}
	}
	if _, err := decodeLiveWireMessage([]byte(`{"type":"unknown"}`)); !errors.Is(err, errLiveUnsupportedOperation) {
		t.Fatalf("unknown message error = %v", err)
	}
	if _, err := decodeLiveWireMessage([]byte(`{"type":"heartbeat"}`)); err != nil {
		t.Fatalf("valid heartbeat rejected: %v", err)
	}
	if message, err := decodeLiveWireMessage([]byte(`{"type":"participant_remove","participant_id":"p-1"}`)); err != nil || message.ParticipantID != "p-1" {
		t.Fatalf("legacy participant removal decode = %#v, %v", message, err)
	}
}

func FuzzDecodeLiveWireMessage(f *testing.F) {
	f.Add([]byte(`{"type":"heartbeat"}`))
	f.Add([]byte(`{"type":"push_changes","operation_id":"op","document_id":"main","base_version":0,"changes":[0]}`))
	f.Add([]byte(`{"type":"unknown"}`))
	f.Fuzz(func(t *testing.T, payload []byte) {
		if len(payload) > 64<<10 {
			return
		}
		message, err := decodeLiveWireMessage(payload)
		if err == nil && message.Type == "" {
			t.Fatal("decoded message without a type")
		}
	})
}

func TestLivePresenceQueueDropsSupersededCursorUpdates(t *testing.T) {
	peer := &livePeer{}
	peer.queuePresence(liveWireMessage{Type: "presence", DocumentID: "main", Revision: 1, Anchor: 1, Head: 1})
	peer.queuePresence(liveWireMessage{Type: "presence", DocumentID: "main", Revision: 2, Anchor: 7, Head: 9})
	message, ok := peer.takePresence()
	if !ok || message.Revision != 2 || message.Anchor != 7 || message.Head != 9 {
		t.Fatalf("coalesced presence = %#v, %t", message, ok)
	}
	if _, ok := peer.takePresence(); ok {
		t.Fatal("superseded presence remained queued")
	}
}

func TestLiveDefaultMessageLimitsSustainTypingAcrossSharedIP(t *testing.T) {
	cfg := testConfig(t)
	rates := map[ratelimit.Category]config.Rate{
		ratelimit.LiveMessage:     cfg.LiveMessageRate,
		ratelimit.LiveMessageRoom: scaledLiveRate(cfg.LiveMessageRate, cfg.LiveMaxWriters+max(1, cfg.LiveMaxViewers/20)),
	}
	rates[ratelimit.LiveMessageIP] = scaledLiveRate(rates[ratelimit.LiveMessageRoom], 2)
	limits, err := ratelimit.NewRegistry(rates, 10_000, time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	api := &liveAPI{limits: limits}
	peers := []*livePeer{
		{api: api, slug: "calmbrightotter", participantID: "participant-a", rateIdentity: "192.0.2.10"},
		{api: api, slug: "calmbrightotter", participantID: "participant-b", rateIdentity: "192.0.2.10"},
	}
	// A 32 ms client send cadence is 1,875 messages/minute. Two sessions
	// behind one NAT must both fit below the default session/room/IP bounds.
	for index, peer := range peers {
		for message := 0; message < 1875; message++ {
			if !api.allowMessage(peer) {
				t.Fatalf("peer %d denied normal sustained typing at message %d", index, message+1)
			}
		}
	}
}

func TestLiveRateFloodNotifiesWithoutAmplifyingTheQueue(t *testing.T) {
	limits, err := ratelimit.NewRegistry(map[ratelimit.Category]config.Rate{
		ratelimit.LiveMessage:     {Count: 1, Window: time.Hour},
		ratelimit.LiveMessageRoom: {Count: 1, Window: time.Hour},
		ratelimit.LiveMessageIP:   {Count: 1, Window: time.Hour},
	}, 100, time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	peer := &livePeer{
		api:           &liveAPI{cfg: config.Config{LiveMaxMessageBytes: 1024}, limits: limits},
		slug:          "calmbrightotter",
		participantID: "participant",
		rateIdentity:  "192.0.2.10",
		out:           make(chan livePeerFrame, 2),
		done:          make(chan struct{}),
	}
	if !peer.api.allowMessage(peer) {
		t.Fatal("first message was unexpectedly rate limited")
	}
	for index := 0; index < 10; index++ {
		if peer.api.allowMessage(peer) {
			t.Fatalf("flood message %d was unexpectedly allowed", index+2)
		}
	}
	if len(peer.out) != 1 {
		t.Fatalf("rate flood queued %d notices, want one", len(peer.out))
	}
	var notice map[string]any
	if err := json.Unmarshal((<-peer.out).data, &notice); err != nil || notice["status"] != "rate_limited" {
		t.Fatalf("rate notice = %#v, %v", notice, err)
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
	if err := conn.Write(context.Background(), websocket.MessageText, []byte(`{"type":"join","session_id":"client-one","metadata_revision":0,"document_revisions":[{"document_id":"main","revision":0}]}`)); err != nil {
		t.Fatal(err)
	}
	_, joinedData, err := conn.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var joinedEvent map[string]any
	if err := json.Unmarshal(joinedData, &joinedEvent); err != nil {
		t.Fatal(err)
	}
	if joinedEvent["type"] != "joined" || joinedEvent["documents"] != nil {
		t.Fatalf("joined event should contain state but no document bodies: %s", joinedData)
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

	reader, handshake, err := websocket.Dial(context.Background(), wsURL, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{placeholder.URL}}})
	if err != nil {
		if handshake != nil {
			t.Fatalf("%v (status %d)", err, handshake.StatusCode)
		}
		t.Fatal(err)
	}
	defer reader.Close(websocket.StatusNormalClosure, "")
	if err := reader.Write(context.Background(), websocket.MessageText, []byte(`{"type":"join","session_id":"client-two","metadata_revision":0,"document_revisions":[{"document_id":"main","revision":0}]}`)); err != nil {
		t.Fatal(err)
	}
	_, joinedData, err = reader.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(joinedData, &joinedEvent); err != nil {
		t.Fatal(err)
	}
	if joinedEvent["documents"] != nil {
		t.Fatalf("joined bridge leaked full documents: %s", joinedData)
	}
	_, bridgeData, err := reader.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(bridgeData, &event); err != nil {
		t.Fatal(err)
	}
	if event["type"] != "changes" || event["revision"] != float64(1) {
		t.Fatalf("HTTP-to-WebSocket bridge event = %s", bridgeData)
	}

	resync, handshake, err := websocket.Dial(context.Background(), wsURL, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{placeholder.URL}}})
	if err != nil {
		if handshake != nil {
			t.Fatalf("%v (status %d)", err, handshake.StatusCode)
		}
		t.Fatal(err)
	}
	defer resync.Close(websocket.StatusNormalClosure, "")
	if err := resync.Write(context.Background(), websocket.MessageText, []byte(`{"type":"join","session_id":"client-three","metadata_revision":0,"document_revisions":[{"document_id":"main","revision":2}]}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := resync.Read(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, resyncData, err := resync.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(resyncData, &event); err != nil {
		t.Fatal(err)
	}
	if event["type"] != "status" || event["status"] != "http_resync_required" {
		t.Fatalf("missing explicit HTTP resync status: %s", resyncData)
	}
}

func TestLiveWebSocketPersistenceFailureIsRetryableAndNeverPublished(t *testing.T) {
	ctx := context.Background()
	placeholder := httptest.NewUnstartedServer(http.NotFoundHandler())
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	failing := &failLiveCommitsStore{RoomStore: store, remaining: 1}
	cfg := testConfig(t)
	cfg.BaseURL, err = url.Parse("http://" + placeholder.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	hub, err := live.NewHub(failing, nil, live.DefaultHubOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Shutdown(ctx, time.Now().UTC())
	placeholder.Config.Handler = NewHandlerWithLive(cfg, nil, &LiveDependencies{
		Store: failing,
		Hub:   hub,
		Slugs: &testLiveSlugGenerator{slugs: []string{"calmbrightotter"}},
	})
	placeholder.Start()
	defer placeholder.Close()

	created, err := placeholder.Client().Post(placeholder.URL+"/api/v1/live", "application/json", strings.NewReader(`{"documents":[{"name":"main","language":"plaintext","content":""}]}`))
	if err != nil {
		t.Fatal(err)
	}
	created.Body.Close()
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", created.StatusCode)
	}

	wsURL := "ws" + strings.TrimPrefix(placeholder.URL, "http") + "/api/v1/live/calmbrightotter/ws"
	peer := dialLivePeer(t, wsURL, placeholder.URL, created.Cookies()...)
	defer peer.Close(websocket.StatusNormalClosure, "")
	joinLivePeer(t, peer, "persistence-client")
	operation := `{"type":"push_changes","operation_id":"retry-persisted-change","client_id":"persistence-client","document_id":"main","base_version":0,"changes":[[0,"hello"]]}`
	writeLiveMessage(t, peer, operation)
	rejected := readLiveEvent(t, peer, "error")
	if rejected["code"] != "service_unavailable" || rejected["status"] != "retryable" || rejected["operation_id"] != "retry-persisted-change" {
		t.Fatalf("persistence failure event = %#v", rejected)
	}
	unchanged, err := store.GetRoomSnapshot(ctx, "calmbrightotter", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Documents[0].Content != "" || unchanged.Documents[0].CurrentRevision != 0 {
		t.Fatalf("failed commit reached SQLite or authority = %#v", unchanged.Documents[0])
	}

	writeLiveMessage(t, peer, operation)
	accepted := readLiveEvent(t, peer, "changes")
	if accepted["revision"] != float64(1) || accepted["operation_id"] != "retry-persisted-change" {
		t.Fatalf("retried persistence event = %#v", accepted)
	}
	persisted, err := store.GetRoomSnapshot(ctx, "calmbrightotter", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Documents[0].Content != "hello" || persisted.Documents[0].CurrentRevision != 1 {
		t.Fatalf("retried durable state = %#v", persisted.Documents[0])
	}
}

func TestLiveCreatorControlsRequireCreatorSession(t *testing.T) {
	placeholder := httptest.NewUnstartedServer(http.NotFoundHandler())
	cfgURL := "http://" + placeholder.Listener.Addr().String()
	handler, store, hub := newLiveTestHandler(t, cfgURL)
	placeholder.Config.Handler = handler
	placeholder.Start()
	defer placeholder.Close()
	defer store.Close()
	defer hub.Shutdown(context.Background(), time.Now().UTC())

	created, err := placeholder.Client().Post(placeholder.URL+"/api/v1/live", "application/json", strings.NewReader(`{"password":"correct horse","documents":[{"name":"main","language":"plaintext","content":""}]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer created.Body.Close()
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d: %s", created.StatusCode, created.Status)
	}
	cookies := created.Cookies()
	if len(cookies) != 2 || cookieNamed(cookies, liveSessionCookie) == nil || cookieNamed(cookies, liveCreatorCookie) == nil || !cookieNamed(cookies, liveSessionCookie).HttpOnly || !cookieNamed(cookies, liveCreatorCookie).HttpOnly || strings.Contains(created.Header.Get("Set-Cookie"), "correct horse") {
		t.Fatalf("creator capability must remain an HttpOnly session: %+v", cookies)
	}

	wsURL := "ws" + strings.TrimPrefix(placeholder.URL, "http") + "/api/v1/live/calmbrightotter/ws"
	creator := dialLivePeer(t, wsURL, placeholder.URL, cookies...)
	defer creator.Close(websocket.StatusNormalClosure, "")
	creatorJoined := joinLivePeer(t, creator, "creator-session")
	if creatorJoined["creator"] != true {
		t.Fatalf("creator joined without creator authority: %s", creatorJoined)
	}
	creatorParticipant, _ := creatorJoined["participant"].(map[string]any)
	if creatorParticipant == nil || creatorParticipant["role"] != "writer" || creatorParticipant["access_class"] != "creator" || creatorParticipant["can_edit"] != true || creatorParticipant["connection_count"] != float64(1) {
		t.Fatalf("creator participant = %#v", creatorParticipant)
	}

	unlock, err := placeholder.Client().Post(placeholder.URL+"/api/v1/live/calmbrightotter/unlock", "application/json", strings.NewReader(`{"password":"correct horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	unlock.Body.Close()
	writerCookies := unlock.Cookies()
	if unlock.StatusCode != http.StatusOK || len(writerCookies) != 1 {
		t.Fatalf("password session response = %d cookies=%d", unlock.StatusCode, len(writerCookies))
	}
	writer := dialLivePeer(t, wsURL, placeholder.URL, writerCookies[0])
	defer writer.Close(websocket.StatusNormalClosure, "")
	writerJoined := joinLivePeer(t, writer, "writer-session")
	if writerJoined["creator"] != false {
		t.Fatalf("ordinary password access acquired creator authority: %s", writerJoined)
	}
	writerParticipant, _ := writerJoined["participant"].(map[string]any)
	if writerParticipant == nil || writerParticipant["role"] != "writer" || writerParticipant["access_class"] != "collaborator" || writerParticipant["can_edit"] != true {
		t.Fatalf("writer participant = %#v", writerParticipant)
	}

	creatorUnlockRequest, err := http.NewRequest(http.MethodPost, placeholder.URL+"/api/v1/live/calmbrightotter/unlock", strings.NewReader(`{"password":"correct horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	creatorUnlockRequest.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		creatorUnlockRequest.AddCookie(cookie)
	}
	creatorUnlock, err := placeholder.Client().Do(creatorUnlockRequest)
	if err != nil {
		t.Fatal(err)
	}
	creatorUnlock.Body.Close()
	if creatorUnlock.StatusCode != http.StatusOK || cookieNamed(creatorUnlock.Cookies(), liveSessionCookie) == nil || cookieNamed(creatorUnlock.Cookies(), liveCreatorCookie) != nil {
		t.Fatalf("creator reauthentication response = %d cookies=%+v", creatorUnlock.StatusCode, creatorUnlock.Cookies())
	}
	creatorAfterUnlock := dialLivePeer(t, wsURL, placeholder.URL, cookieNamed(creatorUnlock.Cookies(), liveSessionCookie), cookieNamed(cookies, liveCreatorCookie))
	defer creatorAfterUnlock.Close(websocket.StatusNormalClosure, "")
	writeLiveMessage(t, creatorAfterUnlock, `{"type":"join","session_id":"creator-session","connection_id":"creator-tab-two","client_id":"creator-client-two","metadata_revision":0,"document_revisions":[{"document_id":"main","revision":0}]}`)
	creatorAfterUnlockJoined := readLiveEvent(t, creatorAfterUnlock, "joined")
	creatorAfterUnlockParticipant, _ := creatorAfterUnlockJoined["participant"].(map[string]any)
	if creatorAfterUnlockJoined["creator"] != true || creatorAfterUnlockParticipant["id"] != creatorParticipant["id"] || creatorAfterUnlockParticipant["connection_count"] != float64(2) {
		t.Fatalf("creator authority after password reauthentication = %#v", creatorAfterUnlockJoined)
	}

	writeLiveMessage(t, writer, `{"type":"room_watch_only","watch_only":true}`)
	if event := readLiveEvent(t, writer, "error"); event["code"] != "unauthorized" {
		t.Fatalf("non-creator room mode response = %#v", event)
	}
	writeLiveMessage(t, writer, `{"type":"participant_remove","participant_id":"`+creatorParticipant["id"].(string)+`"}`)
	if event := readLiveEvent(t, writer, "error"); event["code"] != "unsupported_operation" || event["status"] != "validation" {
		t.Fatalf("legacy removal response = %#v", event)
	}

	writeLiveMessage(t, creator, `{"type":"room_watch_only","watch_only":true}`)
	for name, connection := range map[string]*websocket.Conn{"creator-one": creator, "creator-two": creatorAfterUnlock, "collaborator": writer} {
		mode := readLiveEvent(t, connection, "room_mode_changed")
		if mode["watch_only"] != true || mode["locked"] != true {
			t.Fatalf("%s lock response = %#v", name, mode)
		}
		participants, _ := mode["participants"].([]any)
		for _, item := range participants {
			participant, _ := item.(map[string]any)
			if participant["id"] == creatorParticipant["id"] {
				if participant["access_class"] != "creator" || participant["can_edit"] != true || participant["role"] != "writer" {
					t.Fatalf("%s locked creator = %#v", name, participant)
				}
			} else if participant["access_class"] != "collaborator" || participant["can_edit"] != false || participant["role"] != "watch_only" {
				t.Fatalf("%s locked collaborator = %#v", name, participant)
			}
		}
	}
	writeLiveMessage(t, creator, `{"type":"push_changes","operation_id":"locked-creator-edit","document_id":"main","base_version":0,"changes":[[0,"allowed"]]}`)
	if event := readLiveEvent(t, creator, "changes"); event["revision"] != float64(1) {
		t.Fatalf("locked creator mutation = %#v", event)
	}
	writeLiveMessage(t, writer, `{"type":"push_changes","operation_id":"watcher-edit","document_id":"main","base_version":1,"changes":[7,[0,"blocked"]]}`)
	if event := readLiveEvent(t, writer, "error"); event["code"] != "unauthorized" {
		t.Fatalf("watch-only mutation response = %#v", event)
	}

	writeLiveMessage(t, creatorAfterUnlock, `{"type":"room_watch_only","watch_only":false}`)
	for name, connection := range map[string]*websocket.Conn{"creator-one": creator, "creator-two": creatorAfterUnlock, "collaborator": writer} {
		mode := readLiveEvent(t, connection, "room_mode_changed")
		if mode["watch_only"] != false || mode["locked"] != false {
			t.Fatalf("%s unlock response = %#v", name, mode)
		}
		participants, _ := mode["participants"].([]any)
		for _, item := range participants {
			participant, _ := item.(map[string]any)
			if participant["can_edit"] != true || participant["role"] != "writer" {
				t.Fatalf("%s unlocked writer = %#v", name, participant)
			}
		}
	}
	writeLiveMessage(t, writer, `{"type":"push_changes","operation_id":"writer-after-unlock","document_id":"main","base_version":1,"changes":[7,[0,"!"]]}`)
	if event := readLiveEvent(t, writer, "changes"); event["revision"] != float64(2) {
		t.Fatalf("unlocked collaborator mutation = %#v", event)
	}

	writeLiveMessage(t, creator, `{"type":"participant_remove","participant_id":"`+writerParticipant["id"].(string)+`"}`)
	if event := readLiveEvent(t, creator, "error"); event["code"] != "unsupported_operation" || event["status"] != "validation" {
		t.Fatalf("creator legacy removal response = %#v", event)
	}
	state, err := hub.State("calmbrightotter")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Participants) != 2 {
		t.Fatalf("legacy removal mutated roster = %#v", state.Participants)
	}
	readCtx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	_, data, readErr := writer.Read(readCtx)
	cancel()
	if readErr == nil {
		t.Fatalf("legacy removal broadcast unexpected event: %s", data)
	}
}

func TestLiveFailedLockPersistenceDoesNotBroadcastOrChangePermissions(t *testing.T) {
	placeholder := httptest.NewUnstartedServer(http.NotFoundHandler())
	cfg := testConfig(t)
	parsed, err := url.Parse("http://" + placeholder.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	cfg.BaseURL = parsed
	store, err := sqlite.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	failing := &failLiveLockStore{RoomStore: store}
	hub, err := live.NewHub(failing, nil, live.DefaultHubOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Shutdown(context.Background(), time.Now().UTC())
	api := newLiveAPI(cfg, &LiveDependencies{Store: failing, Hub: hub, Slugs: &testLiveSlugGenerator{}})
	placeholder.Config.Handler = newHandlerWithAPI(cfg, nil, nil, nil, api)
	placeholder.Start()
	defer placeholder.Close()

	created, err := placeholder.Client().Post(placeholder.URL+"/api/v1/live", "application/json", strings.NewReader(`{"documents":[{"name":"main","language":"plaintext","content":""}]}`))
	if err != nil {
		t.Fatal(err)
	}
	created.Body.Close()
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", created.StatusCode)
	}
	wsURL := "ws" + strings.TrimPrefix(placeholder.URL, "http") + "/api/v1/live/calmbrightotter/ws"
	observer := dialLivePeer(t, wsURL, placeholder.URL)
	defer observer.CloseNow()
	joinLivePeer(t, observer, "observer-session")
	creator := dialLivePeer(t, wsURL, placeholder.URL, created.Cookies()...)
	defer creator.CloseNow()
	creatorJoined := joinLivePeer(t, creator, "creator-session")
	creatorParticipant, _ := creatorJoined["participant"].(map[string]any)
	if creatorJoined["creator"] != true || creatorParticipant["can_edit"] != true {
		t.Fatalf("creator join = %#v", creatorJoined)
	}
	readLiveEvent(t, observer, "presence_joined")

	writeLiveMessage(t, creator, `{"type":"room_watch_only","watch_only":true}`)
	failure := readLiveEvent(t, creator, "error")
	if failure["code"] != "service_unavailable" || failure["status"] != "retryable" {
		t.Fatalf("failed lock response = %#v", failure)
	}
	readCtx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	_, data, readErr := observer.Read(readCtx)
	cancel()
	if readErr == nil {
		t.Fatalf("failed lock broadcast unexpected event: %s", data)
	}
	state, err := hub.State("calmbrightotter")
	if err != nil {
		t.Fatal(err)
	}
	if state.WatchOnly {
		t.Fatal("failed lock changed in-memory room mode")
	}
	for _, participant := range state.Participants {
		if !participant.CanEdit || participant.Role != live.ParticipantWriter {
			t.Fatalf("failed lock changed participant permission = %#v", participant)
		}
	}
	persisted, err := store.GetRoomSnapshot(context.Background(), "calmbrightotter", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Locked {
		t.Fatal("failed lock changed durable room mode")
	}
}

func TestLiveSweepDeliversExpiredStatusBeforeClose(t *testing.T) {
	placeholder := httptest.NewUnstartedServer(http.NotFoundHandler())
	cfg := testConfig(t)
	parsed, err := url.Parse("http://" + placeholder.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	cfg.BaseURL = parsed
	cfg.LiveRoomLifetime = time.Minute
	store, err := sqlite.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	hub, err := live.NewHub(store, nil, live.DefaultHubOptions())
	if err != nil {
		t.Fatal(err)
	}
	api := newLiveAPI(cfg, &LiveDependencies{Store: store, Hub: hub, Slugs: &testLiveSlugGenerator{}})
	placeholder.Config.Handler = newHandlerWithAPI(cfg, nil, nil, nil, api)
	placeholder.Start()
	defer placeholder.Close()
	defer hub.Shutdown(context.Background(), time.Now().UTC())

	created, err := placeholder.Client().Post(placeholder.URL+"/api/v1/live", "application/json", strings.NewReader(`{"documents":[{"name":"main","language":"plaintext","content":""}]}`))
	if err != nil {
		t.Fatal(err)
	}
	created.Body.Close()
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", created.StatusCode)
	}
	conn := dialLivePeer(t, "ws"+strings.TrimPrefix(placeholder.URL, "http")+"/api/v1/live/calmbrightotter/ws", placeholder.URL, created.Cookies()[0])
	defer conn.CloseNow()
	joinLivePeer(t, conn, "expiry-session")
	second := dialLivePeer(t, "ws"+strings.TrimPrefix(placeholder.URL, "http")+"/api/v1/live/calmbrightotter/ws", placeholder.URL, created.Cookies()[0])
	defer second.CloseNow()
	writeLiveMessage(t, second, `{"type":"join","session_id":"expiry-session","connection_id":"expiry-tab-two","client_id":"expiry-client-two","metadata_revision":0,"document_revisions":[{"document_id":"main","revision":0}]}`)
	secondJoined := readLiveEvent(t, second, "joined")
	secondParticipant, _ := secondJoined["participant"].(map[string]any)
	if secondParticipant["connection_count"] != float64(2) {
		t.Fatalf("expiry grouped connection = %#v", secondJoined)
	}
	if err := api.sweep(context.Background(), time.Now().UTC().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	for index, connection := range []*websocket.Conn{conn, second} {
		status := readLiveEvent(t, connection, "status")
		if status["status"] != "expired" {
			t.Fatalf("expiry status %d = %#v", index, status)
		}
		readCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, _, readErr := connection.Read(readCtx)
		cancel()
		if websocket.CloseStatus(readErr) != websocket.StatusTryAgainLater || !strings.Contains(readErr.Error(), "room expired") {
			t.Fatalf("expiry close error %d = %v, status = %d", index, readErr, websocket.CloseStatus(readErr))
		}
	}
}

func TestServerLifecycleEvictsDisconnectedRoomAndStops(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	room := live.RoomSnapshot{
		Slug: "calmbrightotter", CreatedAt: now, ExpiresAt: now.Add(time.Hour),
		Documents: []live.DocumentSnapshot{{ID: "main", Name: "main", Language: "plaintext", Position: 0, UpdatedAt: now}},
	}
	if err := store.CreateRoom(ctx, room); err != nil {
		t.Fatal(err)
	}
	options := live.DefaultHubOptions()
	options.HeartbeatInterval = 2 * time.Millisecond
	options.ReconnectGrace = 4 * time.Millisecond
	options.ParticipantTimeout = 6 * time.Millisecond
	hub, err := live.NewHub(store, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	joined, err := hub.Join(ctx, room.Slug, "one-visit", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := joined.Session.Disconnect(now); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(t)
	cfg.ListenAddr = listener.Addr().String()
	cfg.BaseURL, err = url.Parse("http://" + listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	server := NewServerWithFrontendAndLive(cfg, nil, nil, &LiveDependencies{Store: store, Hub: hub, Slugs: &testLiveSlugGenerator{}})
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	deadline := time.Now().Add(time.Second)
	for hub.RoomCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if hub.RoomCount() != 0 {
		t.Fatalf("lifecycle room count = %d, want 0", hub.RoomCount())
	}
	shutdownCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	if err := <-serveErr; !errors.Is(err, ErrServerClosed) {
		t.Fatalf("serve error = %v", err)
	}
	if _, err := hub.Join(ctx, room.Slug, "after-shutdown", time.Now().UTC()); !errors.Is(err, live.ErrHubClosed) {
		t.Fatalf("join after lifecycle shutdown error = %v", err)
	}
}

func dialLivePeer(t *testing.T, wsURL, origin string, cookies ...*http.Cookie) *websocket.Conn {
	t.Helper()
	headers := http.Header{"Origin": []string{origin}}
	values := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie != nil {
			values = append(values, cookie.Name+"="+cookie.Value)
		}
	}
	if len(values) > 0 {
		headers.Set("Cookie", strings.Join(values, "; "))
	}
	conn, handshake, err := websocket.Dial(context.Background(), wsURL, &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		if handshake != nil {
			t.Fatalf("dial live peer: %v (status %d)", err, handshake.StatusCode)
		}
		t.Fatal(err)
	}
	return conn
}

func cookieNamed(cookies []*http.Cookie, name string) *http.Cookie {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

func joinLivePeer(t *testing.T, conn *websocket.Conn, sessionID string) map[string]any {
	t.Helper()
	writeLiveMessage(t, conn, `{"type":"join","session_id":"`+sessionID+`","metadata_revision":0,"document_revisions":[{"document_id":"main","revision":0}]}`)
	return readLiveEvent(t, conn, "joined")
}

func writeLiveMessage(t *testing.T, conn *websocket.Conn, message string) {
	t.Helper()
	if err := conn.Write(context.Background(), websocket.MessageText, []byte(message)); err != nil {
		t.Fatal(err)
	}
}

func readLiveEvent(t *testing.T, conn *websocket.Conn, eventType string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		_, data, err := conn.Read(ctx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		var event map[string]any
		if err := json.Unmarshal(data, &event); err != nil {
			t.Fatal(err)
		}
		if event["type"] == eventType {
			return event
		}
	}
	t.Fatalf("timed out waiting for live event %q", eventType)
	return nil
}
