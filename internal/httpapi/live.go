package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/0atxl/0xbin/internal/config"
	"github.com/0atxl/0xbin/internal/live"
	"github.com/0atxl/0xbin/internal/livecollab"
	"github.com/0atxl/0xbin/internal/ratelimit"
	"github.com/coder/websocket"
	"golang.org/x/crypto/argon2"
)

// LiveDependencies contains the durable store and process-local authority
// needed by the live HTTP/WebSocket transport. The frontend and paste API do
// not depend on this type.
type LiveDependencies struct {
	Store live.RoomStore
	Hub   *live.Hub
	Slugs liveSlugGenerator
}

type liveSlugGenerator interface {
	Generate() (string, error)
}

type liveAPI struct {
	store             live.RoomStore
	hub               *live.Hub
	slugs             liveSlugGenerator
	baseURL           *url.URL
	cfg               config.Config
	limits            *ratelimit.Registry
	sessions          *liveSessionStore
	creators          *liveSessionStore
	peers             *livePeerRegistry
	publications      *livePublicationRegistry
	activeConnections atomic.Int64
	passwordSlots     chan struct{}
}

const (
	liveSessionCookie       = "oxbin_live_session"
	liveCreatorCookie       = "oxbin_live_creator"
	liveSessionLifetime     = 15 * time.Minute
	livePasswordMaxBytes    = 256
	livePasswordSaltBytes   = 16
	livePasswordKeyBytes    = 32
	livePasswordMemory      = 64 * 1024
	livePasswordIterations  = 3
	livePasswordParallelism = 1
	livePeerQueueSize       = 32
	livePresenceInterval    = 50 * time.Millisecond
	liveJoinTimeout         = 10 * time.Second
	liveMaxSessions         = 10_000
)

var errLivePasswordBusy = errors.New("live password verification is busy")

func newLiveAPI(cfg config.Config, dependencies *LiveDependencies) *liveAPI {
	if !cfg.LiveEnabled || dependencies == nil {
		return nil
	}
	if dependencies.Store == nil || dependencies.Hub == nil || dependencies.Slugs == nil {
		panic("live dependencies require a store, hub, and slug generator")
	}
	return &liveAPI{
		store:         dependencies.Store,
		hub:           dependencies.Hub,
		slugs:         dependencies.Slugs,
		baseURL:       cfg.BaseURL,
		cfg:           cfg,
		sessions:      newLiveSessionStore(),
		creators:      newLiveSessionStore(),
		peers:         newLivePeerRegistry(),
		publications:  newLivePublicationRegistry(),
		passwordSlots: make(chan struct{}, 4),
	}
}

type liveCreateRequest struct {
	Password  string               `json:"password"`
	Documents []liveCreateDocument `json:"documents"`
}

type liveCreateDocument struct {
	Name     string `json:"name"`
	Language string `json:"language"`
	Content  string `json:"content"`
}

type liveUnlockRequest struct {
	Password string `json:"password"`
}

type liveCreateResponse struct {
	Slug             string    `json:"slug"`
	URL              string    `json:"url"`
	ExpiresAt        time.Time `json:"expires_at"`
	PasswordRequired bool      `json:"password_required"`
}

// liveConfigResponse contains the public, non-secret limits needed before a
// browser creates its first room. It deliberately excludes rate-limit and
// operational details.
type liveConfigResponse struct {
	MaxBytes int64 `json:"max_bytes"`
	// MaxDocumentBytes is a public semantic alias for MaxBytes. Live rooms have
	// one configurable content budget, so this value must never diverge.
	MaxDocumentBytes    int64 `json:"max_document_bytes"`
	MaxTabs             int   `json:"max_tabs"`
	MaxWriters          int   `json:"max_writers"`
	MaxViewers          int   `json:"max_viewers"`
	MaxParticipants     int   `json:"max_participants"`
	RoomLifetimeSeconds int64 `json:"room_lifetime_seconds"`
}

type liveRoomResponse struct {
	Slug                     string                    `json:"slug"`
	URL                      string                    `json:"url,omitempty"`
	ExpiresAt                time.Time                 `json:"expires_at"`
	PasswordRequired         bool                      `json:"password_required"`
	MetadataRevision         int                       `json:"metadata_revision"`
	MetadataSnapshotRevision int                       `json:"metadata_snapshot_revision"`
	MaxBytes                 int64                     `json:"max_bytes"`
	MaxDocumentBytes         int64                     `json:"max_document_bytes"` // Always identical to MaxBytes.
	MaxTabs                  int                       `json:"max_tabs"`
	MaxWriters               int                       `json:"max_writers"`
	MaxViewers               int                       `json:"max_viewers"`
	MaxParticipants          int                       `json:"max_participants"`
	RoomLifetimeSeconds      int64                     `json:"room_lifetime_seconds"`
	Creator                  bool                      `json:"creator"`
	Documents                []liveDocumentResponse    `json:"documents,omitempty"`
	Participants             []liveParticipantResponse `json:"participants,omitempty"`
	AcceptedOperationIDs     []string                  `json:"accepted_operation_ids,omitempty"`
}

type liveDocumentResponse struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Language         string `json:"language"`
	Content          string `json:"content"`
	Position         int    `json:"position"`
	Revision         int    `json:"revision"`
	SnapshotRevision int    `json:"snapshot_revision"`
}

type liveCursorResponse struct {
	DocumentID string `json:"document_id"`
	Revision   int    `json:"revision"`
	Anchor     int    `json:"anchor"`
	Head       int    `json:"head"`
}

type liveParticipantResponse struct {
	ID         string                 `json:"id"`
	Nickname   string                 `json:"nickname"`
	JoinedAt   time.Time              `json:"joined_at"`
	Color      string                 `json:"color"`
	CurrentTab string                 `json:"current_tab"`
	Cursor     *liveCursorResponse    `json:"cursor,omitempty"`
	Status     live.ParticipantStatus `json:"status"`
	Role       live.ParticipantRole   `json:"role"`
	LastSeenAt time.Time              `json:"last_seen_at"`
}

func (api *liveAPI) create(w http.ResponseWriter, r *http.Request) {
	if !api.allowHTTP(w, r, ratelimit.LiveCreate, clientIPFromContext(r.Context()), 1) {
		return
	}
	var request liveCreateRequest
	if err := decodeJSON(w, r, &request, api.cfg.LiveMaxBytes+64<<10); err != nil {
		api.writeRequestError(w, r, err)
		return
	}
	if !api.cfg.CreationEnabled {
		writeLiveError(w, r, http.StatusServiceUnavailable, "service_unavailable", "Live sharing is temporarily unavailable")
		return
	}
	if len(request.Password) > livePasswordMaxBytes || !utf8.ValidString(request.Password) {
		writeLiveError(w, r, http.StatusBadRequest, "invalid_request", "Invalid live room request")
		return
	}
	if len(request.Documents) < 1 || len(request.Documents) > api.cfg.LiveMaxTabs {
		writeLiveError(w, r, http.StatusBadRequest, "invalid_request", "Invalid live room request")
		return
	}

	now := time.Now().UTC()
	documents := make([]live.DocumentSnapshot, 0, len(request.Documents))
	var contentSize int64
	usedIDs := make(map[string]struct{}, len(request.Documents))
	for index, input := range request.Documents {
		if err := live.ValidateTabName(input.Name); err != nil || live.ValidateLanguageID(input.Language) != nil {
			writeLiveError(w, r, http.StatusBadRequest, "invalid_request", "Invalid live room request")
			return
		}
		if err := live.ValidateDocumentContent(input.Content, api.cfg.LiveMaxBytes); err != nil {
			if int64(len(input.Content)) > api.cfg.LiveMaxBytes {
				writeLiveError(w, r, http.StatusRequestEntityTooLarge, "message_too_large", "Live room content is too large")
			} else {
				writeLiveError(w, r, http.StatusBadRequest, "invalid_request", "Invalid live room request")
			}
			return
		}
		contentSize += int64(len(input.Content))
		if contentSize > api.cfg.LiveMaxBytes {
			writeLiveError(w, r, http.StatusRequestEntityTooLarge, "message_too_large", "Live room content is too large")
			return
		}
		id := "main"
		if index > 0 {
			var err error
			id, err = newLiveDocumentID(usedIDs)
			if err != nil {
				writeLiveError(w, r, http.StatusServiceUnavailable, "service_unavailable", "Live sharing is temporarily unavailable")
				return
			}
		}
		usedIDs[id] = struct{}{}
		documents = append(documents, live.DocumentSnapshot{ID: id, Name: input.Name, Language: input.Language, Content: input.Content, Position: index, UpdatedAt: now})
	}

	passwordHash, err := api.hashPassword(request.Password)
	if err != nil {
		if errors.Is(err, errLivePasswordBusy) {
			writeLiveError(w, r, http.StatusTooManyRequests, "rate_limited", "Too many requests")
			return
		}
		writeLiveError(w, r, http.StatusBadRequest, "invalid_request", "Invalid live room request")
		return
	}
	snapshot := live.RoomSnapshot{
		PasswordHash: passwordHash,
		ContentSize:  contentSize,
		ExpiresAt:    now.Add(api.cfg.LiveRoomLifetime),
		CreatedAt:    now,
		Documents:    documents,
	}
	slugValue, err := slugInsertRoom(r.Context(), api.slugs, api.store, snapshot)
	if err != nil {
		api.writeStoreError(w, r, err)
		return
	}
	snapshot.Slug = slugValue
	creator, err := api.hub.IssueCreatorCapability(slugValue, snapshot.ExpiresAt)
	if err != nil {
		api.writeStoreError(w, r, err)
		return
	}
	api.limits.RecordSuccess(clientIPFromContext(r.Context()))
	setLiveHeaders(w.Header())
	api.setSessionCookie(w, slugValue, now)
	api.setCreatorCookie(w, slugValue, creator)
	writeJSON(w, http.StatusCreated, liveCreateResponse{Slug: slugValue, URL: liveRoomURL(api.baseURL, slugValue), ExpiresAt: snapshot.ExpiresAt, PasswordRequired: passwordHash != ""})
}

func (api *liveAPI) config(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, api.publicConfig())
}

func slugInsertRoom(ctx context.Context, generator liveSlugGenerator, store live.RoomStore, snapshot live.RoomSnapshot) (string, error) {
	return slugInsertWithRetry(ctx, generator, func(ctx context.Context, value string) error {
		snapshot.Slug = value
		return store.CreateRoom(ctx, snapshot)
	})
}

func slugInsertWithRetry(ctx context.Context, generator liveSlugGenerator, insert func(context.Context, string) error) (string, error) {
	for attempt := 0; attempt < 8; attempt++ {
		value, err := generator.Generate()
		if err != nil {
			return "", err
		}
		err = insert(ctx, value)
		if err == nil {
			return value, nil
		}
		if !errors.Is(err, live.ErrRoomSlugCollision) {
			return "", err
		}
	}
	return "", fmt.Errorf("slug attempts exhausted: %w", live.ErrRoomSlugCollision)
}

func (api *liveAPI) bootstrap(w http.ResponseWriter, r *http.Request) {
	slugValue, ok := validSlug(r.PathValue("slug"))
	if !ok {
		api.writeNotFound(w, r)
		return
	}
	if !api.allowHTTP(w, r, ratelimit.LiveConnection, clientIPFromContext(r.Context()), 1) {
		return
	}
	now := time.Now().UTC()
	clientID := r.Header.Get("X-0xbin-Live-Client-ID")
	var (
		snapshot   live.RoomSnapshot
		operations []live.OperationRecord
		err        error
	)
	if clientID == "" {
		snapshot, err = api.store.GetRoomSnapshot(r.Context(), slugValue, now)
	} else {
		snapshot, operations, err = api.store.GetRoomSnapshotWithClientOperations(r.Context(), slugValue, clientID, 64, now)
	}
	if errors.Is(err, live.ErrInvalidChange) {
		writeLiveError(w, r, http.StatusBadRequest, "invalid_request", "Invalid live client ID")
		return
	}
	if errors.Is(err, live.ErrRoomNotFound) {
		api.writeNotFound(w, r)
		return
	}
	if err != nil {
		api.writeStoreError(w, r, err)
		return
	}
	if snapshot.PasswordHash != "" && !api.sessionAuthorized(r, slugValue, now) {
		writeLiveError(w, r, http.StatusUnauthorized, "password_required", "Password required")
		return
	}
	api.limits.RecordSuccess(clientIPFromContext(r.Context()))
	setLiveHeaders(w.Header())
	response := api.responseForLiveSnapshot(snapshot)
	if clientID != "" {
		response.AcceptedOperationIDs = make([]string, 0, len(operations))
		for _, operation := range operations {
			response.AcceptedOperationIDs = append(response.AcceptedOperationIDs, operation.OperationID)
		}
	}
	_, response.Creator = api.creatorCapability(r, slugValue, now)
	writeJSON(w, http.StatusOK, response)
}

func (api *liveAPI) unlock(w http.ResponseWriter, r *http.Request) {
	slugValue, ok := validSlug(r.PathValue("slug"))
	if !ok {
		api.writeNotFound(w, r)
		return
	}
	identity := clientIPFromContext(r.Context())
	if !api.allowHTTP(w, r, ratelimit.LiveUnlock, identity, 1) || !api.allowHTTP(w, r, ratelimit.LiveUnlock, "slug:"+slugValue, 1) {
		return
	}
	var request liveUnlockRequest
	if err := decodeJSON(w, r, &request, 8<<10); err != nil {
		api.writeRequestError(w, r, err)
		return
	}
	if len(request.Password) > livePasswordMaxBytes || !utf8.ValidString(request.Password) {
		writeLiveError(w, r, http.StatusUnauthorized, "invalid_password", "Invalid password")
		return
	}
	snapshot, err := api.store.GetRoomSnapshot(r.Context(), slugValue, time.Now().UTC())
	if errors.Is(err, live.ErrRoomNotFound) {
		api.writeNotFound(w, r)
		return
	}
	if err != nil {
		api.writeStoreError(w, r, err)
		return
	}
	if snapshot.PasswordHash != "" {
		valid, verifyErr := api.verifyPassword(request.Password, snapshot.PasswordHash)
		if errors.Is(verifyErr, errLivePasswordBusy) {
			writeLiveError(w, r, http.StatusTooManyRequests, "rate_limited", "Too many requests")
			return
		}
		if verifyErr != nil || !valid {
			writeLiveError(w, r, http.StatusUnauthorized, "invalid_password", "Invalid password")
			return
		}
	}
	now := time.Now().UTC()
	api.limits.RecordSuccess(identity)
	setLiveHeaders(w.Header())
	if snapshot.PasswordHash != "" {
		api.setSessionCookie(w, slugValue, now)
	}
	writeJSON(w, http.StatusOK, api.responseForLiveSnapshot(snapshot))
}

func (api *liveAPI) writeRequestError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errRequestTooLarge), errors.Is(err, live.ErrOperationLimit):
		writeLiveError(w, r, http.StatusRequestEntityTooLarge, "message_too_large", "Live room message is too large")
	case errors.Is(err, live.ErrRoomLimit):
		writeLiveError(w, r, http.StatusRequestEntityTooLarge, "room_limit_reached", "Live room limit reached")
	default:
		writeLiveError(w, r, http.StatusBadRequest, "invalid_request", "Invalid live room request")
	}
}

func (api *liveAPI) writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, live.ErrRoomNotFound), errors.Is(err, live.ErrRoomExpired):
		api.writeNotFound(w, r)
	case errors.Is(err, live.ErrRoomLimit):
		writeLiveError(w, r, http.StatusRequestEntityTooLarge, "room_limit_reached", "Live room limit reached")
	default:
		writeLiveError(w, r, http.StatusServiceUnavailable, "service_unavailable", "Service is temporarily unavailable")
	}
}

func (api *liveAPI) writeNotFound(w http.ResponseWriter, r *http.Request) {
	writeLiveError(w, r, http.StatusNotFound, "not_found", "Not found")
}

func (api *liveAPI) allowHTTP(w http.ResponseWriter, r *http.Request, category ratelimit.Category, identity string, cost int) bool {
	if api.limits == nil {
		return true
	}
	allowed, retryAfter := api.limits.Allow(category, identity, cost)
	if allowed {
		return true
	}
	w.Header().Set("Retry-After", fmt.Sprintf("%d", max(1, int(retryAfter.Seconds()))))
	writeLiveError(w, r, http.StatusTooManyRequests, "rate_limited", "Too many requests")
	return false
}

func writeLiveError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	setLiveHeaders(w.Header())
	writeError(w, status, code, message, requestIDFromContext(r.Context()))
}

func setLiveHeaders(header http.Header) {
	header.Set("Cache-Control", "no-store")
	header.Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	header.Set("X-Content-Type-Options", "nosniff")
}

func liveRoomURL(base *url.URL, slugValue string) string {
	copy := *base
	copy.Path = "/live/" + slugValue
	copy.RawPath = ""
	return copy.String()
}

func responseForLiveSnapshot(snapshot live.RoomSnapshot) liveRoomResponse {
	response := liveRoomResponse{Slug: snapshot.Slug, ExpiresAt: snapshot.ExpiresAt, PasswordRequired: false, MetadataRevision: snapshot.MetadataRevision, MetadataSnapshotRevision: snapshot.MetadataSnapshotRevision, Documents: make([]liveDocumentResponse, 0, len(snapshot.Documents))}
	for _, document := range snapshot.Documents {
		response.Documents = append(response.Documents, liveDocumentResponse{ID: document.ID, Name: document.Name, Language: document.Language, Content: document.Content, Position: document.Position, Revision: document.CurrentRevision, SnapshotRevision: document.SnapshotRevision})
	}
	return response
}

func (api *liveAPI) responseForLiveSnapshot(snapshot live.RoomSnapshot) liveRoomResponse {
	response := responseForLiveSnapshot(snapshot)
	response.MaxBytes = api.cfg.LiveMaxBytes
	response.MaxDocumentBytes = api.cfg.LiveMaxBytes
	response.MaxTabs = api.cfg.LiveMaxTabs
	response.MaxWriters = api.cfg.LiveMaxWriters
	response.MaxViewers = api.cfg.LiveMaxViewers
	response.MaxParticipants = api.cfg.LiveMaxParticipants
	response.RoomLifetimeSeconds = int64(api.cfg.LiveRoomLifetime.Seconds())
	return response
}

func (api *liveAPI) publicConfig() liveConfigResponse {
	return liveConfigResponse{
		MaxBytes:            api.cfg.LiveMaxBytes,
		MaxDocumentBytes:    api.cfg.LiveMaxBytes,
		MaxTabs:             api.cfg.LiveMaxTabs,
		MaxWriters:          api.cfg.LiveMaxWriters,
		MaxViewers:          api.cfg.LiveMaxViewers,
		MaxParticipants:     api.cfg.LiveMaxParticipants,
		RoomLifetimeSeconds: int64(api.cfg.LiveRoomLifetime.Seconds()),
	}
}

func responseForLiveState(state live.RoomState) liveRoomResponse {
	response := liveRoomResponse{Slug: state.Slug, ExpiresAt: state.ExpiresAt, MetadataRevision: state.MetadataRevision, MetadataSnapshotRevision: state.MetadataSnapshotRevision, Documents: make([]liveDocumentResponse, 0, len(state.Documents)), Participants: make([]liveParticipantResponse, 0, len(state.Participants))}
	for _, document := range state.Documents {
		response.Documents = append(response.Documents, liveDocumentResponse{ID: document.ID, Name: document.Name, Language: document.Language, Content: document.Content, Position: document.Position, Revision: document.Revision, SnapshotRevision: document.SnapshotRevision})
	}
	for _, participant := range state.Participants {
		response.Participants = append(response.Participants, responseForLiveParticipant(participant))
	}
	return response
}

func responseForLiveParticipant(participant live.ParticipantSnapshot) liveParticipantResponse {
	response := liveParticipantResponse{ID: participant.ID, Nickname: participant.Nickname, JoinedAt: participant.JoinedAt, Color: participant.Color, CurrentTab: participant.CurrentTab, Status: participant.Status, Role: participant.Role, LastSeenAt: participant.LastSeenAt}
	if participant.Cursor != nil {
		response.Cursor = &liveCursorResponse{DocumentID: participant.Cursor.DocumentID, Revision: participant.Cursor.Revision, Anchor: participant.Cursor.Anchor, Head: participant.Cursor.Head}
	}
	return response
}

func newLiveDocumentID(used map[string]struct{}) (string, error) {
	for attempt := 0; attempt < 8; attempt++ {
		var bytes [8]byte
		if _, err := rand.Read(bytes[:]); err != nil {
			return "", err
		}
		id := "doc-" + hex.EncodeToString(bytes[:])
		if _, exists := used[id]; !exists {
			return id, nil
		}
	}
	return "", errors.New("document ID attempts exhausted")
}

func (api *liveAPI) hashPassword(password string) (string, error) {
	if password == "" {
		return "", nil
	}
	if !api.acquirePasswordSlot() {
		return "", errLivePasswordBusy
	}
	defer api.releasePasswordSlot()
	salt := make([]byte, livePasswordSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, livePasswordIterations, livePasswordMemory, livePasswordParallelism, livePasswordKeyBytes)
	return fmt.Sprintf("v1$argon2id$m=%d,t=%d,p=%d$%s$%s", livePasswordMemory, livePasswordIterations, livePasswordParallelism, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func (api *liveAPI) verifyPassword(password, encoded string) (bool, error) {
	if !api.acquirePasswordSlot() {
		return false, errLivePasswordBusy
	}
	defer api.releasePasswordSlot()
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 || parts[0] != "v1" || parts[1] != "argon2id" {
		return false, errors.New("invalid password hash")
	}
	var memory uint32
	var iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[2], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil || memory != livePasswordMemory || iterations != livePasswordIterations || parallelism != livePasswordParallelism {
		return false, errors.New("invalid password hash")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(salt) != livePasswordSaltBytes {
		return false, errors.New("invalid password hash")
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(expected) != livePasswordKeyBytes {
		return false, errors.New("invalid password hash")
	}
	actual := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, livePasswordKeyBytes)
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

func (api *liveAPI) acquirePasswordSlot() bool {
	select {
	case api.passwordSlots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (api *liveAPI) releasePasswordSlot() {
	<-api.passwordSlots
}

type liveSessionRecord struct {
	slug    string
	expires time.Time
	creator live.CreatorCapability
}

type liveSessionStore struct {
	mu      sync.Mutex
	records map[string]liveSessionRecord
}

func newLiveSessionStore() *liveSessionStore {
	return &liveSessionStore{records: make(map[string]liveSessionRecord)}
}

func (store *liveSessionStore) put(slugValue string, expires time.Time, creator live.CreatorCapability) string {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return ""
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	store.mu.Lock()
	if len(store.records) >= liveMaxSessions {
		for key, record := range store.records {
			if !record.expires.After(time.Now().UTC()) {
				delete(store.records, key)
			}
		}
		if len(store.records) >= liveMaxSessions {
			for key := range store.records {
				delete(store.records, key)
				break
			}
		}
	}
	store.records[token] = liveSessionRecord{slug: slugValue, expires: expires, creator: creator}
	store.mu.Unlock()
	return token
}

func (store *liveSessionStore) get(token, slugValue string, now time.Time) (liveSessionRecord, bool) {
	if token == "" {
		return liveSessionRecord{}, false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	record, ok := store.records[token]
	if !ok || record.slug != slugValue || !record.expires.After(now) {
		if ok && !record.expires.After(now) {
			delete(store.records, token)
		}
		return liveSessionRecord{}, false
	}
	return record, true
}

func (api *liveAPI) sessionAuthorized(r *http.Request, slugValue string, now time.Time) bool {
	_, ok := api.session(r, slugValue, now)
	return ok
}

func (api *liveAPI) session(r *http.Request, slugValue string, now time.Time) (liveSessionRecord, bool) {
	cookie, err := r.Cookie(liveSessionCookie)
	if err != nil {
		return liveSessionRecord{}, false
	}
	return api.sessions.get(cookie.Value, slugValue, now)
}

func (api *liveAPI) setSessionCookie(w http.ResponseWriter, slugValue string, now time.Time) {
	token := api.sessions.put(slugValue, now.Add(liveSessionLifetime), live.CreatorCapability{})
	if token == "" {
		return
	}
	http.SetCookie(w, &http.Cookie{Name: liveSessionCookie, Value: token, Path: "/api/v1/live/" + slugValue, Expires: now.Add(liveSessionLifetime), MaxAge: int(liveSessionLifetime.Seconds()), HttpOnly: true, Secure: strings.EqualFold(api.baseURL.Scheme, "https"), SameSite: http.SameSiteStrictMode})
}

// The creator credential is independent from the short-lived room-access
// session. It remains only in process memory and is scoped to one room, so a
// restart drops creator authority even when a browser still holds the cookie.
func (api *liveAPI) setCreatorCookie(w http.ResponseWriter, slugValue string, creator live.CreatorCapability) {
	if creator == (live.CreatorCapability{}) {
		return
	}
	token := api.creators.put(slugValue, creator.ExpiresAt(), creator)
	if token == "" {
		return
	}
	now := time.Now().UTC()
	http.SetCookie(w, &http.Cookie{Name: liveCreatorCookie, Value: token, Path: "/api/v1/live/" + slugValue, Expires: creator.ExpiresAt(), MaxAge: int(creator.ExpiresAt().Sub(now).Seconds()), HttpOnly: true, Secure: strings.EqualFold(api.baseURL.Scheme, "https"), SameSite: http.SameSiteStrictMode})
}

func (api *liveAPI) creatorCapability(r *http.Request, slugValue string, now time.Time) (live.CreatorCapability, bool) {
	cookie, err := r.Cookie(liveCreatorCookie)
	if err != nil {
		return live.CreatorCapability{}, false
	}
	record, ok := api.creators.get(cookie.Value, slugValue, now)
	if !ok || record.creator == (live.CreatorCapability{}) || !api.hub.ValidCreatorCapability(slugValue, record.creator, now) {
		return live.CreatorCapability{}, false
	}
	return record.creator, true
}

type livePeer struct {
	api            *liveAPI
	conn           *websocket.Conn
	slug           string
	session        *live.RoomSession
	clientID       string
	rateIdentity   string
	participantID  string
	out            chan livePeerFrame
	control        chan livePeerFrame
	done           chan struct{}
	stopOnce       sync.Once
	presenceMu     sync.Mutex
	presence       *liveWireMessage
	rateNoticeMu   sync.Mutex
	lastRateNotice time.Time
}

type livePeerFrame struct {
	data        []byte
	closeCode   websocket.StatusCode
	closeReason string
}

type livePeerRegistry struct {
	mu    sync.RWMutex
	rooms map[string]map[*livePeer]struct{}
}

func newLivePeerRegistry() *livePeerRegistry {
	return &livePeerRegistry{rooms: make(map[string]map[*livePeer]struct{})}
}

func (registry *livePeerRegistry) add(peer *livePeer) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.rooms[peer.slug] == nil {
		registry.rooms[peer.slug] = make(map[*livePeer]struct{})
	}
	registry.rooms[peer.slug][peer] = struct{}{}
}

func (registry *livePeerRegistry) remove(peer *livePeer) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	room := registry.rooms[peer.slug]
	delete(room, peer)
	if len(room) == 0 {
		delete(registry.rooms, peer.slug)
	}
}

func (registry *livePeerRegistry) list(slugValue string) []*livePeer {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	peers := make([]*livePeer, 0, len(registry.rooms[slugValue]))
	for peer := range registry.rooms[slugValue] {
		peers = append(peers, peer)
	}
	return peers
}

func (peer *livePeer) enqueue(data []byte) {
	if len(data) > peer.api.cfg.LiveMaxMessageBytes {
		peer.stop(websocket.StatusMessageTooBig, "live message too large")
		return
	}
	select {
	case <-peer.done:
		return
	case peer.out <- livePeerFrame{data: data}:
	default:
		peer.stop(websocket.StatusTryAgainLater, "connection is overloaded")
	}
}

func (peer *livePeer) closeAfter(data []byte, code websocket.StatusCode, reason string) {
	peer.stopOnce.Do(func() {
		peer.control <- livePeerFrame{data: data, closeCode: code, closeReason: reason}
	})
}

func (peer *livePeer) expire() {
	peer.closeAfter(
		encodeLiveWireEvent(liveStatusEvent{Type: "status", Status: "expired"}),
		websocket.StatusTryAgainLater,
		"room expired",
	)
}

func (peer *livePeer) stop(code websocket.StatusCode, reason string) {
	peer.stopOnce.Do(func() {
		close(peer.done)
		_ = peer.conn.Close(code, reason)
	})
}

// queuePresence replaces an unsent cursor update with the newest state. The
// durable edit path never waits on or shares this ephemeral queue.
func (peer *livePeer) queuePresence(message liveWireMessage) {
	peer.presenceMu.Lock()
	peer.presence = &message
	peer.presenceMu.Unlock()
}

func (peer *livePeer) takePresence() (liveWireMessage, bool) {
	peer.presenceMu.Lock()
	defer peer.presenceMu.Unlock()
	if peer.presence == nil {
		return liveWireMessage{}, false
	}
	message := *peer.presence
	peer.presence = nil
	return message, true
}

func (peer *livePeer) presenceLoop(ctx context.Context) {
	ticker := time.NewTicker(livePresenceInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-peer.done:
			return
		case <-ticker.C:
			message, ok := peer.takePresence()
			if !ok {
				continue
			}
			if err := peer.api.handleWireMessage(ctx, peer, message); err != nil {
				if errors.Is(err, live.ErrRoomExpired) {
					peer.expire()
					return
				}
				peer.api.sendOperationError(peer, message.OperationID, err)
			}
		}
	}
}

func (peer *livePeer) notifyRateLimited(now time.Time) {
	peer.rateNoticeMu.Lock()
	defer peer.rateNoticeMu.Unlock()
	if !peer.lastRateNotice.IsZero() && now.Sub(peer.lastRateNotice) < time.Second {
		return
	}
	peer.lastRateNotice = now
	peer.enqueue(liveEvent("status", map[string]any{"status": "rate_limited"}))
}

func (peer *livePeer) writer(ctx context.Context) {
	for {
		select {
		case frame := <-peer.control:
			peer.writeFinalFrame(ctx, frame)
			return
		default:
		}
		select {
		case <-peer.done:
			return
		case frame := <-peer.control:
			peer.writeFinalFrame(ctx, frame)
			return
		case frame := <-peer.out:
			writeCtx, cancel := context.WithTimeout(ctx, peer.api.cfg.WriteTimeout)
			err := peer.conn.Write(writeCtx, websocket.MessageText, frame.data)
			cancel()
			if err != nil {
				peer.stop(websocket.StatusGoingAway, "write failed")
				return
			}
		}
	}
}

func (peer *livePeer) writeFinalFrame(ctx context.Context, frame livePeerFrame) {
	writeCtx, cancel := context.WithTimeout(ctx, peer.api.cfg.WriteTimeout)
	_ = peer.conn.Write(writeCtx, websocket.MessageText, frame.data)
	cancel()
	close(peer.done)
	_ = peer.conn.Close(frame.closeCode, frame.closeReason)
}

func (api *liveAPI) broadcast(slugValue string, data []byte, except *livePeer) {
	for _, peer := range api.peers.list(slugValue) {
		if peer != except {
			peer.enqueue(data)
		}
	}
}

func (api *liveAPI) websocket(w http.ResponseWriter, r *http.Request) {
	slugValue, ok := validSlug(r.PathValue("slug"))
	if !ok || !api.originAllowed(r) {
		writeLiveError(w, r, http.StatusForbidden, "forbidden", "WebSocket connection is not allowed")
		return
	}
	identity := clientIPFromContext(r.Context())
	if !api.allowHTTP(w, r, ratelimit.LiveConnection, identity, 1) || !api.allowHTTP(w, r, ratelimit.LiveConnection, "slug:"+slugValue, 1) {
		return
	}
	snapshot, err := api.store.GetRoomSnapshot(r.Context(), slugValue, time.Now().UTC())
	if errors.Is(err, live.ErrRoomNotFound) {
		api.writeNotFound(w, r)
		return
	}
	if err != nil {
		api.writeStoreError(w, r, err)
		return
	}
	_, sessionAuthorized := api.session(r, slugValue, time.Now().UTC())
	if snapshot.PasswordHash != "" && !sessionAuthorized {
		// Authorization must complete before Accept: an unauthorized protected
		// room request is an HTTP failure, never a briefly established socket.
		writeLiveError(w, r, http.StatusUnauthorized, "password_required", "Password required")
		return
	}
	if !api.reserveConnection(w, r) {
		return
	}
	defer api.activeConnections.Add(-1)

	setLiveHeaders(w.Header())
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{api.baseURL.Host}, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	creator, _ := api.creatorCapability(r, slugValue, time.Now().UTC())
	conn.SetReadLimit(int64(api.cfg.LiveMaxMessageBytes))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	joinCtx, joinCancel := context.WithTimeout(ctx, liveJoinTimeout)
	messageType, data, err := conn.Read(joinCtx)
	joinCancel()
	if err != nil || messageType != websocket.MessageText {
		_ = conn.Close(websocket.StatusProtocolError, "join required")
		return
	}
	join, err := decodeLiveWireMessage(data)
	if err != nil || join.Type != "join" {
		_ = conn.Close(websocket.StatusProtocolError, "join required")
		return
	}
	sessionID := join.SessionID
	if sessionID == "" {
		_ = conn.Close(websocket.StatusPolicyViolation, "session ID required")
		return
	}
	known, ok := knownLiveRevisions(join)
	if !ok {
		_ = conn.Close(websocket.StatusProtocolError, "known revisions required")
		return
	}
	unlockPublication := api.publications.lock(slugValue)
	joined, err := api.hub.JoinWithCreator(ctx, slugValue, sessionID, creator, time.Now().UTC())
	if err != nil {
		unlockPublication()
		api.closeHandshakeError(conn, err)
		return
	}
	bridge, err := joined.Session.Bridge(known, time.Now().UTC())
	if err != nil {
		unlockPublication()
		api.closeHandshakeError(conn, err)
		return
	}
	clientID := join.ClientID
	if clientID == "" {
		clientID = sessionID
	}
	peer := &livePeer{api: api, conn: conn, slug: slugValue, session: joined.Session, clientID: clientID, rateIdentity: identity, participantID: joined.Participant.ID, out: make(chan livePeerFrame, livePeerQueueSize), control: make(chan livePeerFrame, 1), done: make(chan struct{})}
	go peer.writer(ctx)
	go peer.presenceLoop(ctx)
	api.peers.add(peer)
	state := responseForLiveState(joined.State)
	peer.enqueue(encodeLiveWireEvent(liveJoinedEvent{
		Type: "joined", ExpiresAt: joined.State.ExpiresAt,
		MetadataRevision:  joined.State.MetadataRevision,
		DocumentRevisions: liveDocumentRevisions(joined.State),
		Participants:      state.Participants, Participant: responseForLiveParticipant(joined.Participant),
		Creator: joined.Session.IsCreator(), WatchOnly: joined.State.WatchOnly,
		Reconnected: joined.Reconnected,
	}))
	if bridge.Resync {
		peer.enqueue(encodeLiveWireEvent(liveStatusEvent{Type: "status", Status: "http_resync_required"}))
	} else {
		for _, accepted := range bridge.MetadataChanges {
			peer.enqueue(encodeLiveWireEvent(metadataWireEvent(accepted)))
		}
		for _, accepted := range bridge.DocumentChanges {
			peer.enqueue(encodeLiveWireEvent(documentWireEvent(accepted)))
		}
	}
	api.broadcast(slugValue, encodeLiveWireEvent(struct {
		Type        string                  `json:"type"`
		Participant liveParticipantResponse `json:"participant"`
	}{Type: "presence_joined", Participant: responseForLiveParticipant(joined.Participant)}), peer)
	unlockPublication()
	defer func() {
		api.peers.remove(peer)
		_ = joined.Session.Disconnect(time.Now().UTC())
		api.broadcast(slugValue, liveEvent("presence_left", map[string]any{"participant_id": joined.Participant.ID}), peer)
		peer.stop(websocket.StatusNormalClosure, "")
	}()
	pingDone := make(chan struct{})
	go peer.heartbeatLoop(ctx, pingDone)
	defer close(pingDone)

	for {
		messageType, data, err = conn.Read(ctx)
		if err != nil {
			return
		}
		if messageType != websocket.MessageText {
			peer.enqueue(liveEvent("error", map[string]any{"code": "invalid_request", "message": "Text messages are required"}))
			continue
		}
		if !api.allowMessage(peer) {
			continue
		}
		message, err := decodeLiveWireMessage(data)
		if err != nil {
			peer.enqueue(liveEvent("error", map[string]any{"code": "invalid_request", "message": "Invalid live message"}))
			continue
		}
		if message.Type == "presence" {
			peer.queuePresence(message)
			continue
		}
		if err := api.handleWireMessage(ctx, peer, message); err != nil {
			if errors.Is(err, live.ErrRoomExpired) {
				peer.expire()
				return
			}
			api.sendOperationError(peer, message.OperationID, err)
			if errors.Is(err, live.ErrParticipantInactive) {
				return
			}
		}
	}
}

func (peer *livePeer) heartbeatLoop(ctx context.Context, done chan struct{}) {
	ticker := time.NewTicker(peer.api.cfg.LiveHeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := peer.session.Heartbeat(time.Now().UTC()); err != nil {
				if errors.Is(err, live.ErrRoomExpired) {
					peer.expire()
				} else {
					peer.stop(websocket.StatusGoingAway, "heartbeat failed")
				}
				return
			}
			pingCtx, cancel := context.WithTimeout(ctx, peer.api.cfg.LiveHeartbeatInterval/2)
			err := peer.conn.Ping(pingCtx)
			cancel()
			if err != nil {
				peer.stop(websocket.StatusGoingAway, "heartbeat failed")
				return
			}
		}
	}
}

func (api *liveAPI) reserveConnection(w http.ResponseWriter, r *http.Request) bool {
	for {
		current := api.activeConnections.Load()
		if current >= int64(api.cfg.LiveMaxConnections) {
			writeLiveError(w, r, http.StatusTooManyRequests, "connection_limit_reached", "Too many live connections")
			return false
		}
		if api.activeConnections.CompareAndSwap(current, current+1) {
			return true
		}
	}
}

func (api *liveAPI) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Scheme == api.baseURL.Scheme && parsed.Host == api.baseURL.Host && parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == ""
}

func (api *liveAPI) allowMessage(peer *livePeer) bool {
	if api.limits == nil {
		return true
	}
	sessionAllowed, _ := api.limits.Allow(ratelimit.LiveMessage, "session:"+peer.slug+":"+peer.participantID, 1)
	roomAllowed, _ := api.limits.Allow(ratelimit.LiveMessageRoom, "room:"+peer.slug, 1)
	ipAllowed, _ := api.limits.Allow(ratelimit.LiveMessageIP, "ip:"+peer.rateIdentity, 1)
	if sessionAllowed && roomAllowed && ipAllowed {
		return true
	}
	peer.notifyRateLimited(time.Now().UTC())
	return false
}

func (api *liveAPI) handleWireMessage(ctx context.Context, peer *livePeer, message liveWireMessage) error {
	now := time.Now().UTC()
	switch message.Type {
	case "heartbeat":
		return peer.session.Heartbeat(now)
	case "ack":
		peer.enqueue(liveEvent("ack", map[string]any{"operation_id": message.OperationID}))
		return nil
	case "push_changes":
		changes, err := livecollab.ParseChangeSetJSON(message.Changes)
		if err != nil {
			return live.ErrOperationLimit
		}
		unlockPublication := api.publications.lock(peer.slug)
		defer unlockPublication()
		accepted, err := peer.session.SubmitDocument(ctx, live.DocumentOperation{OperationID: message.OperationID, ClientID: peer.clientID, DocumentID: message.DocumentID, BaseVersion: message.BaseVersion, Changes: changes}, now)
		if err != nil {
			return err
		}
		event := encodeLiveWireEvent(documentWireEvent(accepted))
		if accepted.Duplicate {
			peer.enqueue(event)
		} else {
			api.broadcast(peer.slug, event, nil)
		}
		return nil
	case "document_create", "document_update", "document_delete", "document_reorder":
		unlockPublication := api.publications.lock(peer.slug)
		defer unlockPublication()
		accepted, err := peer.session.ApplyMetadata(ctx, live.MetadataOperation{OperationID: message.OperationID, ClientID: peer.clientID, BaseVersion: message.BaseVersion, Kind: message.Type, DocumentID: message.DocumentID, Name: message.Name, Language: message.Language, Content: message.Content, Order: message.Order}, now)
		if err != nil {
			return err
		}
		event := encodeLiveWireEvent(metadataWireEvent(accepted))
		if accepted.Duplicate {
			peer.enqueue(event)
		} else {
			api.broadcast(peer.slug, event, nil)
		}
		return nil
	case "room_watch_only":
		unlockPublication := api.publications.lock(peer.slug)
		defer unlockPublication()
		state, err := peer.session.SetWatchOnly(message.WatchOnly, now)
		if err != nil {
			return err
		}
		api.broadcast(peer.slug, encodeLiveWireEvent(liveRoomModeEvent{
			Type: "room_mode_changed", WatchOnly: state.WatchOnly,
			Participants: responseForLiveState(state).Participants,
		}), nil)
		return nil
	case "participant_remove":
		unlockPublication := api.publications.lock(peer.slug)
		defer unlockPublication()
		err := peer.session.RemoveParticipant(message.ParticipantID, now)
		if err != nil {
			return err
		}
		event := encodeLiveWireEvent(liveParticipantRemovedEvent{Type: "participant_removed", ParticipantID: message.ParticipantID})
		api.broadcast(peer.slug, event, nil)
		for _, target := range api.peers.list(peer.slug) {
			if target.participantID == message.ParticipantID {
				target.stop(websocket.StatusPolicyViolation, "removed from room")
			}
		}
		return nil
	case "presence":
		participant, err := peer.session.UpdatePresence(live.PresenceUpdate{CurrentTab: message.CurrentTab, DocumentID: message.DocumentID, Revision: message.Revision, Anchor: message.Anchor, Head: message.Head}, now)
		if err != nil {
			return err
		}
		api.broadcast(peer.slug, liveEvent("presence_updated", map[string]any{"participant": responseForLiveParticipant(participant)}), nil)
		return nil
	case "participant_rename":
		participant, err := peer.session.Rename(message.Name, now)
		if err != nil {
			return err
		}
		api.broadcast(peer.slug, liveEvent("participant_renamed", map[string]any{"participant": responseForLiveParticipant(participant)}), nil)
		return nil
	default:
		return live.ErrOperationConflict
	}
}

func (api *liveAPI) sendOperationError(peer *livePeer, operationID string, err error) {
	code, status := classifyLiveOperationError(err)
	fields := map[string]any{"code": code, "status": status, "message": "Live operation could not be applied"}
	if operationID != "" {
		fields["operation_id"] = operationID
	}
	peer.enqueue(liveEvent("error", fields))
}

// classifyLiveOperationError is a protocol boundary: clients use status to
// decide whether the stable operation ID can be retried, needs an HTTP
// reconciliation, or must be retained for manual recovery.
func classifyLiveOperationError(err error) (code, status string) {
	switch {
	case errors.Is(err, live.ErrRoomExpired):
		return "room_expired", "expired"
	case errors.Is(err, live.ErrDocumentResync), errors.Is(err, live.ErrMetadataResync):
		return "resync_required", "resync_required"
	case errors.Is(err, livecollab.ErrRevisionConflict):
		return "resync_required", "resync_required"
	case errors.Is(err, live.ErrOperationLimit):
		return "message_too_large", "validation"
	case errors.Is(err, live.ErrParticipantInactive), errors.Is(err, live.ErrParticipantNotFound), errors.Is(err, live.ErrSessionRemoved), errors.Is(err, live.ErrCreatorRequired), errors.Is(err, live.ErrWatchOnly):
		return "unauthorized", "auth_required"
	case errors.Is(err, live.ErrParticipantLimit):
		return "room_limit_reached", "overloaded"
	case errors.Is(err, live.ErrNameTaken):
		return "name_taken", "validation"
	case errors.Is(err, live.ErrDocumentNotFound), errors.Is(err, live.ErrDocumentDeleted), errors.Is(err, live.ErrLastDocument), errors.Is(err, live.ErrOperationConflict), errors.Is(err, live.ErrInvalidPresence), errors.Is(err, livecollab.ErrInvalidChangeSet), errors.Is(err, livecollab.ErrDuplicateOperation):
		return "invalid_request", "validation"
	default:
		return "service_unavailable", "retryable"
	}
}

func (api *liveAPI) closeHandshakeError(conn *websocket.Conn, err error) {
	code := websocket.StatusPolicyViolation
	reason := "join failed"
	if errors.Is(err, live.ErrRoomExpired) || errors.Is(err, live.ErrRoomNotFound) {
		code, reason = websocket.StatusTryAgainLater, "room expired"
	} else if errors.Is(err, live.ErrParticipantLimit) {
		code, reason = websocket.StatusTryAgainLater, "room limit reached"
	}
	_ = conn.Close(code, reason)
}

func (api *liveAPI) shutdown(ctx context.Context) error {
	for _, peer := range api.allPeers() {
		peer.stop(websocket.StatusGoingAway, "server shutting down")
	}
	return api.hub.Shutdown(ctx, time.Now().UTC())
}

func (api *liveAPI) runLifecycle(ctx context.Context) {
	ticker := time.NewTicker(api.hub.SweepInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if err := api.sweep(ctx, now.UTC()); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, live.ErrHubClosed) {
				slog.Warn("live room sweep failed", "error", err)
			}
		}
	}
}

func (api *liveAPI) sweep(ctx context.Context, now time.Time) error {
	if _, err := api.hub.SweepWithParticipantRemovals(ctx, now, func(slug, participantID string) {
		api.broadcast(slug, liveEvent("participant_removed", map[string]any{"participant_id": participantID}), nil)
	}); err != nil {
		return err
	}
	for _, peer := range api.allPeers() {
		if _, err := peer.session.State(); errors.Is(err, live.ErrRoomExpired) {
			peer.expire()
		}
	}
	return nil
}

func (api *liveAPI) allPeers() []*livePeer {
	api.peers.mu.RLock()
	defer api.peers.mu.RUnlock()
	var peers []*livePeer
	for _, room := range api.peers.rooms {
		for peer := range room {
			peers = append(peers, peer)
		}
	}
	return peers
}
