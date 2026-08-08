package sqlite

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/0atxl/0xbin/internal/live"
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
	if count != 2 {
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

func TestCreateEncryptedAndGetActiveRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	envelope := testEnvelope()
	created, err := store.CreateEncrypted(ctx, paste.NewEncryptedPaste{
		Slug: "quietbrightotter", Envelope: envelope, ContentSize: 16,
		ExpiresAt: now.Add(time.Hour), CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	retrieved, err := store.GetActive(ctx, created.Slug, now)
	if err != nil {
		t.Fatal(err)
	}
	if !retrieved.IsEncrypted || retrieved.Envelope == nil || *retrieved.Envelope != envelope || retrieved.CryptoVersion != paste.CryptoVersion {
		t.Fatalf("encrypted retrieval = %#v", retrieved)
	}
	if retrieved.Payload != (paste.PlaintextPayload{}) {
		t.Fatalf("encrypted retrieval exposed plaintext payload = %#v", retrieved.Payload)
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

func TestConsumeActiveDeletesExactlyOneBurnPaste(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	burn := testNewPaste("quietbrightotter", now.Add(time.Hour), now)
	burn.BurnAfterRead = true
	if _, err := store.Create(ctx, burn); err != nil {
		t.Fatal(err)
	}

	const contenders = 24
	results := make(chan error, contenders)
	start := make(chan struct{})
	var group sync.WaitGroup
	for range contenders {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, err := store.ConsumeActive(ctx, burn.Slug, now)
			results <- err
		}()
	}
	close(start)
	group.Wait()
	close(results)

	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, paste.ErrNotFound) {
			t.Fatalf("ConsumeActive() error = %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful consumes = %d, want 1", successes)
	}
	if _, err := store.GetActive(ctx, burn.Slug, now); !errors.Is(err, paste.ErrNotFound) {
		t.Fatalf("burn paste after consume error = %v", err)
	}
}

func TestConsumeActiveRejectsExpiredAndNormalPastes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	expired := testNewPaste("stalequickwren", now.Add(-time.Hour), now)
	expired.BurnAfterRead = true
	normal := testNewPaste("calmbrightotter", now.Add(time.Hour), now)
	for _, newPaste := range []paste.NewPaste{expired, normal} {
		if _, err := store.Create(ctx, newPaste); err != nil {
			t.Fatal(err)
		}
	}
	for _, slug := range []string{expired.Slug, normal.Slug} {
		if _, err := store.ConsumeActive(ctx, slug, now); !errors.Is(err, paste.ErrNotFound) {
			t.Fatalf("ConsumeActive(%q) error = %v", slug, err)
		}
	}
}

func TestLiveRoomRoundTripAndReopen(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	now := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	want := testLiveRoom("calmbrightotter", now)
	store, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRoom(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetRoomSnapshot(ctx, want.Slug, now)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GetRoomSnapshot() = %#v, want %#v", got, want)
	}
	var storedPassword string
	if err := store.DB().QueryRowContext(ctx, "SELECT password_hash FROM live_rooms WHERE slug = ?", want.Slug).Scan(&storedPassword); err != nil {
		t.Fatal(err)
	}
	if storedPassword != want.PasswordHash {
		t.Fatalf("stored password hash = %q, want %q", storedPassword, want.PasswordHash)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	got, err = store.GetRoomSnapshot(ctx, want.Slug, now)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reopened room = %#v, want %#v", got, want)
	}
}

func TestLiveRoomExpiryAppliesToEveryAccessPath(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	room := testLiveRoom("quietquickwren", now)
	room.CreatedAt = now.Add(-2 * time.Hour)
	room.ExpiresAt = now.Add(-time.Minute)
	if err := store.CreateRoom(ctx, room); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetRoomSnapshot(ctx, room.Slug, now); !errors.Is(err, live.ErrRoomNotFound) {
		t.Fatalf("expired GetRoomSnapshot() error = %v", err)
	}
	if err := store.SaveSnapshot(ctx, room, now); !errors.Is(err, live.ErrRoomNotFound) {
		t.Fatalf("expired SaveSnapshot() error = %v", err)
	}
	change := live.ChangeRecord{
		StreamKind: live.StreamDocument, StreamID: "main", Revision: 1,
		Kind: "document_update", Payload: `{"content":"expired"}`,
	}
	if err := store.AppendChanges(ctx, room.Slug, []live.ChangeRecord{change}, now); !errors.Is(err, live.ErrRoomNotFound) {
		t.Fatalf("expired AppendChanges() error = %v", err)
	}
	if _, err := store.LoadChangesSince(ctx, room.Slug, live.StreamDocument, "main", 0, now); !errors.Is(err, live.ErrRoomNotFound) {
		t.Fatalf("expired LoadChangesSince() error = %v", err)
	}
	if err := store.CompactChanges(ctx, room.Slug, live.StreamDocument, "main", 0, now); !errors.Is(err, live.ErrRoomNotFound) {
		t.Fatalf("expired CompactChanges() error = %v", err)
	}
}

func TestCreateLiveRoomMapsOnlySlugPrimaryKeyToCollision(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	room := testLiveRoom("calmbrightotter", now)
	if err := store.CreateRoom(ctx, room); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRoom(ctx, room); !errors.Is(err, live.ErrRoomSlugCollision) {
		t.Fatalf("duplicate live room error = %v, want %v", err, live.ErrRoomSlugCollision)
	}
}

func TestLiveRoomChangesUseIndependentRevisionStreams(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	room := testLiveRoom("swiftcleverfox", now)
	if err := store.CreateRoom(ctx, room); err != nil {
		t.Fatal(err)
	}
	changes := []live.ChangeRecord{
		{StreamKind: live.StreamMetadata, StreamID: live.MetadataStreamID, Revision: 1, Kind: "document_create", Payload: `{"id":"notes"}`},
		{StreamKind: live.StreamDocument, StreamID: "main", Revision: 1, Kind: "document_update", Payload: `{"content":"package main\n"}`},
	}
	if err := store.AppendChanges(ctx, room.Slug, changes, now); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendChanges(ctx, room.Slug, []live.ChangeRecord{
		{StreamKind: live.StreamMetadata, StreamID: live.MetadataStreamID, Revision: 2, Kind: "document_reorder", Payload: `{"order":["main"]}`},
	}, now); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetRoomSnapshot(ctx, room.Slug, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.MetadataRevision != 2 || got.Documents[0].CurrentRevision != 1 {
		t.Fatalf("independent revisions = metadata %d, document %d", got.MetadataRevision, got.Documents[0].CurrentRevision)
	}
	metadata, err := store.LoadChangesSince(ctx, room.Slug, live.StreamMetadata, live.MetadataStreamID, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	document, err := store.LoadChangesSince(ctx, room.Slug, live.StreamDocument, "main", 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata) != 2 || len(document) != 1 || document[0].Revision != 1 {
		t.Fatalf("loaded changes = metadata %#v, document %#v", metadata, document)
	}

	updated := got
	updated.Documents[0].Content = "package main\n\nfunc main() {}\n"
	if err := store.SaveSnapshot(ctx, updated, now); err != nil {
		t.Fatal(err)
	}
	if err := store.CompactChanges(ctx, room.Slug, live.StreamMetadata, live.MetadataStreamID, 1, now); err != nil {
		t.Fatal(err)
	}
	if err := store.CompactChanges(ctx, room.Slug, live.StreamDocument, "main", 1, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadChangesSince(ctx, room.Slug, live.StreamMetadata, live.MetadataStreamID, 0, now); !errors.Is(err, live.ErrHistoryCompacted) {
		t.Fatalf("compacted metadata history error = %v", err)
	}
	if _, err := store.LoadChangesSince(ctx, room.Slug, live.StreamDocument, "main", 0, now); !errors.Is(err, live.ErrHistoryCompacted) {
		t.Fatalf("compacted document history error = %v", err)
	}
	metadata, err = store.LoadChangesSince(ctx, room.Slug, live.StreamMetadata, live.MetadataStreamID, 1, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata) != 1 || metadata[0].Revision != 2 {
		t.Fatalf("remaining metadata history = %#v", metadata)
	}
}

func TestCommitLiveDocumentChangeUsesOneTransactionAndPreservesOtherRows(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	room := testLiveRoom("steadybrightotter", now)
	if err := store.CreateRoom(ctx, room); err != nil {
		t.Fatal(err)
	}
	rowIDs := make(map[string]int64)
	rows, err := store.DB().QueryContext(ctx, `SELECT document_id, rowid FROM live_documents WHERE room_slug = ?`, room.Slug)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		var rowID int64
		if err := rows.Scan(&id, &rowID); err != nil {
			t.Fatal(err)
		}
		rowIDs[id] = rowID
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	var changesBefore int64
	if err := store.DB().QueryRowContext(ctx, `SELECT total_changes()`).Scan(&changesBefore); err != nil {
		t.Fatal(err)
	}
	room.Documents[0].Content += "!"
	room.Documents[0].CurrentRevision = 1
	room.Documents[0].SnapshotRevision = 1
	room.Documents[0].UpdatedAt = now.Add(time.Second)
	room.ContentSize++
	commit := live.ChangeCommit{
		Snapshot: room,
		Change: live.ChangeRecord{StreamKind: live.StreamDocument, StreamID: "main", Revision: 1,
			Kind: "push_changes", Payload: `{"changes":[5,[0,"!"]]}`, CreatedAt: now.Add(time.Second)},
	}
	if err := store.CommitChange(ctx, commit, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	history, err := store.LoadChangesSince(ctx, room.Slug, live.StreamDocument, "main", 0, now.Add(time.Second))
	if err != nil || len(history) != 1 || history[0].Revision != 1 {
		t.Fatalf("retained history = %#v, %v", history, err)
	}
	var changesAfter int64
	if err := store.DB().QueryRowContext(ctx, `SELECT total_changes()`).Scan(&changesAfter); err != nil {
		t.Fatal(err)
	}
	if got := changesAfter - changesBefore; got != 3 {
		t.Fatalf("SQLite rows changed by one document commit = %d, want 3 (history, room, one document)", got)
	}
	for id, wantRowID := range rowIDs {
		var gotRowID int64
		if err := store.DB().QueryRowContext(ctx, `SELECT rowid FROM live_documents WHERE room_slug = ? AND document_id = ?`, room.Slug, id).Scan(&gotRowID); err != nil {
			t.Fatal(err)
		}
		if gotRowID != wantRowID {
			t.Fatalf("document %q rowid changed from %d to %d", id, wantRowID, gotRowID)
		}
	}
}

func TestLiveRoomChangeBatchRollsBackOnRevisionConflict(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	room := testLiveRoom("calmquickwren", now)
	if err := store.CreateRoom(ctx, room); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendChanges(ctx, room.Slug, []live.ChangeRecord{
		{StreamKind: live.StreamMetadata, StreamID: live.MetadataStreamID, Revision: 1, Kind: "document_create", Payload: `{"id":"notes"}`},
	}, now); err != nil {
		t.Fatal(err)
	}
	conflicting := []live.ChangeRecord{
		{StreamKind: live.StreamMetadata, StreamID: live.MetadataStreamID, Revision: 2, Kind: "document_create", Payload: `{"id":"notes"}`},
		{StreamKind: live.StreamMetadata, StreamID: live.MetadataStreamID, Revision: 2, Kind: "document_delete", Payload: `{"id":"notes"}`},
	}
	if err := store.AppendChanges(ctx, room.Slug, conflicting, now); !errors.Is(err, live.ErrRevisionConflict) {
		t.Fatalf("conflicting AppendChanges() error = %v", err)
	}
	got, err := store.GetRoomSnapshot(ctx, room.Slug, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.MetadataRevision != 1 {
		t.Fatalf("metadata revision after rollback = %d, want 1", got.MetadataRevision)
	}
	changes, err := store.LoadChangesSince(ctx, room.Slug, live.StreamMetadata, live.MetadataStreamID, 1, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Fatalf("changes after rollback = %#v", changes)
	}
}

func TestDeleteExpiredLiveRoomsCascades(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	room := testLiveRoom("brightcalmotter", now)
	room.CreatedAt = now.Add(-2 * time.Hour)
	if err := store.CreateRoom(ctx, room); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendChanges(ctx, room.Slug, []live.ChangeRecord{
		{StreamKind: live.StreamDocument, StreamID: "main", Revision: 1, Kind: "document_update", Payload: `{"content":"changed"}`},
	}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, "UPDATE live_rooms SET expires_at = ? WHERE slug = ?", now.Add(-time.Minute).Unix(), room.Slug); err != nil {
		t.Fatal(err)
	}
	deleted, err := store.DeleteExpiredRooms(ctx, now, 1)
	if err != nil || deleted != 1 {
		t.Fatalf("DeleteExpiredRooms() = %d, %v; want 1, nil", deleted, err)
	}
	for _, table := range []string{"live_rooms", "live_documents", "live_changes"} {
		var count int
		column := "room_slug"
		if table == "live_rooms" {
			column = "slug"
		}
		if err := store.DB().QueryRowContext(ctx, "SELECT count(*) FROM "+table+" WHERE "+column+" = ?", room.Slug).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s rows after cascade = %d", table, count)
		}
	}
}

func testLiveRoom(slug string, now time.Time) live.RoomSnapshot {
	room := live.RoomSnapshot{
		Slug:                     slug,
		PasswordHash:             "$argon2id$v=19$m=65536,t=3,p=1$test$hash",
		MetadataRevision:         0,
		MetadataSnapshotRevision: 0,
		ExpiresAt:                now.Add(24 * time.Hour),
		CreatedAt:                now,
		Documents: []live.DocumentSnapshot{
			{
				ID: "main", Name: "main.go", Language: "go", Content: "package main\n",
				Position: 0, CurrentRevision: 0, SnapshotRevision: 0, UpdatedAt: now,
			},
			{
				ID: "notes", Name: "Notes", Language: "plaintext", Content: "shared notes",
				Position: 1, CurrentRevision: 0, SnapshotRevision: 0, UpdatedAt: now,
			},
		},
	}
	room.ContentSize = int64(len(room.Documents[0].Content) + len(room.Documents[1].Content))
	return room
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

func testEnvelope() paste.CiphertextEnvelope {
	return paste.CiphertextEnvelope{
		Version: paste.CryptoVersion, Algorithm: paste.CryptoAlgorithm,
		IV: "AAECAwQFBgcICQoL", Ciphertext: "AAECAwQFBgcICQoLDA0ODw",
	}
}
