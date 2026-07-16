package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/0atxl/0xbin/internal/paste"
)

const requestMetadataAllowance = 4 << 10

var slugPattern = regexp.MustCompile(`^[a-z]{1,128}$`)

// PasteService is the plaintext API's domain boundary.
type PasteService interface {
	CreatePlaintext(context.Context, paste.CreatePlaintextInput) (paste.Paste, error)
	GetActive(context.Context, string) (paste.Paste, error)
}

type pasteAPI struct {
	pastes          PasteService
	baseURL         *url.URL
	maxContentBytes int64
}

type createPasteRequest struct {
	Mode    string                 `json:"mode"`
	Payload paste.PlaintextPayload `json:"payload"`
	Expiry  string                 `json:"expiry"`
}

type createPasteResponse struct {
	Slug      string    `json:"slug"`
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

type pasteResponse struct {
	Slug          string                 `json:"slug"`
	Payload       paste.PlaintextPayload `json:"payload"`
	BurnAfterRead bool                   `json:"burn_after_read"`
	ExpiresAt     time.Time              `json:"expires_at"`
	CreatedAt     time.Time              `json:"created_at"`
}

func (api pasteAPI) create(w http.ResponseWriter, r *http.Request) {
	var request createPasteRequest
	if err := decodeJSON(w, r, &request, api.maxContentBytes+requestMetadataAllowance); err != nil {
		api.writeRequestError(w, r, err)
		return
	}
	if request.Mode != "plaintext" {
		api.writeRequestError(w, r, fmt.Errorf("%w: mode must be plaintext", paste.ErrInvalidPayload))
		return
	}
	created, err := api.pastes.CreatePlaintext(r.Context(), paste.CreatePlaintextInput{
		Payload: request.Payload,
		Expiry:  request.Expiry,
	})
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

func (api pasteAPI) get(w http.ResponseWriter, r *http.Request) {
	slug, ok := validSlug(r.PathValue("slug"))
	if !ok {
		api.writeNotFound(w, r)
		return
	}
	result, err := api.pastes.GetActive(r.Context(), slug)
	if err != nil {
		api.writeGetError(w, r, err)
		return
	}
	setPasteHeaders(w.Header())
	writeJSON(w, http.StatusOK, pasteResponse{
		Slug:          result.Slug,
		Payload:       result.Payload,
		BurnAfterRead: result.BurnAfterRead,
		ExpiresAt:     result.ExpiresAt,
		CreatedAt:     result.CreatedAt,
	})
}

func (api pasteAPI) raw(w http.ResponseWriter, r *http.Request) {
	slug, ok := validSlug(r.PathValue("slug"))
	if !ok {
		api.writeNotFound(w, r)
		return
	}
	result, err := api.pastes.GetActive(r.Context(), slug)
	if err != nil {
		api.writeGetError(w, r, err)
		return
	}
	setPasteHeaders(w.Header())
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, result.Payload.Content)
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
		api.writeNotFound(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "service_unavailable", "Service is temporarily unavailable", requestIDFromContext(r.Context()))
}

func (api pasteAPI) writeNotFound(w http.ResponseWriter, r *http.Request) {
	setPasteHeaders(w.Header())
	writeError(w, http.StatusNotFound, "not_found", "Not found", requestIDFromContext(r.Context()))
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
