package slug

import (
	"context"
	"errors"
	"fmt"
)

const DefaultMaxAttempts = 8

// ErrAttemptsExhausted means every bounded insertion attempt collided with an
// existing paste slug.
var ErrAttemptsExhausted = errors.New("slug insertion attempts exhausted")

// InsertWithRetry generates a slug and immediately attempts insertion. It
// retries only errors identified by isCollision; every other storage error is
// returned without another attempt.
func InsertWithRetry(
	ctx context.Context,
	maxAttempts int,
	generate func() (string, error),
	insert func(context.Context, string) error,
	isCollision func(error) bool,
) (string, error) {
	if maxAttempts <= 0 {
		return "", fmt.Errorf("max attempts must be positive")
	}
	if generate == nil || insert == nil || isCollision == nil {
		return "", fmt.Errorf("generate, insert, and collision classifier are required")
	}

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		generated, err := generate()
		if err != nil {
			return "", fmt.Errorf("generate slug: %w", err)
		}
		if err := insert(ctx, generated); err != nil {
			if isCollision(err) {
				continue
			}
			return "", fmt.Errorf("insert slug: %w", err)
		}
		return generated, nil
	}
	return "", fmt.Errorf("after %d attempts: %w", maxAttempts, ErrAttemptsExhausted)
}
