package paste

import (
	"errors"
	"strings"
	"testing"
)

func TestValidatePlaintextAcceptsValidPayload(t *testing.T) {
	payload := PlaintextPayload{
		Version:  PlaintextVersion,
		Title:    "Unicode example",
		Language: "plaintext",
		Content:  "hello, 世界\n",
	}
	if err := ValidatePlaintext(payload, MaxContentBytes); err != nil {
		t.Fatalf("ValidatePlaintext() error = %v", err)
	}
}

func TestValidatePlaintextRejectsInvalidPayload(t *testing.T) {
	tests := []struct {
		name    string
		payload PlaintextPayload
		target  error
	}{
		{
			name:    "unsupported version",
			payload: PlaintextPayload{Version: 2, Content: "content"},
			target:  ErrInvalidPayload,
		},
		{
			name:    "empty content",
			payload: PlaintextPayload{Version: PlaintextVersion},
			target:  ErrInvalidPayload,
		},
		{
			name:    "oversized content",
			payload: PlaintextPayload{Version: PlaintextVersion, Content: strings.Repeat("a", MaxContentBytes+1)},
			target:  ErrPayloadTooLarge,
		},
		{
			name:    "title too long",
			payload: PlaintextPayload{Version: PlaintextVersion, Title: strings.Repeat("a", MaxTitleBytes+1), Content: "content"},
			target:  ErrInvalidPayload,
		},
		{
			name:    "language too long",
			payload: PlaintextPayload{Version: PlaintextVersion, Language: strings.Repeat("a", MaxLanguageBytes+1), Content: "content"},
			target:  ErrInvalidPayload,
		},
		{
			name:    "invalid UTF-8 title",
			payload: PlaintextPayload{Version: PlaintextVersion, Title: string([]byte{0xff}), Content: "content"},
			target:  ErrInvalidPayload,
		},
		{
			name:    "invalid UTF-8 language",
			payload: PlaintextPayload{Version: PlaintextVersion, Language: string([]byte{0xff}), Content: "content"},
			target:  ErrInvalidPayload,
		},
		{
			name:    "invalid UTF-8 content",
			payload: PlaintextPayload{Version: PlaintextVersion, Content: string([]byte{0xff})},
			target:  ErrInvalidPayload,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidatePlaintext(test.payload, MaxContentBytes)
			if !errors.Is(err, test.target) {
				t.Fatalf("ValidatePlaintext() error = %v, want %v", err, test.target)
			}
		})
	}
}

func TestValidatePlaintextChecksSizeBeforeUTF8Scan(t *testing.T) {
	content := strings.Repeat("a", MaxContentBytes+1) + string([]byte{0xff})
	err := ValidatePlaintext(PlaintextPayload{Version: PlaintextVersion, Content: content}, MaxContentBytes)
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("ValidatePlaintext() error = %v, want %v", err, ErrPayloadTooLarge)
	}
}
