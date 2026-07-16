package sqlite

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/0atxl/0xbin/internal/paste"
)

func TestOpenMigratesAndReopens(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	store, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.DB().QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("migrations = %d", count)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	store, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES (999, 0)"); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	if _, err := Open(ctx, dir); err == nil {
		t.Fatal("Open() error = nil")
	}
}

func TestCreateAndGetActiveRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	createdAt := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	newPaste := paste.NewPaste{
		Slug: "calmbrightotter",
		Payload: paste.PlaintextPayload{
			Version:  paste.PlaintextVersion,
			Title:    "Exact 世界",
			Language: "go",
			Content:  "package main\n",
		},
		BurnAfterRead: true,
		ContentSize:   int64(len("package main\n")),
		ExpiresAt:     createdAt.Add(time.Hour),
		CreatedAt:     createdAt,
	}
	created, err := store.Create(ctx, newPaste)
	if err != nil {
		t.Fatal(err)
	}
	retrieved, err := store.GetActive(ctx, newPaste.Slug, createdAt)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(created, retrieved) {
		t.Fatalf("GetActive() = %#v, want %#v", retrieved, created)
	}
}

func TestGetActiveCollapsesMissingAndExpired(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	_, err = store.Create(ctx, testNewPaste("quietquickwren", now.Add(-time.Hour), now))
	if err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"quietquickwren", "missingcalmfox"} {
		_, err := store.GetActive(ctx, slug, now)
		if !errors.Is(err, paste.ErrNotFound) {
			t.Errorf("GetActive(%q) error = %v, want %v", slug, err, paste.ErrNotFound)
		}
	}

	var count int
	if err := store.DB().QueryRowContext(ctx, "SELECT count(*) FROM pastes WHERE slug = ?", "quietquickwren").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expired row count = %d, want 1; retrieval must not depend on cleanup", count)
	}
}

func TestCreateMapsOnlySlugPrimaryKeyToCollision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	first := testNewPaste("calmbrightotter", now.Add(time.Hour), now)
	if _, err := store.Create(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, first); !errors.Is(err, paste.ErrSlugCollision) {
		t.Fatalf("duplicate slug error = %v, want %v", err, paste.ErrSlugCollision)
	}

	if _, err := store.DB().ExecContext(ctx, "CREATE UNIQUE INDEX test_payload_unique ON pastes(payload)"); err != nil {
		t.Fatal(err)
	}
	other := first
	other.Slug = "swiftcleverfox"
	if _, err := store.Create(ctx, other); err == nil || errors.Is(err, paste.ErrSlugCollision) {
		t.Fatalf("other unique error = %v, must not be a slug collision", err)
	}
}

func TestDeleteExpiredBatchRemovesOnlyExpiredRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	for _, newPaste := range []paste.NewPaste{
		testNewPaste("oldbrightotter", now.Add(-2*time.Hour), now),
		testNewPaste("stalequickwren", now.Add(-time.Hour), now),
		testNewPaste("activecalmfox", now.Add(time.Hour), now),
	} {
		if _, err := store.Create(ctx, newPaste); err != nil {
			t.Fatal(err)
		}
	}

	deleted, err := store.DeleteExpiredBatch(ctx, now, 1)
	if err != nil || deleted != 1 {
		t.Fatalf("DeleteExpiredBatch() = %d, %v; want 1, nil", deleted, err)
	}
	deleted, err = store.DeleteExpiredBatch(ctx, now, 10)
	if err != nil || deleted != 1 {
		t.Fatalf("DeleteExpiredBatch() = %d, %v; want 1, nil", deleted, err)
	}
	deleted, err = store.DeleteExpiredBatch(ctx, now, 10)
	if err != nil || deleted != 0 {
		t.Fatalf("DeleteExpiredBatch() = %d, %v; want 0, nil", deleted, err)
	}
	if _, err := store.GetActive(ctx, "activecalmfox", now); err != nil {
		t.Fatalf("active paste was removed: %v", err)
	}
}

func TestDeleteExpiredBatchRejectsInvalidLimit(t *testing.T) {
	t.Parallel()
	store, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DeleteExpiredBatch(context.Background(), time.Now(), 0); err == nil {
		t.Fatal("DeleteExpiredBatch() error = nil")
	}
}

func testNewPaste(slug string, expiresAt, createdAt time.Time) paste.NewPaste {
	content := "content"
	return paste.NewPaste{
		Slug: slug,
		Payload: paste.PlaintextPayload{
			Version:  paste.PlaintextVersion,
			Language: "plaintext",
			Content:  content,
		},
		ContentSize: int64(len(content)),
		ExpiresAt:   expiresAt,
		CreatedAt:   createdAt,
	}
}
