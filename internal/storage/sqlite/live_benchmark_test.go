package sqlite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0atxl/0xbin/internal/live"
)

// BenchmarkCommitLiveDocumentChange_MaxRoom records the Phase 7 persistence
// baseline at the default maximum tab and aggregate-content limits. The old
// path used three transactions and rewrote all eight document rows per edit;
// the optimized path uses one transaction and updates one document row.
func BenchmarkCommitLiveDocumentChange_MaxRoom(b *testing.B) {
	ctx := context.Background()
	store, err := Open(ctx, b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	documents := make([]live.DocumentSnapshot, 8)
	for i := range documents {
		documents[i] = live.DocumentSnapshot{
			ID: fmt.Sprintf("doc-%d", i), Name: fmt.Sprintf("tab-%d", i),
			Language: "plaintext", Content: strings.Repeat("x", 128<<10),
			Position: i, UpdatedAt: now,
		}
	}
	room := live.RoomSnapshot{Slug: "steadybrightotter", CreatedAt: now, ExpiresAt: now.Add(time.Hour), Documents: documents}
	if err := store.CreateRoom(ctx, room); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(documents[0].Content)))
	b.ResetTimer()
	for i := 1; i <= b.N; i++ {
		if i%2 == 0 {
			room.Documents[0].Content = "x" + room.Documents[0].Content[1:]
		} else {
			room.Documents[0].Content = "y" + room.Documents[0].Content[1:]
		}
		room.Documents[0].CurrentRevision = i
		room.Documents[0].SnapshotRevision = i
		room.Documents[0].UpdatedAt = now.Add(time.Duration(i) * time.Second)
		commit := live.ChangeCommit{Snapshot: room, Change: live.ChangeRecord{
			StreamKind: live.StreamDocument, StreamID: "doc-0", Revision: i,
			Kind: "push_changes", Payload: `{"changes":[[1,"y"],131071]}`,
			CreatedAt: room.Documents[0].UpdatedAt,
		}}
		commit.Operation = testLiveOperation(commit.Change, fmt.Sprintf("benchmark-%d", i))
		commit.RetainOperations = 2000
		if err := store.CommitChange(ctx, commit, now); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(3, "baseline_tx/op")
	b.ReportMetric(8, "baseline_documents/op")
	b.ReportMetric(float64(8*len(documents[0].Content)), "baseline_snapshot_bytes/op")
	b.ReportMetric(1, "sqlite_tx/op")
	b.ReportMetric(1, "documents_updated/op")
	b.ReportMetric(float64(len(documents[0].Content)), "snapshot_bytes/op")
}

func TestLiveDocumentCommitReducesPhysicalWALBytes(t *testing.T) {
	const edits = 8
	optimized := measureLivePersistenceWAL(t, edits, true)
	legacy := measureLivePersistenceWAL(t, edits, false)
	if optimized.walBytes >= legacy.walBytes/2 {
		t.Fatalf("optimized WAL bytes = %d, legacy = %d; want at least a 2x reduction", optimized.walBytes, legacy.walBytes)
	}
	t.Logf("physical WAL bytes/edit: optimized=%d legacy=%d reduction=%.1fx",
		optimized.walBytes/edits, legacy.walBytes/edits, float64(legacy.walBytes)/float64(optimized.walBytes))
}

func BenchmarkLiveDocumentPersistenceWAL_MaxRoom(b *testing.B) {
	for _, benchmark := range []struct {
		name      string
		optimized bool
		txPerEdit float64
	}{
		{name: "optimized", optimized: true, txPerEdit: 1},
		{name: "legacy", optimized: false, txPerEdit: 3},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			measurement := measureLivePersistenceWAL(b, b.N, benchmark.optimized)
			b.ReportMetric(float64(measurement.walBytes)/float64(b.N), "physical_WAL_bytes/op")
			b.ReportMetric(float64(measurement.editDuration.Nanoseconds())/float64(b.N), "persistence_ns/op")
			b.ReportMetric(benchmark.txPerEdit, "sqlite_tx/op")
		})
	}
}

type livePersistenceMeasurement struct {
	walBytes     int64
	editDuration time.Duration
}

func measureLivePersistenceWAL(tb testing.TB, edits int, optimized bool) livePersistenceMeasurement {
	tb.Helper()
	ctx := context.Background()
	dataDir := tb.TempDir()
	store, err := Open(ctx, dataDir)
	if err != nil {
		tb.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB().ExecContext(ctx, `PRAGMA wal_autocheckpoint = 0`); err != nil {
		tb.Fatal(err)
	}
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	room := maxLiveBenchmarkRoom(now)
	if err := store.CreateRoom(ctx, room); err != nil {
		tb.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		tb.Fatal(err)
	}

	started := time.Now()
	for revision := 1; revision <= edits; revision++ {
		if revision%2 == 0 {
			room.Documents[0].Content = "x" + room.Documents[0].Content[1:]
		} else {
			room.Documents[0].Content = "y" + room.Documents[0].Content[1:]
		}
		room.Documents[0].CurrentRevision = revision
		room.Documents[0].SnapshotRevision = revision
		room.Documents[0].UpdatedAt = now.Add(time.Duration(revision) * time.Second)
		change := live.ChangeRecord{
			StreamKind: live.StreamDocument, StreamID: "doc-0", Revision: revision,
			Kind: "push_changes", Payload: `{"changes":[[1,"y"],131071]}`,
			CreatedAt: room.Documents[0].UpdatedAt,
		}
		if optimized {
			commit := live.ChangeCommit{Snapshot: room, Change: change, RetainOperations: 2000}
			commit.Operation = testLiveOperation(change, fmt.Sprintf("measure-%d", revision))
			err = store.CommitChange(ctx, commit, now)
		} else {
			err = store.AppendChanges(ctx, room.Slug, []live.ChangeRecord{change}, now)
			if err == nil {
				err = store.SaveSnapshot(ctx, room, now)
			}
			if err == nil {
				err = store.CompactChanges(ctx, room.Slug, live.StreamDocument, "doc-0", revision, now)
			}
		}
		if err != nil {
			tb.Fatalf("persist revision %d: %v", revision, err)
		}
	}
	editDuration := time.Since(started)
	info, err := os.Stat(filepath.Join(dataDir, "0xbin.db-wal"))
	if err != nil {
		tb.Fatal(err)
	}
	return livePersistenceMeasurement{walBytes: info.Size(), editDuration: editDuration}
}

func maxLiveBenchmarkRoom(now time.Time) live.RoomSnapshot {
	documents := make([]live.DocumentSnapshot, 8)
	for i := range documents {
		documents[i] = live.DocumentSnapshot{
			ID: fmt.Sprintf("doc-%d", i), Name: fmt.Sprintf("tab-%d", i),
			Language: "plaintext", Content: strings.Repeat("x", 128<<10),
			Position: i, UpdatedAt: now,
		}
	}
	return live.RoomSnapshot{Slug: "steadybrightotter", CreatedAt: now, ExpiresAt: now.Add(time.Hour), Documents: documents}
}
