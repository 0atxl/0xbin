package paste

import (
	"fmt"
	"time"
)

// ExpiryPolicy maps stable client identifiers to server-controlled durations.
type ExpiryPolicy struct {
	allowed map[string]time.Duration
}

// DefaultExpiryPolicy permits the MVP's one-hour and one-day identifiers.
func DefaultExpiryPolicy() ExpiryPolicy {
	policy, err := NewExpiryPolicy(map[string]time.Duration{
		"1h":  time.Hour,
		"24h": 24 * time.Hour,
	})
	if err != nil {
		panic("default expiry policy is invalid: " + err.Error())
	}
	return policy
}

// NewExpiryPolicy validates and copies an operator-controlled identifier map.
func NewExpiryPolicy(allowed map[string]time.Duration) (ExpiryPolicy, error) {
	if len(allowed) == 0 {
		return ExpiryPolicy{}, fmt.Errorf("at least one expiry identifier is required")
	}
	copy := make(map[string]time.Duration, len(allowed))
	for identifier, duration := range allowed {
		if identifier == "" {
			return ExpiryPolicy{}, fmt.Errorf("expiry identifier must not be empty")
		}
		if duration <= 0 {
			return ExpiryPolicy{}, fmt.Errorf("expiry duration for %q must be positive", identifier)
		}
		if duration > 24*time.Hour {
			return ExpiryPolicy{}, fmt.Errorf("expiry duration for %q must not exceed 24h", identifier)
		}
		copy[identifier] = duration
	}
	return ExpiryPolicy{allowed: copy}, nil
}

// ExpiresAt calculates expiry from server time and an allowed identifier.
func (p ExpiryPolicy) ExpiresAt(identifier string, now time.Time) (time.Time, error) {
	duration, ok := p.allowed[identifier]
	if !ok {
		return time.Time{}, fmt.Errorf("%w: %q", ErrInvalidExpiry, identifier)
	}
	return normalizeTime(now).Add(duration), nil
}

func normalizeTime(value time.Time) time.Time {
	return time.Unix(value.UTC().Unix(), 0).UTC()
}
