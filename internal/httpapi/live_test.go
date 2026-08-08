package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
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
	if snapshot.MaxBytes != 1<<20 || snapshot.MaxTabs != 8 || snapshot.MaxWriters != 10 || snapshot.MaxViewers != 100 || snapshot.MaxParticipants != 110 || snapshot.RoomLifetimeSeconds != int64((24*time.Hour).Seconds()) {
		t.Fatalf("operator live limits = %+v", snapshot)
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
}

func TestLiveConfigExposesConfiguredPublicLimits(t *testing.T) {
	handler, store, hub := newLiveTestHandler(t, "http://localhost:8080")
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
	if got.MaxBytes != 1<<20 || got.MaxDocumentBytes != 1<<20 || got.MaxTabs != 8 || got.MaxWriters != 10 || got.MaxViewers != 100 || got.MaxParticipants != 110 || got.RoomLifetimeSeconds != int64((24*time.Hour).Seconds()) {
		t.Fatalf("public live config = %+v", got)
	}
}

func TestLiveCreateUsesConfiguredDocumentAndRoomLimits(t *testing.T) {
	tests := []struct {
		name     string
		maxBytes int64
		content  int
		want     int
	}{
		{name: "below former default accepts", maxBytes: 512 << 10, content: (512 << 10) - 1, want: http.StatusCreated},
		{name: "below former default rejects", maxBytes: 512 << 10, content: (512 << 10) + 1, want: http.StatusBadRequest},
		{name: "above former default accepts", maxBytes: 2 << 20, content: (2 << 20) - 1, want: http.StatusCreated},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, store, hub := newLiveTestHandlerWithConfig(t, "http://localhost:8080", func(cfg *config.Config) {
				cfg.LiveMaxBytes = test.maxBytes
			})
			defer store.Close()
			defer hub.Shutdown(context.Background(), time.Now().UTC())
			body := `{"documents":[{"name":"main","language":"plaintext","content":` + strconv.Quote(strings.Repeat("x", test.content)) + `}]}`
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
		{name: "session failure requires authentication", err: live.ErrSessionRemoved, code: "unauthorized", status: "auth_required"},
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

func TestLiveCreatorCredentialOutlivesAccessSessionWithoutBecomingDurable(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	room := live.RoomSnapshot{
		Slug: "calmbrightotter", CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour),
		Documents: []live.DocumentSnapshot{{ID: "main", Name: "main", Language: "plaintext", Content: "", UpdatedAt: now}},
	}
	if err := store.CreateRoom(ctx, room); err != nil {
		t.Fatal(err)
	}
	hub, err := live.NewHub(store, nil, live.DefaultHubOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Shutdown(ctx, now)
	capability, err := hub.IssueCreatorCapability(room.Slug, room.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	api := &liveAPI{sessions: newLiveSessionStore(), creators: newLiveSessionStore(), hub: hub}
	accessToken := api.sessions.put(room.Slug, now.Add(liveSessionLifetime), live.CreatorCapability{})
	creatorToken := api.creators.put(room.Slug, room.ExpiresAt, capability)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/live/"+room.Slug, nil)
	request.AddCookie(&http.Cookie{Name: liveSessionCookie, Value: accessToken})
	request.AddCookie(&http.Cookie{Name: liveCreatorCookie, Value: creatorToken})
	later := now.Add(liveSessionLifetime + time.Minute)
	if api.sessionAuthorized(request, room.Slug, later) {
		t.Fatal("ordinary access session survived its configured lifetime")
	}
	got, ok := api.creatorCapability(request, room.Slug, later)
	if !ok {
		t.Fatal("creator credential should survive ordinary access expiry")
	}
	joined, err := hub.JoinWithCreator(ctx, room.Slug, "creator-after-renewal", got, later)
	if err != nil || !joined.Session.IsCreator() {
		t.Fatalf("creator reconnect = %#v, %v", joined, err)
	}
	hub.RevokeCreatorCapability(room.Slug)
	if _, ok := api.creatorCapability(request, room.Slug, later); ok {
		t.Fatal("revoked creator capability remained valid at the HTTP boundary")
	}
	joined, err = hub.JoinWithCreator(ctx, room.Slug, "after-revocation", got, later)
	if err != nil || joined.Session.IsCreator() {
		t.Fatalf("revoked creator reconnect = %#v, %v", joined, err)
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
	peer := dialLivePeer(t, wsURL, placeholder.URL, nil)
	defer peer.CloseNow()
	joinLivePeer(t, peer, "oversized-frame")
	if err := peer.Write(context.Background(), websocket.MessageText, []byte(strings.Repeat("x", (64<<10)+1))); err != nil {
		t.Fatal(err)
	}
	readCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _, err = peer.Read(readCtx)
	if websocket.CloseStatus(err) != websocket.StatusMessageTooBig {
		t.Fatalf("oversized frame close = %v, status = %d", err, websocket.CloseStatus(err))
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
	if _, err := decodeLiveWireMessage([]byte(`{"type":"heartbeat"}`)); err != nil {
		t.Fatalf("valid heartbeat rejected: %v", err)
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
	if creatorParticipant == nil || creatorParticipant["role"] != "writer" {
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
	if writerParticipant == nil || writerParticipant["role"] != "writer" {
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
	if joined := joinLivePeer(t, creatorAfterUnlock, "creator-after-unlock"); joined["creator"] != true {
		t.Fatalf("creator authority was erased by password reauthentication: %s", joined)
	}

	writeLiveMessage(t, writer, `{"type":"room_watch_only","watch_only":true}`)
	if event := readLiveEvent(t, writer, "error"); event["code"] != "unauthorized" {
		t.Fatalf("non-creator room mode response = %#v", event)
	}
	writeLiveMessage(t, writer, `{"type":"participant_remove","participant_id":"`+creatorParticipant["id"].(string)+`"}`)
	if event := readLiveEvent(t, writer, "error"); event["code"] != "unauthorized" {
		t.Fatalf("non-creator removal response = %#v", event)
	}

	writeLiveMessage(t, creator, `{"type":"room_watch_only","watch_only":true}`)
	mode := readLiveEvent(t, creator, "room_mode_changed")
	if mode["watch_only"] != true {
		t.Fatalf("creator room mode response = %#v", mode)
	}
	participants, _ := mode["participants"].([]any)
	for _, item := range participants {
		participant, _ := item.(map[string]any)
		if participant["role"] != "watch_only" {
			t.Fatalf("mode transition left a writer active: %#v", participant)
		}
	}
	writeLiveMessage(t, writer, `{"type":"push_changes","operation_id":"watcher-edit","document_id":"main","base_version":0,"changes":[[0,"blocked"]]}`)
	if event := readLiveEvent(t, writer, "error"); event["code"] != "unauthorized" {
		t.Fatalf("watch-only mutation response = %#v", event)
	}

	writeLiveMessage(t, creator, `{"type":"participant_remove","participant_id":"`+writerParticipant["id"].(string)+`"}`)
	removed := readLiveEvent(t, creator, "participant_removed")
	if removed["participant_id"] != writerParticipant["id"] {
		t.Fatalf("removal event = %#v", removed)
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
	if err := api.sweep(context.Background(), time.Now().UTC().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	status := readLiveEvent(t, conn, "status")
	if status["status"] != "expired" {
		t.Fatalf("expiry status = %#v", status)
	}
	readCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _, err = conn.Read(readCtx)
	if websocket.CloseStatus(err) != websocket.StatusTryAgainLater || !strings.Contains(err.Error(), "room expired") {
		t.Fatalf("expiry close error = %v, status = %d", err, websocket.CloseStatus(err))
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
