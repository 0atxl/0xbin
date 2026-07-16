// Package paste owns paste validation and lifecycle semantics.
package paste

import (
	"errors"
	"fmt"
	"unicode/utf8"
)

const (
	PlaintextVersion = 1
	MaxContentBytes  = 1 << 20
	MaxTitleBytes    = 200
	MaxLanguageBytes = 64
)

var (
	ErrInvalidPayload  = errors.New("invalid plaintext payload")
	ErrPayloadTooLarge = errors.New("paste content is too large")
	ErrInvalidExpiry   = errors.New("invalid expiry identifier")
	ErrNotFound        = errors.New("paste not found")
	ErrSlugCollision   = errors.New("paste slug collision")
)

// PlaintextPayload is the version 1 server-readable paste representation.
type PlaintextPayload struct {
	Version  int    `json:"version"`
	Title    string `json:"title"`
	Language string `json:"language"`
	Content  string `json:"content"`
}

// ValidatePlaintext rejects unsupported, malformed, or oversized plaintext.
// Limits are measured in UTF-8 bytes, matching storage and request limits.
func ValidatePlaintext(payload PlaintextPayload, maxContentBytes int64) error {
	if payload.Version != PlaintextVersion {
		return fmt.Errorf("%w: unsupported version %d", ErrInvalidPayload, payload.Version)
	}
	if maxContentBytes < 1 || maxContentBytes > MaxContentBytes {
		return fmt.Errorf("max content bytes must be between 1 and %d", MaxContentBytes)
	}
	if int64(len(payload.Content)) > maxContentBytes {
		return fmt.Errorf("%w: maximum is %d bytes", ErrPayloadTooLarge, maxContentBytes)
	}
	if len(payload.Title) > MaxTitleBytes {
		return fmt.Errorf("%w: title exceeds %d bytes", ErrInvalidPayload, MaxTitleBytes)
	}
	if len(payload.Language) > MaxLanguageBytes {
		return fmt.Errorf("%w: language exceeds %d bytes", ErrInvalidPayload, MaxLanguageBytes)
	}
	if payload.Content == "" {
		return fmt.Errorf("%w: content is required", ErrInvalidPayload)
	}
	if !utf8.ValidString(payload.Title) || !utf8.ValidString(payload.Language) || !utf8.ValidString(payload.Content) {
		return fmt.Errorf("%w: fields must contain valid UTF-8", ErrInvalidPayload)
	}
	return nil
}
