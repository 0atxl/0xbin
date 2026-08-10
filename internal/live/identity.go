package live

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

const (
	MaxParticipantCredentialBytes = 128
	MaxConnectionIDBytes          = 128
	MaxClientIDBytes              = 128
)

var (
	ErrInvalidParticipantCredential = errors.New("invalid live participant credential")
	ErrInvalidConnectionID          = errors.New("invalid live connection ID")
	ErrInvalidClientID              = errors.New("invalid live client ID")
)

var participantIDDomain = []byte("0xbin/live-participant/v1\x00")

// JoinIdentity keeps browser-participant, mounted-page connection, and
// operation-stream identity separate even while the compatibility path still
// supports one active connection per participant.
type JoinIdentity struct {
	ParticipantCredential string
	ConnectionID          string
	ClientID              string
	PreferredName         string
	PreferredNameSet      bool
}

// Validate rejects empty, oversized, non-ASCII, and control-bearing opaque
// identifiers. Preferred names use the existing user-visible nickname policy.
func (identity JoinIdentity) Validate() error {
	if err := validateOpaqueIdentifier(identity.ParticipantCredential, MaxParticipantCredentialBytes, ErrInvalidParticipantCredential); err != nil {
		return err
	}
	if err := validateOpaqueIdentifier(identity.ConnectionID, MaxConnectionIDBytes, ErrInvalidConnectionID); err != nil {
		return err
	}
	if err := validateOpaqueIdentifier(identity.ClientID, MaxClientIDBytes, ErrInvalidClientID); err != nil {
		return err
	}
	if identity.PreferredNameSet {
		return ValidateNickname(identity.PreferredName)
	}
	return nil
}

// ParticipantIDForRoom derives a stable, non-secret public identity. The room
// binding prevents one browser credential from becoming a cross-room tracker.
func ParticipantIDForRoom(slug, credential string) (string, error) {
	if slug == "" {
		return "", ErrInvalidParticipantCredential
	}
	if err := validateOpaqueIdentifier(credential, MaxParticipantCredentialBytes, ErrInvalidParticipantCredential); err != nil {
		return "", err
	}
	digest := sha256.New()
	_, _ = digest.Write(participantIDDomain)
	_, _ = digest.Write([]byte(slug))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte(credential))
	// A 128-bit public identifier keeps collision risk negligible without
	// exposing the credential or the full hash input.
	return hex.EncodeToString(digest.Sum(nil)[:16]), nil
}

func validateOpaqueIdentifier(value string, maxBytes int, sentinel error) error {
	if len(value) == 0 || len(value) > maxBytes {
		return fmt.Errorf("%w: length must be between 1 and %d bytes", sentinel, maxBytes)
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' {
			continue
		}
		return fmt.Errorf("%w: unsupported character", sentinel)
	}
	return nil
}
