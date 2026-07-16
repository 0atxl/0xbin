package slug

import (
	"context"
	"errors"
	"testing"
)

func TestInsertWithRetryRetriesForcedCollisions(t *testing.T) {
	errSlugPrimaryKey := errors.New("slug primary key collision")
	generated := []string{"calmcalmotter", "calmcalmotter", "swiftswiftwren"}
	generateCalls := 0
	insertCalls := 0

	slug, err := InsertWithRetry(
		context.Background(),
		DefaultMaxAttempts,
		func() (string, error) {
			slug := generated[generateCalls]
			generateCalls++
			return slug, nil
		},
		func(_ context.Context, slug string) error {
			insertCalls++
			if slug == "calmcalmotter" {
				return errSlugPrimaryKey
			}
			return nil
		},
		func(err error) bool { return errors.Is(err, errSlugPrimaryKey) },
	)
	if err != nil {
		t.Fatal(err)
	}
	if slug != "swiftswiftwren" {
		t.Fatalf("InsertWithRetry() = %q, want %q", slug, "swiftswiftwren")
	}
	if generateCalls != 3 || insertCalls != 3 {
		t.Fatalf("calls = generate %d, insert %d; want 3 each", generateCalls, insertCalls)
	}
}

func TestInsertWithRetryDoesNotMistakeOtherUniqueErrorForSlugCollision(t *testing.T) {
	errSlugPrimaryKey := errors.New("slug primary key collision")
	errOtherUniqueConstraint := errors.New("other unique constraint")
	generateCalls := 0
	insertCalls := 0

	_, err := InsertWithRetry(
		context.Background(),
		DefaultMaxAttempts,
		func() (string, error) {
			generateCalls++
			return "calmcalmotter", nil
		},
		func(context.Context, string) error {
			insertCalls++
			return errOtherUniqueConstraint
		},
		func(err error) bool { return errors.Is(err, errSlugPrimaryKey) },
	)
	if !errors.Is(err, errOtherUniqueConstraint) {
		t.Fatalf("InsertWithRetry() error = %v, want %v", err, errOtherUniqueConstraint)
	}
	if generateCalls != 1 || insertCalls != 1 {
		t.Fatalf("calls = generate %d, insert %d; want 1 each", generateCalls, insertCalls)
	}
}

func TestInsertWithRetryIsBounded(t *testing.T) {
	errCollision := errors.New("collision")
	insertCalls := 0
	_, err := InsertWithRetry(
		context.Background(),
		3,
		func() (string, error) { return "calmcalmotter", nil },
		func(context.Context, string) error {
			insertCalls++
			return errCollision
		},
		func(err error) bool { return errors.Is(err, errCollision) },
	)
	if !errors.Is(err, ErrAttemptsExhausted) {
		t.Fatalf("InsertWithRetry() error = %v, want %v", err, ErrAttemptsExhausted)
	}
	if insertCalls != 3 {
		t.Fatalf("insert calls = %d, want 3", insertCalls)
	}
}

func TestInsertWithRetryHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	generateCalls := 0
	_, err := InsertWithRetry(
		ctx,
		DefaultMaxAttempts,
		func() (string, error) {
			generateCalls++
			return "calmcalmotter", nil
		},
		func(context.Context, string) error { return nil },
		func(error) bool { return false },
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("InsertWithRetry() error = %v, want %v", err, context.Canceled)
	}
	if generateCalls != 0 {
		t.Fatalf("generate calls = %d, want 0", generateCalls)
	}
}
