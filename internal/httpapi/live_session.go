package httpapi

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/0atxl/0xbin/internal/live"
)

// liveSessionStore owns the bounded process-local password access sessions.
// Creator capability validation remains separate and durable through its
// room-bound hash.
type liveSessionRecord struct {
	slug    string
	expires time.Time
}

type liveSessionStore struct {
	mu         sync.Mutex
	records    map[string]liveSessionRecord
	maxRecords int
}

func newLiveSessionStore() *liveSessionStore {
	return &liveSessionStore{
		records:    make(map[string]liveSessionRecord),
		maxRecords: liveMaxSessions,
	}
}

func (store *liveSessionStore) put(slugValue string, expires time.Time) string {
	token, err := newLiveSessionToken()
	if err != nil {
		return ""
	}
	store.mu.Lock()
	store.pruneExpiredLocked(time.Now().UTC())
	if len(store.records) >= store.maxRecords {
		for key := range store.records {
			delete(store.records, key)
			break
		}
	}
	store.records[token] = liveSessionRecord{slug: slugValue, expires: expires}
	store.mu.Unlock()
	return token
}

func (store *liveSessionStore) pruneExpiredLocked(now time.Time) {
	for key, record := range store.records {
		if !record.expires.After(now) {
			delete(store.records, key)
		}
	}
}

func newLiveSessionToken() (string, error) {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(tokenBytes), nil
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
	token := api.sessions.put(slugValue, now.Add(liveSessionLifetime))
	if token == "" {
		return
	}
	http.SetCookie(w, &http.Cookie{Name: liveSessionCookie, Value: token, Path: "/api/v1/live/" + slugValue, Expires: now.Add(liveSessionLifetime), MaxAge: int(liveSessionLifetime.Seconds()), HttpOnly: true, Secure: strings.EqualFold(api.baseURL.Scheme, "https"), SameSite: http.SameSiteStrictMode})
}

// The raw creator credential exists only in this room-scoped HttpOnly cookie;
// SQLite stores its room-bound hash.
func (api *liveAPI) setCreatorCookie(w http.ResponseWriter, slugValue string, expiresAt time.Time, creator live.CreatorCapability) {
	token := creator.CookieValue()
	now := time.Now().UTC()
	if token == "" || !expiresAt.After(now) {
		return
	}
	http.SetCookie(w, &http.Cookie{Name: liveCreatorCookie, Value: token, Path: "/api/v1/live/" + slugValue, Expires: expiresAt, MaxAge: int(expiresAt.Sub(now).Seconds()), HttpOnly: true, Secure: strings.EqualFold(api.baseURL.Scheme, "https"), SameSite: http.SameSiteStrictMode})
}

func (api *liveAPI) creatorCapability(r *http.Request, snapshot live.RoomSnapshot, now time.Time) (live.CreatorCapability, bool) {
	if !snapshot.ExpiresAt.After(now) {
		return live.CreatorCapability{}, false
	}
	cookie, err := r.Cookie(liveCreatorCookie)
	if err != nil {
		return live.CreatorCapability{}, false
	}
	capability, err := live.ParseCreatorCapability(cookie.Value)
	if err != nil || !capability.MatchesRoomHash(snapshot.Slug, snapshot.CreatorTokenHash) {
		return live.CreatorCapability{}, false
	}
	return capability, true
}
