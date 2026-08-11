package live

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
)

const (
	creatorTokenBytes = 32
	CreatorHashBytes  = sha256.Size
)

var creatorHashDomain = []byte("0xbin/live-creator/v1\x00")

// CreatorCapability is the raw room-creator credential. The server sends its
// encoded value only in an HttpOnly cookie and persists only HashForRoom.
type CreatorCapability struct {
	token [creatorTokenBytes]byte
	valid bool
}

// NewCreatorCapability returns a random 256-bit creator credential.
func NewCreatorCapability() (CreatorCapability, error) {
	var capability CreatorCapability
	if _, err := rand.Read(capability.token[:]); err != nil {
		return CreatorCapability{}, err
	}
	capability.valid = true
	return capability, nil
}

// ParseCreatorCapability decodes the exact unpadded base64url cookie format.
func ParseCreatorCapability(value string) (CreatorCapability, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) != creatorTokenBytes || base64.RawURLEncoding.EncodeToString(decoded) != value {
		return CreatorCapability{}, errors.New("invalid live creator capability")
	}
	var capability CreatorCapability
	copy(capability.token[:], decoded)
	capability.valid = true
	return capability, nil
}

// CookieValue returns the opaque cookie encoding. Invalid zero capabilities
// intentionally encode to an empty value.
func (capability CreatorCapability) CookieValue() string {
	if !capability.valid {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(capability.token[:])
}

// HashForRoom binds the capability to one room without retaining the raw token.
func (capability CreatorCapability) HashForRoom(slug string) []byte {
	if !capability.valid || slug == "" {
		return nil
	}
	digest := sha256.New()
	_, _ = digest.Write(creatorHashDomain)
	_, _ = digest.Write([]byte(slug))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write(capability.token[:])
	return digest.Sum(nil)
}

// MatchesRoomHash validates a creator credential in constant time.
func (capability CreatorCapability) MatchesRoomHash(slug string, stored []byte) bool {
	expected := capability.HashForRoom(slug)
	return len(expected) == CreatorHashBytes && len(stored) == CreatorHashBytes && subtle.ConstantTimeCompare(expected, stored) == 1
}
