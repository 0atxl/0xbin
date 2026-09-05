package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/0atxl/0xbin/internal/paste"
	"github.com/0atxl/0xbin/internal/ratelimit"
)

const requestMetadataAllowance = 4 << 10

var slugPattern = regexp.MustCompile(`^[a-z]{1,128}$`)

// PasteService is the public API's domain boundary.
type PasteService interface {
	CreatePlaintext(context.Context, paste.CreatePlaintextInput) (paste.Paste, error)
	CreateEncrypted(context.Context, paste.CreateEncryptedInput) (paste.Paste, error)
	GetActive(context.Context, string) (paste.Paste, error)
	Consume(context.Context, string) (paste.Paste, error)
}

type pasteAPI struct {
	pastes          PasteService
	baseURL         *url.URL
	maxContentBytes int64
	creationEnabled bool
	limits          *ratelimit.Registry
}

type readAdmission struct {
	identity string
	missCost int
}

type createPasteRequest struct {
	Mode          string          `json:"mode"`
	Payload       json.RawMessage `json:"payload"`
	Expiry        string          `json:"expiry"`
	BurnAfterRead bool            `json:"burn_after_read"`
}

type burnConfirmationResponse struct {
	BurnAfterRead bool `json:"burn_after_read"`
	IsEncrypted   bool `json:"is_encrypted"`
}

type createPasteResponse struct {
	Slug      string    `json:"slug"`
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

type pasteResponse struct {
	Slug          string                    `json:"slug"`
	Payload       *paste.PlaintextPayload   `json:"payload,omitempty"`
	Envelope      *paste.CiphertextEnvelope `json:"envelope,omitempty"`
	IsEncrypted   bool                      `json:"is_encrypted"`
	CryptoVersion *int                      `json:"crypto_version,omitempty"`
	BurnAfterRead bool                      `json:"burn_after_read"`
	ExpiresAt     time.Time                 `json:"expires_at"`
	CreatedAt     time.Time                 `json:"created_at"`
}

func (api pasteAPI) create(w http.ResponseWriter, r *http.Request) {
	if !api.creationEnabled {
		setPasteHeaders(w.Header())
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "Service is temporarily unavailable", requestIDFromContext(r.Context()))
		return
	}
	if !api.allow(w, r, ratelimit.Create, 1) {
		return
	}
	var request createPasteRequest
	if err := decodeJSON(w, r, &request, encryptedRequestLimit(api.maxContentBytes)); err != nil {
		api.writeRequestError(w, r, err)
		return
	}
	created, err := api.createPaste(r.Context(), request)
	if err != nil {
		api.writeRequestError(w, r, err)
		return
	}
	setPasteHeaders(w.Header())
	writeJSON(w, http.StatusCreated, createPasteResponse{
		Slug:      created.Slug,
		URL:       pasteURL(api.baseURL, created.Slug),
		ExpiresAt: created.ExpiresAt,
	})
}

func (api pasteAPI) createPaste(ctx context.Context, request createPasteRequest) (paste.Paste, error) {
	switch request.Mode {
	case "plaintext":
		if int64(len(request.Payload)) > api.maxContentBytes+requestMetadataAllowance {
			return paste.Paste{}, paste.ErrPayloadTooLarge
		}
		var payload paste.PlaintextPayload
		if err := decodePayload(request.Payload, &payload); err != nil {
			return paste.Paste{}, fmt.Errorf("%w: malformed plaintext payload", paste.ErrInvalidPayload)
		}
		return api.pastes.CreatePlaintext(ctx, paste.CreatePlaintextInput{Payload: payload, Expiry: request.Expiry, BurnAfterRead: request.BurnAfterRead})
	case "encrypted":
		var envelope paste.CiphertextEnvelope
		if err := decodePayload(request.Payload, &envelope); err != nil {
			return paste.Paste{}, fmt.Errorf("%w: malformed encrypted envelope", paste.ErrInvalidPayload)
		}
		limit, err := paste.EncryptedPayloadLimit(api.maxContentBytes)
		if err != nil {
			return paste.Paste{}, err
		}
		if _, err := paste.ValidateCiphertextEnvelope(envelope, limit); err != nil {
			return paste.Paste{}, err
		}
		return api.pastes.CreateEncrypted(ctx, paste.CreateEncryptedInput{Envelope: envelope, Expiry: request.Expiry, BurnAfterRead: request.BurnAfterRead})
	default:
		return paste.Paste{}, fmt.Errorf("%w: unsupported paste mode", paste.ErrInvalidPayload)
	}
}

func (api pasteAPI) get(w http.ResponseWriter, r *http.Request) {
	slug, ok := validSlug(r.PathValue("slug"))
	if !ok {
		api.writeMiss(w, r)
		return
	}
	admission, ok := api.admitRead(w, r)
	if !ok {
		return
	}
	result, err := api.pastes.GetActive(r.Context(), slug)
	if err != nil {
		if errors.Is(err, paste.ErrNotFound) {
			if !api.finishReadMiss(w, r, admission) {
				return
			}
			api.writeNotFound(w, r)
			return
		}
		api.cancelRead(admission)
		api.writeGetError(w, r, err)
		return
	}
	api.finishReadSuccess(admission)
	setPasteHeaders(w.Header())
	if result.BurnAfterRead {
		writeJSON(w, http.StatusOK, burnConfirmationResponse{BurnAfterRead: true, IsEncrypted: result.IsEncrypted})
		return
	}
	writeJSON(w, http.StatusOK, responseForPaste(result))
}

func (api pasteAPI) consume(w http.ResponseWriter, r *http.Request) {
	slug, ok := validSlug(r.PathValue("slug"))
	if !ok {
		api.writeNotFound(w, r)
		return
	}
	if !api.allow(w, r, ratelimit.Consume, 1) || !api.allowIdentity(w, r, ratelimit.Consume, "slug:"+slug, 1) {
		return
	}
	result, err := api.pastes.Consume(r.Context(), slug)
	if errors.Is(err, paste.ErrNotFound) {
		api.writeNotFound(w, r)
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "Service is temporarily unavailable", requestIDFromContext(r.Context()))
		return
	}
	setPasteHeaders(w.Header())
	writeJSON(w, http.StatusOK, responseForPaste(result))
}

func (api pasteAPI) raw(w http.ResponseWriter, r *http.Request) {
	slug, ok := validSlug(r.PathValue("slug"))
	if !ok {
		api.writeMiss(w, r)
		return
	}
	admission, ok := api.admitRead(w, r)
	if !ok {
		return
	}
	result, err := api.pastes.GetActive(r.Context(), slug)
	if err != nil {
		if errors.Is(err, paste.ErrNotFound) {
			if !api.finishReadMiss(w, r, admission) {
				return
			}
			api.writeNotFound(w, r)
			return
		}
		api.cancelRead(admission)
		api.writeGetError(w, r, err)
		return
	}
	if result.IsEncrypted || result.BurnAfterRead {
		api.cancelRead(admission)
		api.writeNotFound(w, r)
		return
	}
	api.finishReadSuccess(admission)
	setPasteHeaders(w.Header())
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, result.Payload.Content)
}

func responseForPaste(result paste.Paste) pasteResponse {
	response := pasteResponse{Slug: result.Slug, IsEncrypted: result.IsEncrypted, BurnAfterRead: result.BurnAfterRead, ExpiresAt: result.ExpiresAt, CreatedAt: result.CreatedAt}
	if result.IsEncrypted {
		response.Envelope = result.Envelope
		response.CryptoVersion = &result.CryptoVersion
	} else {
		response.Payload = &result.Payload
	}
	return response
}

func encryptedRequestLimit(maxContentBytes int64) int64 {
	// Base64url expands opaque ciphertext by up to 4/3; allowance covers the
	// IV, envelope fields, and JSON request metadata.
	limit, err := paste.EncryptedPayloadLimit(maxContentBytes)
	if err != nil {
		panic(err)
	}
	return ((limit+2)/3)*4 + requestMetadataAllowance
}

func decodePayload(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func (api pasteAPI) writeRequestError(w http.ResponseWriter, r *http.Request, err error) {
	requestID := requestIDFromContext(r.Context())
	switch {
	case errors.Is(err, errRequestTooLarge), errors.Is(err, paste.ErrPayloadTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "Paste is too large", requestID)
	case errors.Is(err, paste.ErrInvalidPayload), errors.Is(err, paste.ErrInvalidExpiry):
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid paste request", requestID)
	default:
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "Service is temporarily unavailable", requestID)
	}
}

func (api pasteAPI) writeGetError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, paste.ErrNotFound) {
		api.writeMiss(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "service_unavailable", "Service is temporarily unavailable", requestIDFromContext(r.Context()))
}

func (api pasteAPI) writeMiss(w http.ResponseWriter, r *http.Request) {
	cost := api.limits.RecordMiss(clientIPFromContext(r.Context()))
	if !api.allow(w, r, ratelimit.Miss, cost) {
		return
	}
	api.writeNotFound(w, r)
}

func (api pasteAPI) admitRead(w http.ResponseWriter, r *http.Request) (readAdmission, bool) {
	identity := clientIPFromContext(r.Context())
	if !api.allowIdentity(w, r, ratelimit.Read, identity, 1) {
		return readAdmission{}, false
	}
	missCost := api.limits.NextMissCost(identity)
	if missCost > 0 && !api.allowIdentity(w, r, ratelimit.Miss, identity, missCost) {
		api.limits.Refund(ratelimit.Read, identity, 1)
		return readAdmission{}, false
	}
	return readAdmission{identity: identity, missCost: missCost}, true
}

func (api pasteAPI) finishReadMiss(w http.ResponseWriter, r *http.Request, admission readAdmission) bool {
	api.limits.Refund(ratelimit.Read, admission.identity, 1)
	cost := api.limits.RecordMiss(admission.identity)
	if admission.missCost == 0 {
		return api.allowIdentity(w, r, ratelimit.Miss, admission.identity, cost)
	}
	if cost < admission.missCost {
		api.limits.Refund(ratelimit.Miss, admission.identity, admission.missCost-cost)
	} else if cost > admission.missCost && !api.allowIdentity(w, r, ratelimit.Miss, admission.identity, cost-admission.missCost) {
		return false
	}
	return true
}

func (api pasteAPI) finishReadSuccess(admission readAdmission) {
	if admission.missCost > 0 {
		api.limits.Refund(ratelimit.Miss, admission.identity, admission.missCost)
	}
	api.limits.RecordSuccess(admission.identity)
}

func (api pasteAPI) cancelRead(admission readAdmission) {
	api.limits.Refund(ratelimit.Read, admission.identity, 1)
	if admission.missCost > 0 {
		api.limits.Refund(ratelimit.Miss, admission.identity, admission.missCost)
	}
}

func (api pasteAPI) writeNotFound(w http.ResponseWriter, r *http.Request) {
	setPasteHeaders(w.Header())
	writeError(w, http.StatusNotFound, "not_found", "Not found", requestIDFromContext(r.Context()))
}

func (api pasteAPI) allow(w http.ResponseWriter, r *http.Request, category ratelimit.Category, cost int) bool {
	return api.allowIdentity(w, r, category, clientIPFromContext(r.Context()), cost)
}

func (api pasteAPI) allowIdentity(w http.ResponseWriter, r *http.Request, category ratelimit.Category, identity string, cost int) bool {
	allowed, retryAfter := api.limits.Allow(category, identity, cost)
	if allowed {
		return true
	}
	api.writeRateLimited(w, r, retryAfter)
	return false
}

func (api pasteAPI) writeRateLimited(w http.ResponseWriter, r *http.Request, retryAfter time.Duration) {
	seconds := max(1, int(retryAfter.Seconds()))
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many requests", requestIDFromContext(r.Context()))
}

func setPasteHeaders(header http.Header) {
	header.Set("Cache-Control", "no-store")
	header.Set("X-Robots-Tag", "noindex, nofollow, noarchive")
}

func validSlug(value string) (string, bool) {
	if !slugPattern.MatchString(value) {
		return "", false
	}
	return value, true
}

func pasteURL(base *url.URL, slug string) string {
	copy := *base
	copy.Path = "/" + slug
	return copy.String()
}

var errRequestTooLarge = errors.New("request body too large")

func decodeJSON(w http.ResponseWriter, r *http.Request, target any, maxBytes int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return errRequestTooLarge
		}
		return fmt.Errorf("%w: malformed JSON", paste.ErrInvalidPayload)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%w: request must contain one JSON value", paste.ErrInvalidPayload)
		}
		return fmt.Errorf("%w: malformed JSON", paste.ErrInvalidPayload)
	}
	return nil
}
