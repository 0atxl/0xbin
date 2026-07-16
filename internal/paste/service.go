package paste

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/0atxl/0xbin/internal/slug"
)

// Paste is a plaintext paste returned by the domain and storage layers.
type Paste struct {
	Slug          string
	Payload       PlaintextPayload
	Envelope      *CiphertextEnvelope
	IsEncrypted   bool
	CryptoVersion int
	BurnAfterRead bool
	ContentSize   int64
	ExpiresAt     time.Time
	CreatedAt     time.Time
}

// NewPaste contains server-derived values ready for durable insertion.
type NewPaste struct {
	Slug          string
	Payload       PlaintextPayload
	BurnAfterRead bool
	ContentSize   int64
	ExpiresAt     time.Time
	CreatedAt     time.Time
}

// NewEncryptedPaste contains server-derived values for an opaque encrypted
// envelope. It deliberately has no key or plaintext fields.
type NewEncryptedPaste struct {
	Slug          string
	Envelope      CiphertextEnvelope
	BurnAfterRead bool
	ContentSize   int64
	ExpiresAt     time.Time
	CreatedAt     time.Time
}

// CreatePlaintextInput deliberately contains no client-controlled timestamps.
type CreatePlaintextInput struct {
	Payload       PlaintextPayload
	Expiry        string
	BurnAfterRead bool
}

// CreateEncryptedInput deliberately contains no plaintext or encryption key.
type CreateEncryptedInput struct {
	Envelope      CiphertextEnvelope
	Expiry        string
	BurnAfterRead bool
}

// Store is the Step 4 plaintext storage boundary.
type Store interface {
	Create(context.Context, NewPaste) (Paste, error)
	CreateEncrypted(context.Context, NewEncryptedPaste) (Paste, error)
	GetActive(context.Context, string, time.Time) (Paste, error)
	ConsumeActive(context.Context, string, time.Time) (Paste, error)
}

// SlugGenerator is implemented by slug.Generator and deterministic test fakes.
type SlugGenerator interface {
	Generate() (string, error)
}

// Service validates inputs and derives lifecycle values before storage.
type Service struct {
	store           Store
	slugs           SlugGenerator
	expiries        ExpiryPolicy
	maxContentBytes int64
	now             func() time.Time
}

// NewService constructs the plaintext paste service. The clock is injected so
// expiry and timestamps are deterministic in tests.
func NewService(store Store, slugs SlugGenerator, expiries ExpiryPolicy, maxContentBytes int64, now func() time.Time) (*Service, error) {
	if store == nil || slugs == nil || now == nil {
		return nil, fmt.Errorf("store, slug generator, and clock are required")
	}
	if maxContentBytes < 1 || maxContentBytes > MaxContentBytes {
		return nil, fmt.Errorf("max content bytes must be between 1 and %d", MaxContentBytes)
	}
	if len(expiries.allowed) == 0 {
		return nil, fmt.Errorf("expiry policy is required")
	}
	return &Service{
		store:           store,
		slugs:           slugs,
		expiries:        expiries,
		maxContentBytes: maxContentBytes,
		now:             now,
	}, nil
}

// CreatePlaintext validates a request, calculates server timestamps, and uses
// bounded generate-and-insert retries for authoritative slug uniqueness.
func (s *Service) CreatePlaintext(ctx context.Context, input CreatePlaintextInput) (Paste, error) {
	if err := ValidatePlaintext(input.Payload, s.maxContentBytes); err != nil {
		return Paste{}, err
	}
	createdAt := normalizeTime(s.now())
	expiresAt, err := s.expiries.ExpiresAt(input.Expiry, createdAt)
	if err != nil {
		return Paste{}, err
	}

	var created Paste
	_, err = slug.InsertWithRetry(
		ctx,
		slug.DefaultMaxAttempts,
		s.slugs.Generate,
		func(ctx context.Context, generated string) error {
			created, err = s.store.Create(ctx, NewPaste{
				Slug:          generated,
				Payload:       input.Payload,
				BurnAfterRead: input.BurnAfterRead,
				ContentSize:   int64(len(input.Payload.Content)),
				ExpiresAt:     expiresAt,
				CreatedAt:     createdAt,
			})
			return err
		},
		func(err error) bool { return errors.Is(err, ErrSlugCollision) },
	)
	if err != nil {
		return Paste{}, err
	}
	return created, nil
}

// CreateEncrypted validates an opaque envelope and derives lifecycle values.
// It cannot inspect encrypted plaintext or receive an encryption key.
func (s *Service) CreateEncrypted(ctx context.Context, input CreateEncryptedInput) (Paste, error) {
	limit, err := EncryptedPayloadLimit(s.maxContentBytes)
	if err != nil {
		return Paste{}, err
	}
	contentSize, err := ValidateCiphertextEnvelope(input.Envelope, limit)
	if err != nil {
		return Paste{}, err
	}
	createdAt := normalizeTime(s.now())
	expiresAt, err := s.expiries.ExpiresAt(input.Expiry, createdAt)
	if err != nil {
		return Paste{}, err
	}

	var created Paste
	_, err = slug.InsertWithRetry(
		ctx,
		slug.DefaultMaxAttempts,
		s.slugs.Generate,
		func(ctx context.Context, generated string) error {
			created, err = s.store.CreateEncrypted(ctx, NewEncryptedPaste{
				Slug:          generated,
				Envelope:      input.Envelope,
				BurnAfterRead: input.BurnAfterRead,
				ContentSize:   contentSize,
				ExpiresAt:     expiresAt,
				CreatedAt:     createdAt,
			})
			return err
		},
		func(err error) bool { return errors.Is(err, ErrSlugCollision) },
	)
	if err != nil {
		return Paste{}, err
	}
	return created, nil
}

// GetActive retrieves a paste using server time. Missing and expired records
// are both represented by ErrNotFound from the store.
func (s *Service) GetActive(ctx context.Context, slug string) (Paste, error) {
	return s.store.GetActive(ctx, slug, normalizeTime(s.now()))
}

// Consume deletes and returns one active burn-after-read paste atomically.
func (s *Service) Consume(ctx context.Context, slug string) (Paste, error) {
	return s.store.ConsumeActive(ctx, slug, normalizeTime(s.now()))
}
