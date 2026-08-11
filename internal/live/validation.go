// Package live contains validation and policy helpers shared by the future
// live-room HTTP, persistence, and WebSocket boundaries.
package live

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxNicknameBytes   = 32
	MaxTabNameBytes    = 64
	MaxLanguageIDBytes = 64
	MaxDocumentIDBytes = 64
)

var (
	ErrInvalidNickname   = errors.New("invalid live participant name")
	ErrInvalidTabName    = errors.New("invalid live tab name")
	ErrInvalidLanguage   = errors.New("invalid live language")
	ErrInvalidDocumentID = errors.New("invalid live document ID")
	ErrInvalidContent    = errors.New("invalid live document content")
	ErrInvalidOperation  = errors.New("invalid live operation")
)

var languageIDs = []string{
	"plaintext",
	"javascript",
	"typescript",
	"html",
	"python",
	"go",
	"c",
	"cpp",
	"java",
	"rust",
}

var operationKinds = map[string]struct{}{
	"join":               {},
	"push_changes":       {},
	"document_create":    {},
	"document_update":    {},
	"document_delete":    {},
	"document_reorder":   {},
	"presence":           {},
	"participant_rename": {},
	"ack":                {},
}

// SupportedLanguageIDs returns the language identifiers accepted by the
// existing CodeMirror language registry.
func SupportedLanguageIDs() []string { return append([]string(nil), languageIDs...) }

// ValidateNickname validates a temporary participant name. Names are kept as
// inert text and may contain Unicode, but controls and surrounding whitespace
// are rejected so roster rows remain readable and unambiguous.
func ValidateNickname(value string) error {
	return validateLabel(value, MaxNicknameBytes, ErrInvalidNickname)
}

// ValidateTabName validates a user-visible shared tab name.
func ValidateTabName(value string) error {
	return validateLabel(value, MaxTabNameBytes, ErrInvalidTabName)
}

// ValidateLanguageID accepts only the language modes already exposed by the
// frontend. The server must not accept arbitrary syntax-loader identifiers.
func ValidateLanguageID(value string) error {
	if len(value) == 0 || len(value) > MaxLanguageIDBytes {
		return fmt.Errorf("%w: length must be between 1 and %d bytes", ErrInvalidLanguage, MaxLanguageIDBytes)
	}
	for _, language := range languageIDs {
		if value == language {
			return nil
		}
	}
	return fmt.Errorf("%w: unsupported identifier %q", ErrInvalidLanguage, value)
}

// ValidateDocumentID accepts stable server-generated IDs and rejects values
// that could be confused with paths or message fields.
func ValidateDocumentID(value string) error {
	if len(value) == 0 || len(value) > MaxDocumentIDBytes {
		return fmt.Errorf("%w: length must be between 1 and %d bytes", ErrInvalidDocumentID, MaxDocumentIDBytes)
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' {
			if index == 0 && character == '-' {
				return fmt.Errorf("%w: must start with a lowercase letter or digit", ErrInvalidDocumentID)
			}
			continue
		}
		return fmt.Errorf("%w: unsupported character", ErrInvalidDocumentID)
	}
	return nil
}

// ValidateDocumentContent validates UTF-8 and applies the aggregate room
// content limit supplied by configuration.
func ValidateDocumentContent(value string, maxBytes int64) error {
	if maxBytes < 0 || int64(len(value)) > maxBytes {
		return fmt.Errorf("%w: content exceeds %d bytes", ErrInvalidContent, maxBytes)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%w: content is not valid UTF-8", ErrInvalidContent)
	}
	return nil
}

// ValidateRoomOperation validates the typed operation names used by the live
// message envelope. Payload-specific validation belongs to each operation.
func ValidateRoomOperation(kind string) error {
	if _, ok := operationKinds[kind]; !ok {
		return fmt.Errorf("%w: unsupported kind %q", ErrInvalidOperation, kind)
	}
	return nil
}

// ValidateDocumentOrder validates a complete tab ordering.
func ValidateDocumentOrder(ids []string, maxTabs int) error {
	if maxTabs < 1 || len(ids) < 1 || len(ids) > maxTabs {
		return fmt.Errorf("%w: document order must contain between 1 and %d IDs", ErrInvalidOperation, maxTabs)
	}
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if err := ValidateDocumentID(id); err != nil {
			return err
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("%w: duplicate document ID %q", ErrInvalidOperation, id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

// NameKey is the case-insensitive key used for active-room nickname
// uniqueness. Callers should validate a name before storing the key.
func NameKey(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

func validateLabel(value string, maxBytes int, kind error) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("%w: value is not valid UTF-8", kind)
	}
	if len(value) == 0 || len(value) > maxBytes {
		return fmt.Errorf("%w: length must be between 1 and %d bytes", kind, maxBytes)
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%w: surrounding whitespace is not allowed", kind)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("%w: control characters are not allowed", kind)
		}
	}
	return nil
}
