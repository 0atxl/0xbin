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

func TestValidateCiphertextEnvelope(t *testing.T) {
	valid := CiphertextEnvelope{Version: CryptoVersion, Algorithm: CryptoAlgorithm, IV: "AAECAwQFBgcICQoL", Ciphertext: "AAECAwQFBgcICQoLDA0ODw"}
	size, err := ValidateCiphertextEnvelope(valid, MaxEncryptedPayloadBytes())
	if err != nil || size != AESGCMTagBytes {
		t.Fatalf("ValidateCiphertextEnvelope() = %d, %v", size, err)
	}
	for _, envelope := range []CiphertextEnvelope{
		{Version: 2, Algorithm: CryptoAlgorithm, IV: valid.IV, Ciphertext: valid.Ciphertext},
		{Version: CryptoVersion, Algorithm: "other", IV: valid.IV, Ciphertext: valid.Ciphertext},
		{Version: CryptoVersion, Algorithm: CryptoAlgorithm, IV: "not/base64", Ciphertext: valid.Ciphertext},
		{Version: CryptoVersion, Algorithm: CryptoAlgorithm, IV: "AAECAw", Ciphertext: valid.Ciphertext},
		{Version: CryptoVersion, Algorithm: CryptoAlgorithm, IV: valid.IV, Ciphertext: "AA"},
	} {
		if _, err := ValidateCiphertextEnvelope(envelope, MaxEncryptedPayloadBytes()); !errors.Is(err, ErrInvalidPayload) {
			t.Fatalf("ValidateCiphertextEnvelope(%#v) error = %v", envelope, err)
		}
	}
	oversized := valid
	oversized.Ciphertext = strings.Repeat("A", int((MaxEncryptedPayloadBytes()+1)*4/3))
	if _, err := ValidateCiphertextEnvelope(oversized, MaxEncryptedPayloadBytes()); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("oversized envelope error = %v", err)
	}
}
