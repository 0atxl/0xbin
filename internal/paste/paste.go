// Package paste owns paste validation and lifecycle semantics.
package paste

import (
	"encoding/base64"
	"errors"
	"fmt"
	"unicode/utf8"
)

const (
	PlaintextVersion = 1
	MaxContentBytes  = 1 << 20
	MaxTitleBytes    = 200
	MaxLanguageBytes = 64
	CryptoVersion    = 1
	CryptoAlgorithm  = "A256GCM"
	AESGCMIVBytes    = 12
	AESGCMTagBytes   = 16
	maxEnvelopeBytes = MaxContentBytes + 4<<10
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

// CiphertextEnvelope is the opaque, versioned browser-encrypted payload.
// The server validates its structure but never decrypts it.
type CiphertextEnvelope struct {
	Version    int    `json:"version"`
	Algorithm  string `json:"algorithm"`
	IV         string `json:"iv"`
	Ciphertext string `json:"ciphertext"`
}

// ValidateCiphertextEnvelope performs structural validation only. It never
// authenticates or decrypts ciphertext because the key remains in the URL
// fragment and never reaches the server.
func ValidateCiphertextEnvelope(envelope CiphertextEnvelope, maxBytes int64) (int64, error) {
	if envelope.Version != CryptoVersion || envelope.Algorithm != CryptoAlgorithm {
		return 0, fmt.Errorf("%w: unsupported encrypted envelope", ErrInvalidPayload)
	}
	if maxBytes < 1 || maxBytes > maxEnvelopeBytes {
		return 0, fmt.Errorf("encrypted payload limit must be between 1 and %d", maxEnvelopeBytes)
	}
	iv, err := decodeBase64url(envelope.IV)
	if err != nil || len(iv) != AESGCMIVBytes {
		return 0, fmt.Errorf("%w: invalid encrypted envelope", ErrInvalidPayload)
	}
	ciphertext, err := decodeBase64url(envelope.Ciphertext)
	if err != nil || len(ciphertext) < AESGCMTagBytes {
		return 0, fmt.Errorf("%w: invalid encrypted envelope", ErrInvalidPayload)
	}
	if int64(len(ciphertext)) > maxBytes {
		return 0, fmt.Errorf("%w: maximum is %d bytes", ErrPayloadTooLarge, maxBytes)
	}
	return int64(len(ciphertext)), nil
}

// MaxEncryptedPayloadBytes bounds decoded ciphertext while allowing the
// encrypted JSON metadata and AES-GCM authentication tag around 1 MiB content.
func MaxEncryptedPayloadBytes() int64 { return maxEnvelopeBytes }

// EncryptedPayloadLimit derives the ciphertext bound from an operator's
// plaintext-content limit while retaining room for encrypted JSON metadata.
func EncryptedPayloadLimit(maxContentBytes int64) (int64, error) {
	if maxContentBytes < 1 || maxContentBytes > MaxContentBytes {
		return 0, fmt.Errorf("max content bytes must be between 1 and %d", MaxContentBytes)
	}
	return maxContentBytes + 4<<10, nil
}

func decodeBase64url(value string) ([]byte, error) {
	if value == "" {
		return nil, errors.New("empty base64url")
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil || base64.RawURLEncoding.EncodeToString(decoded) != value {
		return nil, errors.New("invalid base64url")
	}
	return decoded, nil
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
