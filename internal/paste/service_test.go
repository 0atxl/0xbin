package paste

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestServiceCreatePlaintextDerivesLifecycleValues(t *testing.T) {
	now := time.Date(2026, time.July, 16, 12, 30, 45, 999, time.FixedZone("test", 5*60*60+30*60))
	store := &recordingStore{}
	service, err := NewService(
		store,
		&sequenceGenerator{slugs: []string{"calmbrightotter"}},
		DefaultExpiryPolicy(),
		MaxContentBytes,
		func() time.Time { return now },
	)
	if err != nil {
		t.Fatal(err)
	}
	input := CreatePlaintextInput{
		Payload: PlaintextPayload{
			Version:  PlaintextVersion,
			Title:    "title",
			Language: "go",
			Content:  "package main\n",
		},
		Expiry:        "1h",
		BurnAfterRead: true,
	}

	created, err := service.CreatePlaintext(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	wantCreatedAt := time.Date(2026, time.July, 16, 7, 0, 45, 0, time.UTC)
	wantExpiresAt := wantCreatedAt.Add(time.Hour)
	if created.Slug != "calmbrightotter" || !reflect.DeepEqual(created.Payload, input.Payload) {
		t.Fatalf("created paste = %#v", created)
	}
	if !created.CreatedAt.Equal(wantCreatedAt) || !created.ExpiresAt.Equal(wantExpiresAt) {
		t.Fatalf("timestamps = created %v, expires %v; want %v and %v", created.CreatedAt, created.ExpiresAt, wantCreatedAt, wantExpiresAt)
	}
	if created.ContentSize != int64(len(input.Payload.Content)) || !created.BurnAfterRead {
		t.Fatalf("derived values = size %d, burn %v", created.ContentSize, created.BurnAfterRead)
	}
}

func TestServiceRejectsInvalidInputBeforeStorage(t *testing.T) {
	store := &recordingStore{}
	service, err := NewService(store, &sequenceGenerator{slugs: []string{"unused"}}, DefaultExpiryPolicy(), MaxContentBytes, time.Now)
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.CreatePlaintext(context.Background(), CreatePlaintextInput{
		Payload: PlaintextPayload{Version: PlaintextVersion, Content: ""},
		Expiry:  "1h",
	})
	if !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("CreatePlaintext() error = %v, want %v", err, ErrInvalidPayload)
	}
	if store.createCalls != 0 {
		t.Fatalf("store Create calls = %d, want 0", store.createCalls)
	}
}

func TestServiceRejectsUnknownExpiryBeforeStorage(t *testing.T) {
	store := &recordingStore{}
	service, err := NewService(store, &sequenceGenerator{slugs: []string{"unused"}}, DefaultExpiryPolicy(), MaxContentBytes, time.Now)
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.CreatePlaintext(context.Background(), CreatePlaintextInput{
		Payload: PlaintextPayload{Version: PlaintextVersion, Content: "content"},
		Expiry:  "2099-01-01T00:00:00Z",
	})
	if !errors.Is(err, ErrInvalidExpiry) {
		t.Fatalf("CreatePlaintext() error = %v, want %v", err, ErrInvalidExpiry)
	}
	if store.createCalls != 0 {
		t.Fatalf("store Create calls = %d, want 0", store.createCalls)
	}
}

type recordingStore struct {
	createCalls int
}

func (s *recordingStore) Create(_ context.Context, newPaste NewPaste) (Paste, error) {
	s.createCalls++
	return Paste{
		Slug:          newPaste.Slug,
		Payload:       newPaste.Payload,
		BurnAfterRead: newPaste.BurnAfterRead,
		ContentSize:   newPaste.ContentSize,
		ExpiresAt:     newPaste.ExpiresAt,
		CreatedAt:     newPaste.CreatedAt,
	}, nil
}

func (*recordingStore) GetActive(context.Context, string, time.Time) (Paste, error) {
	return Paste{}, ErrNotFound
}

type sequenceGenerator struct {
	slugs []string
	next  int
}

func (g *sequenceGenerator) Generate() (string, error) {
	if g.next >= len(g.slugs) {
		return "", errors.New("no test slug available")
	}
	slug := g.slugs[g.next]
	g.next++
	return slug, nil
}
