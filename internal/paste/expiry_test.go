package paste

import (
	"errors"
	"testing"
	"time"
)

func TestExpiryPolicyCalculatesFromServerTime(t *testing.T) {
	now := time.Date(2026, time.July, 16, 8, 30, 0, 987, time.FixedZone("test", 5*60*60+30*60))
	expiresAt, err := DefaultExpiryPolicy().ExpiresAt("1h", now)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, time.July, 16, 4, 0, 0, 0, time.UTC)
	if !expiresAt.Equal(want) || expiresAt.Location() != time.UTC {
		t.Fatalf("ExpiresAt() = %v, want %v", expiresAt, want)
	}
}

func TestExpiryPolicyRejectsUnknownIdentifier(t *testing.T) {
	_, err := DefaultExpiryPolicy().ExpiresAt("2h", time.Now())
	if !errors.Is(err, ErrInvalidExpiry) {
		t.Fatalf("ExpiresAt() error = %v, want %v", err, ErrInvalidExpiry)
	}
}

func TestExpiryPolicyCopiesConfiguration(t *testing.T) {
	allowed := map[string]time.Duration{"short": time.Minute}
	policy, err := NewExpiryPolicy(allowed)
	if err != nil {
		t.Fatal(err)
	}
	allowed["short"] = time.Hour
	expiresAt, err := policy.ExpiresAt("short", time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Unix(60, 0).UTC(); !expiresAt.Equal(want) {
		t.Fatalf("ExpiresAt() = %v, want %v", expiresAt, want)
	}
}

func TestExpiryPolicyRejectsDurationBeyondMVP(t *testing.T) {
	if _, err := NewExpiryPolicy(map[string]time.Duration{"later": 7 * 24 * time.Hour}); err == nil {
		t.Fatal("NewExpiryPolicy() error = nil")
	}
}
