package live_test

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/0atxl/0xbin/internal/live"
	"github.com/0atxl/0xbin/internal/livecollab"
	"github.com/0atxl/0xbin/internal/storage/sqlite"
)

func TestHubRebasesConcurrentEditsAndPersistsBeforeReturn(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateRoom(ctx, testRoom(now)); err != nil {
		t.Fatal(err)
	}
	recording := &recordingStore{RoomStore: store}
	options := testHubOptions([]string{"participant-a", "participant-b"}, nil)
	hub, err := live.NewHub(recording, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	alice, err := hub.Join(ctx, "calmbrightotter", "session-a", now)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := hub.Join(ctx, "calmbrightotter", "session-b", now)
	if err != nil {
		t.Fatal(err)
	}
	first := mustChangeSet(t, `[5,[0,"!"]]`)
	second := mustChangeSet(t, `[5,[0,"?"]]`)
	accepted, err := alice.Session.SubmitDocument(ctx, live.DocumentOperation{
		OperationID: "alice-1", ClientID: "client-a", DocumentID: "main", BaseVersion: 0, Changes: first,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Revision != 1 || accepted.Document != "hello!" {
		t.Fatalf("first accepted edit = %#v", accepted)
	}
	accepted, err = bob.Session.SubmitDocument(ctx, live.DocumentOperation{
		OperationID: "bob-1", ClientID: "client-b", DocumentID: "main", BaseVersion: 0, Changes: second,
	}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Revision != 2 || accepted.Document != "hello!?" {
		t.Fatalf("rebased accepted edit = %#v", accepted)
	}
	if !reflect.DeepEqual(recording.events, []string{"commit", "commit"}) {
		t.Fatalf("persistence order = %#v", recording.events)
	}
	persisted, err := store.GetRoomSnapshot(ctx, "calmbrightotter", now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Documents[0].Content != "hello!?" || persisted.Documents[0].CurrentRevision != 2 {
		t.Fatalf("persisted document = %#v", persisted.Documents[0])
	}
	if err := hub.Shutdown(ctx, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}

	newOptions := testHubOptions([]string{"participant-after-restart"}, nil)
	restarted, err := live.NewHub(store, nil, newOptions)
	if err != nil {
		t.Fatal(err)
	}
	joined, err := restarted.Join(ctx, "calmbrightotter", "new-session", now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if joined.Reconnected || joined.Participant.ID == alice.Participant.ID {
		t.Fatalf("presence survived restart: %#v", joined.Participant)
	}
	if len(joined.State.Participants) != 1 || joined.State.Documents[0].Content != "hello!?" {
		t.Fatalf("restarted state = %#v", joined.State)
	}
}

func TestHubMetadataConflictsAndIdempotentCreate(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateRoom(ctx, testRoom(now)); err != nil {
		t.Fatal(err)
	}
	hub, err := live.NewHub(store, nil, testHubOptions([]string{"p-a", "p-b"}, []string{"created-doc"}))
	if err != nil {
		t.Fatal(err)
	}
	alice, err := hub.Join(ctx, "calmbrightotter", "session-a", now)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := hub.Join(ctx, "calmbrightotter", "session-b", now)
	if err != nil {
		t.Fatal(err)
	}
	create := live.MetadataOperation{
		OperationID: "create-1", ClientID: "client-a", BaseVersion: 0,
		Kind: "document_create", Name: "scratch", Language: "plaintext", Content: "notes",
	}
	created, err := alice.Session.ApplyMetadata(ctx, create, now)
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 || created.DocumentID != "created-doc" {
		t.Fatalf("created tab = %#v", created)
	}
	duplicate, err := alice.Session.ApplyMetadata(ctx, create, now)
	if err != nil || !duplicate.Duplicate || duplicate.DocumentID != created.DocumentID {
		t.Fatalf("duplicate create = %#v, %v", duplicate, err)
	}
	changedCreate := create
	changedCreate.Content = "different"
	if _, err := alice.Session.ApplyMetadata(ctx, changedCreate, now); !errors.Is(err, livecollab.ErrDuplicateOperation) {
		t.Fatalf("changed duplicate error = %v", err)
	}

	if _, err := bob.Session.ApplyMetadata(ctx, live.MetadataOperation{
		OperationID: "stale-reorder", ClientID: "client-b", BaseVersion: 0,
		Kind: "document_reorder", Order: []string{"created-doc", "main", "notes"},
	}, now); !errors.Is(err, live.ErrMetadataResync) {
		t.Fatalf("stale reorder error = %v", err)
	}

	if _, err := alice.Session.ApplyMetadata(ctx, live.MetadataOperation{
		OperationID: "delete-notes", ClientID: "client-a", BaseVersion: 1,
		Kind: "document_delete", DocumentID: "notes",
	}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := bob.Session.ApplyMetadata(ctx, live.MetadataOperation{
		OperationID: "rename-deleted", ClientID: "client-b", BaseVersion: 1,
		Kind: "document_update", DocumentID: "notes", Name: "gone", Language: "plaintext",
	}, now); !errors.Is(err, live.ErrDocumentDeleted) {
		t.Fatalf("rename after delete error = %v", err)
	}
	if _, err := alice.Session.ApplyMetadata(ctx, live.MetadataOperation{
		OperationID: "delete-created", ClientID: "client-a", BaseVersion: 2,
		Kind: "document_delete", DocumentID: "created-doc",
	}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.Session.ApplyMetadata(ctx, live.MetadataOperation{
		OperationID: "delete-last", ClientID: "client-a", BaseVersion: 3,
		Kind: "document_delete", DocumentID: "main",
	}, now); !errors.Is(err, live.ErrLastDocument) {
		t.Fatalf("delete last document error = %v", err)
	}
}

func TestHubAtomicCommitFailureIsNotAcknowledgedAndSuccessfulRetrySurvivesRestart(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateRoom(ctx, testRoom(now)); err != nil {
		t.Fatal(err)
	}
	recording := &recordingStore{RoomStore: store, failCommits: 1}
	hub, err := live.NewHub(recording, nil, testHubOptions([]string{"participant-a"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	joined, err := hub.Join(ctx, "calmbrightotter", "session-a", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := joined.Session.SubmitDocument(ctx, live.DocumentOperation{
		OperationID: "interrupted-edit", ClientID: "client-a", DocumentID: "main", BaseVersion: 0,
		Changes: mustChangeSet(t, `[5,[0,"!"]]`),
	}, now); !errors.Is(err, live.ErrPersistence) {
		t.Fatalf("interrupted commit error = %v", err)
	}
	persisted, err := store.GetRoomSnapshot(ctx, "calmbrightotter", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Documents[0].Content != "hello" || persisted.Documents[0].CurrentRevision != 0 || persisted.Documents[0].SnapshotRevision != 0 {
		t.Fatalf("interrupted durable state = %#v", persisted.Documents[0])
	}
	if _, err := joined.Session.SubmitDocument(ctx, live.DocumentOperation{
		OperationID: "interrupted-edit", ClientID: "client-a", DocumentID: "main", BaseVersion: 0,
		Changes: mustChangeSet(t, `[5,[0,"!"]]`),
	}, now.Add(time.Second)); err != nil {
		t.Fatalf("retry after interrupted commit: %v", err)
	}
	restarted, err := live.NewHub(store, nil, testHubOptions([]string{"participant-after"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	rejoined, err := restarted.Join(ctx, "calmbrightotter", "new-session", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if rejoined.State.Documents[0].Content != "hello!" || rejoined.State.Documents[0].Revision != 1 {
		t.Fatalf("replayed room state = %#v", rejoined.State.Documents[0])
	}
}

func TestHubPresenceReconnectsWithinGraceAndEvictsAfterLeave(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateRoom(ctx, testRoom(now)); err != nil {
		t.Fatal(err)
	}
	options := testHubOptions([]string{"participant-a", "participant-b", "participant-after"}, nil)
	options.ReconnectGrace = 30 * time.Second
	hub, err := live.NewHub(store, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	first, err := hub.Join(ctx, "calmbrightotter", "session-a", now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := hub.Join(ctx, "calmbrightotter", "session-b", now)
	if err != nil {
		t.Fatal(err)
	}
	if first.Participant.Nickname == second.Participant.Nickname || first.Participant.Color == "" || second.Participant.Color == "" {
		t.Fatalf("participant identities are not distinct: %#v %#v", first.Participant, second.Participant)
	}
	if _, err := first.Session.UpdatePresence(live.PresenceUpdate{
		CurrentTab: "notes", DocumentID: "notes", Revision: 0, Anchor: 2, Head: 4,
	}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := first.Session.Disconnect(now.Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	state, err := second.Session.State()
	if err != nil {
		t.Fatal(err)
	}
	var disconnected live.ParticipantSnapshot
	for _, participant := range state.Participants {
		if participant.ID == first.Participant.ID {
			disconnected = participant
		}
	}
	if disconnected.Status != live.ParticipantConnectionLost || disconnected.Cursor == nil || disconnected.CurrentTab != "notes" {
		t.Fatalf("disconnected presence = %#v", disconnected)
	}
	reconnected, err := hub.Join(ctx, "calmbrightotter", "session-a", now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !reconnected.Reconnected || reconnected.Participant.ID != first.Participant.ID || reconnected.Participant.Nickname != first.Participant.Nickname || reconnected.Participant.Color != first.Participant.Color || !reconnected.Participant.JoinedAt.Equal(first.Participant.JoinedAt) {
		t.Fatalf("reconnected participant = %#v; first = %#v", reconnected.Participant, first.Participant)
	}
	if err := first.Session.Heartbeat(now.Add(3 * time.Second)); !errors.Is(err, live.ErrParticipantInactive) {
		t.Fatalf("stale connection heartbeat error = %v", err)
	}
	if err := reconnected.Session.Leave(now.Add(4 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := second.Session.Leave(now.Add(4 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if hub.RoomCount() != 0 {
		t.Fatalf("room count after final leave = %d, want 0", hub.RoomCount())
	}
	newHub, err := live.NewHub(store, nil, testHubOptions([]string{"participant-after"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	joined, err := newHub.Join(ctx, "calmbrightotter", "new-session", now.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if joined.Reconnected || joined.Participant.ID == first.Participant.ID {
		t.Fatalf("new process reclaimed old presence: %#v", joined.Participant)
	}
}

func TestHubSweepEvictsSequentialOneVisitRoomsAfterReconnectGrace(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateRoom(ctx, testRoom(now)); err != nil {
		t.Fatal(err)
	}
	options := testHubOptions([]string{"visit-a", "visit-b", "visit-c"}, nil)
	options.HeartbeatInterval = 5 * time.Second
	options.ReconnectGrace = 10 * time.Second
	options.ParticipantTimeout = 20 * time.Second
	hub, err := live.NewHub(store, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	for visit := 0; visit < 3; visit++ {
		joinedAt := now.Add(time.Duration(visit) * time.Minute)
		joined, err := hub.Join(ctx, "calmbrightotter", "session-"+strconv.Itoa(visit), joinedAt)
		if err != nil {
			t.Fatal(err)
		}
		if err := joined.Session.Disconnect(joinedAt.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if removed, err := hub.Sweep(ctx, joinedAt.Add(11*time.Second)); err != nil || removed != 0 {
			t.Fatalf("sweep within reconnect grace = %d, %v; want 0, nil", removed, err)
		}
		if removed, err := hub.Sweep(ctx, joinedAt.Add(11*time.Second+time.Nanosecond)); err != nil || removed != 1 {
			t.Fatalf("sweep after reconnect grace = %d, %v; want 1, nil", removed, err)
		}
		if hub.RoomCount() != 0 {
			t.Fatalf("room count after visit %d = %d, want 0", visit, hub.RoomCount())
		}
	}
}

func TestHubSweepPublishesGraceExpiredParticipantBeforeRemovingState(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateRoom(ctx, testRoom(now)); err != nil {
		t.Fatal(err)
	}
	options := testHubOptions([]string{"stale", "active"}, nil)
	options.HeartbeatInterval = 5 * time.Second
	options.ReconnectGrace = 10 * time.Second
	options.ParticipantTimeout = 20 * time.Second
	hub, err := live.NewHub(store, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Shutdown(ctx, now)

	stale, err := hub.Join(ctx, "calmbrightotter", "stale-session", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hub.Join(ctx, "calmbrightotter", "active-session", now); err != nil {
		t.Fatal(err)
	}
	if err := stale.Session.Disconnect(now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var published []string
	if _, err := hub.SweepWithParticipantRemovals(ctx, now.Add(11*time.Second+time.Nanosecond), func(slug, participantID string) {
		if slug != "calmbrightotter" {
			t.Fatalf("removal slug = %q", slug)
		}
		published = append(published, participantID)
	}); err != nil {
		t.Fatal(err)
	}
	if len(published) != 1 || published[0] != stale.Participant.ID {
		t.Fatalf("published removals = %v, want [%s]", published, stale.Participant.ID)
	}
	state, err := hub.State("calmbrightotter")
	if err != nil {
		t.Fatal(err)
	}
	for _, participant := range state.Participants {
		if participant.ID == stale.Participant.ID {
			t.Fatal("stale participant remains after grace expiry")
		}
	}
}

func TestHubSweepExpiresLoadedRoomWithoutStorageCleanup(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	room := testRoom(now)
	room.ExpiresAt = now.Add(time.Minute)
	if err := store.CreateRoom(ctx, room); err != nil {
		t.Fatal(err)
	}
	hub, err := live.NewHub(store, nil, testHubOptions([]string{"participant-a"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	joined, err := hub.Join(ctx, room.Slug, "session-a", now)
	if err != nil {
		t.Fatal(err)
	}
	if removed, err := hub.Sweep(ctx, room.ExpiresAt); err != nil || removed != 1 {
		t.Fatalf("expiry sweep = %d, %v; want 1, nil", removed, err)
	}
	if _, err := joined.Session.State(); !errors.Is(err, live.ErrRoomExpired) {
		t.Fatalf("expired session state error = %v", err)
	}
	var rows int
	if err := store.DB().QueryRowContext(ctx, "SELECT count(*) FROM live_rooms WHERE slug = ?", room.Slug).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("expiry sweep reclaimed durable rows = %d, want 1 row retained", rows)
	}
	if _, err := store.GetRoomSnapshot(ctx, room.Slug, room.ExpiresAt); !errors.Is(err, live.ErrRoomNotFound) {
		t.Fatalf("expired durable read error = %v", err)
	}
}

func TestHubShutdownDoesNotFlushRejectedAtomicCommit(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateRoom(ctx, testRoom(now)); err != nil {
		t.Fatal(err)
	}
	recording := &recordingStore{RoomStore: store, failCommits: 1}
	hub, err := live.NewHub(recording, nil, testHubOptions([]string{"participant-a"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	joined, err := hub.Join(ctx, "calmbrightotter", "session-a", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := joined.Session.SubmitDocument(ctx, live.DocumentOperation{
		OperationID: "interrupted-edit", ClientID: "client-a", DocumentID: "main", BaseVersion: 0,
		Changes: mustChangeSet(t, `[5,[0,"!"]]`),
	}, now); !errors.Is(err, live.ErrPersistence) {
		t.Fatalf("interrupted save error = %v", err)
	}
	if err := hub.Shutdown(ctx, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	persisted, err := store.GetRoomSnapshot(ctx, "calmbrightotter", now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Documents[0].Content != "hello" || persisted.Documents[0].CurrentRevision != 0 {
		t.Fatalf("rejected commit reached durable state = %#v", persisted.Documents[0])
	}
}

func TestHubCompactsHistoryOnlyAfterConfiguredThreshold(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateRoom(ctx, testRoom(now)); err != nil {
		t.Fatal(err)
	}
	options := testHubOptions([]string{"participant-a"}, nil)
	options.MaxHistoryRows = 2
	hub, err := live.NewHub(store, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	joined, err := hub.Join(ctx, "calmbrightotter", "session-a", now)
	if err != nil {
		t.Fatal(err)
	}
	content := "hello"
	for revision := 1; revision <= 4; revision++ {
		change := mustChangeSet(t, `[`+strconv.Itoa(len(content))+`,[0,"!"]]`)
		if _, err := joined.Session.SubmitDocument(ctx, live.DocumentOperation{
			OperationID: "edit-" + strconv.Itoa(revision), ClientID: "client-a",
			DocumentID: "main", BaseVersion: revision - 1, Changes: change,
		}, now.Add(time.Duration(revision)*time.Second)); err != nil {
			t.Fatal(err)
		}
		content += "!"
		var rows int
		if err := store.DB().QueryRowContext(ctx, `SELECT count(*) FROM live_changes WHERE room_slug = ? AND stream_kind = ? AND stream_id = ?`, "calmbrightotter", live.StreamDocument, "main").Scan(&rows); err != nil {
			t.Fatal(err)
		}
		want := map[int]int{1: 1, 2: 2, 3: 0, 4: 1}[revision]
		if rows != want {
			t.Fatalf("history rows after revision %d = %d, want %d", revision, rows, want)
		}
	}
}

func TestHubRetriesCommittedOperationAfterCompactionAndRestart(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateRoom(ctx, testRoom(now)); err != nil {
		t.Fatal(err)
	}
	options := testHubOptions([]string{"before-restart"}, nil)
	options.MaxHistoryRows = 1
	hub, err := live.NewHub(store, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	joined, err := hub.Join(ctx, "calmbrightotter", "session-before", now)
	if err != nil {
		t.Fatal(err)
	}
	first := live.DocumentOperation{
		OperationID: "durable-edit", ClientID: "stable-client", DocumentID: "main",
		BaseVersion: 0, Changes: mustChangeSet(t, `[5,[0,"!"]]`),
	}
	accepted, err := joined.Session.SubmitDocument(ctx, first, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Revision != 1 {
		t.Fatalf("accepted revision = %d, want 1", accepted.Revision)
	}
	if _, err := joined.Session.SubmitDocument(ctx, live.DocumentOperation{
		OperationID: "later-edit", ClientID: "stable-client", DocumentID: "main",
		BaseVersion: 1, Changes: mustChangeSet(t, `[6,[0,"?"]]`),
	}, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	firstMetadata := live.MetadataOperation{
		OperationID: "durable-metadata", ClientID: "stable-client", BaseVersion: 0,
		Kind: "document_update", DocumentID: "main", Name: "first-name", Language: "plaintext",
	}
	acceptedMetadata, err := joined.Session.ApplyMetadata(ctx, firstMetadata, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := joined.Session.ApplyMetadata(ctx, live.MetadataOperation{
		OperationID: "later-metadata", ClientID: "stable-client", BaseVersion: 1,
		Kind: "document_update", DocumentID: "main", Name: "second-name", Language: "plaintext",
	}, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	var historyRows int
	if err := store.DB().QueryRowContext(ctx, `SELECT count(*) FROM live_changes WHERE room_slug = ?`, "calmbrightotter").Scan(&historyRows); err != nil {
		t.Fatal(err)
	}
	if historyRows != 0 {
		t.Fatalf("compacted history rows = %d, want 0", historyRows)
	}
	if err := hub.Shutdown(ctx, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	restartedOptions := testHubOptions([]string{"after-restart"}, nil)
	restartedOptions.MaxHistoryRows = 1
	restarted, err := live.NewHub(store, nil, restartedOptions)
	if err != nil {
		t.Fatal(err)
	}
	rejoined, err := restarted.Join(ctx, "calmbrightotter", "session-after", now.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := rejoined.Session.SubmitDocument(ctx, first, now.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !duplicate.Duplicate || duplicate.Revision != accepted.Revision || !reflect.DeepEqual(duplicate.Changes, accepted.Changes) {
		t.Fatalf("durable duplicate = %#v, want original %#v", duplicate, accepted)
	}
	duplicateMetadata, err := rejoined.Session.ApplyMetadata(ctx, firstMetadata, now.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !duplicateMetadata.Duplicate || duplicateMetadata.Revision != acceptedMetadata.Revision || duplicateMetadata.Name != acceptedMetadata.Name {
		t.Fatalf("durable metadata duplicate = %#v, want original %#v", duplicateMetadata, acceptedMetadata)
	}
	conflicting := first
	conflicting.Changes = mustChangeSet(t, `[5,[0,"x"]]`)
	if _, err := rejoined.Session.SubmitDocument(ctx, conflicting, now.Add(6*time.Second)); !errors.Is(err, livecollab.ErrDuplicateOperation) {
		t.Fatalf("conflicting durable operation error = %v", err)
	}
	persisted, err := store.GetRoomSnapshot(ctx, "calmbrightotter", now.Add(7*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Documents[0].Content != "hello!?" || persisted.Documents[0].CurrentRevision != 2 {
		t.Fatalf("retry changed authoritative document = %#v", persisted.Documents[0])
	}
	if persisted.Documents[0].Name != "second-name" || persisted.MetadataRevision != 2 {
		t.Fatalf("retry changed authoritative metadata = %#v", persisted)
	}
}

func TestHubRetriesCommittedOperationAfterRoomEviction(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 9, 13, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateRoom(ctx, testRoom(now)); err != nil {
		t.Fatal(err)
	}
	hub, err := live.NewHub(store, nil, testHubOptions([]string{"first", "second"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	joined, err := hub.Join(ctx, "calmbrightotter", "first-session", now)
	if err != nil {
		t.Fatal(err)
	}
	operation := live.DocumentOperation{
		OperationID: "evicted-edit", ClientID: "stable-client", DocumentID: "main",
		BaseVersion: 0, Changes: mustChangeSet(t, `[5,[0,"!"]]`),
	}
	if _, err := joined.Session.SubmitDocument(ctx, operation, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := joined.Session.Leave(now.Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if hub.RoomCount() != 0 {
		t.Fatalf("room count after leave = %d, want eviction", hub.RoomCount())
	}
	rejoined, err := hub.Join(ctx, "calmbrightotter", "second-session", now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := rejoined.Session.SubmitDocument(ctx, operation, now.Add(4*time.Second))
	if err != nil || !duplicate.Duplicate || duplicate.Revision != 1 {
		t.Fatalf("evicted-room retry = %#v, %v", duplicate, err)
	}
	if rejoined.State.Documents[0].Content != "hello!" {
		t.Fatalf("reloaded content = %q", rejoined.State.Documents[0].Content)
	}
}

func TestHubCompactsHistoryAtConfiguredByteThreshold(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateRoom(ctx, testRoom(now)); err != nil {
		t.Fatal(err)
	}
	options := testHubOptions([]string{"participant-a"}, nil)
	options.MaxHistoryBytes = 1
	hub, err := live.NewHub(store, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	joined, err := hub.Join(ctx, "calmbrightotter", "session-a", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := joined.Session.SubmitDocument(ctx, live.DocumentOperation{
		OperationID: "large-history-edit", ClientID: "client-a", DocumentID: "main",
		BaseVersion: 0, Changes: mustChangeSet(t, `[5,[0,"!"]]`),
	}, now); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := store.DB().QueryRowContext(ctx, `SELECT count(*) FROM live_changes WHERE room_slug = ?`, "calmbrightotter").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("history rows after byte threshold = %d, want 0", rows)
	}
}

func TestHubShutdownRacesWithJoins(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateRoom(ctx, testRoom(now)); err != nil {
		t.Fatal(err)
	}
	hub, err := live.NewHub(store, nil, live.DefaultHubOptions())
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 33)
	var group sync.WaitGroup
	for index := 0; index < 32; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			_, err := hub.Join(ctx, "calmbrightotter", "session-"+strconv.Itoa(index), now)
			results <- err
		}(index)
	}
	group.Add(1)
	go func() {
		defer group.Done()
		<-start
		results <- hub.Shutdown(ctx, now)
	}()
	close(start)
	group.Wait()
	close(results)
	for err := range results {
		if err != nil && !errors.Is(err, live.ErrHubClosed) && !errors.Is(err, live.ErrRoomExpired) {
			t.Fatalf("join/shutdown race error = %v", err)
		}
	}
	if hub.RoomCount() != 0 {
		t.Fatalf("room count after shutdown race = %d, want 0", hub.RoomCount())
	}
	if _, err := hub.Join(ctx, "calmbrightotter", "after-shutdown", now); !errors.Is(err, live.ErrHubClosed) {
		t.Fatalf("join after shutdown error = %v", err)
	}
}

func TestHubBoundsHistoryAndReturnsResync(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateRoom(ctx, testRoom(now)); err != nil {
		t.Fatal(err)
	}
	options := testHubOptions([]string{"participant-a"}, nil)
	options.MaxHistoryRows = 1
	hub, err := live.NewHub(store, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	joined, err := hub.Join(ctx, "calmbrightotter", "session-a", now)
	if err != nil {
		t.Fatal(err)
	}
	for index, text := range []string{"!", "?"} {
		oldLength := 5 + index
		changes := mustChangeSet(t, `[`+strconv.Itoa(oldLength)+`,[0,"`+text+`"]]`)
		if _, err := joined.Session.SubmitDocument(ctx, live.DocumentOperation{
			OperationID: "edit-" + string(rune('a'+index)), ClientID: "client-a", DocumentID: "main", BaseVersion: index, Changes: changes,
		}, now.Add(time.Duration(index+1)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := joined.Session.SubmitDocument(ctx, live.DocumentOperation{
		OperationID: "stale", ClientID: "client-a", DocumentID: "main", BaseVersion: 0,
		Changes: mustChangeSet(t, `[5,[0,"x"]]`),
	}, now.Add(3*time.Second)); !errors.Is(err, live.ErrDocumentResync) {
		t.Fatalf("stale history error = %v", err)
	}
}

func TestHubSerializesConcurrentEdits(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateRoom(ctx, testRoom(now)); err != nil {
		t.Fatal(err)
	}
	options := testHubOptions(nil, nil)
	options.MaxHistoryRows = 64
	hub, err := live.NewHub(store, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	joined, err := hub.Join(ctx, "calmbrightotter", "session-a", now)
	if err != nil {
		t.Fatal(err)
	}
	const edits = 12
	results := make(chan error, edits)
	var group sync.WaitGroup
	for index := 0; index < edits; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			changes := mustChangeSet(t, `[5,[0,"`+string(rune('a'+index))+`"]]`)
			_, err := joined.Session.SubmitDocument(ctx, live.DocumentOperation{
				OperationID: "concurrent-" + string(rune('a'+index)), ClientID: "client-a", DocumentID: "main", BaseVersion: 0, Changes: changes,
			}, now)
			results <- err
		}(index)
	}
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	state, err := joined.Session.State()
	if err != nil {
		t.Fatal(err)
	}
	if state.Documents[0].Revision != edits || len([]rune(state.Documents[0].Content)) != len([]rune("hello"))+edits {
		t.Fatalf("concurrent state = %#v", state.Documents[0])
	}
}

func TestHubBridgesRetainedDeltasAndRequiresResyncWhenCompacted(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateRoom(ctx, testRoom(now)); err != nil {
		t.Fatal(err)
	}
	options := testHubOptions([]string{"writer", "reader"}, nil)
	options.MaxHistoryRows = 1
	hub, err := live.NewHub(store, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := hub.Join(ctx, "calmbrightotter", "writer-session", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Session.SubmitDocument(ctx, live.DocumentOperation{
		OperationID: "bridge-edit", ClientID: "writer", DocumentID: "main", BaseVersion: 0,
		Changes: mustChangeSet(t, `[5,[0,"!"]]`),
	}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Session.ApplyMetadata(ctx, live.MetadataOperation{
		OperationID: "bridge-rename", ClientID: "writer", BaseVersion: 0,
		Kind: "document_update", DocumentID: "main", Name: "renamed", Language: "plaintext",
	}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	reader, err := hub.Join(ctx, "calmbrightotter", "reader-session", now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	bridge, err := reader.Session.Bridge(live.KnownRevisions{Metadata: 0, Documents: map[string]int{"main": 0, "notes": 0}}, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if bridge.Resync || len(bridge.DocumentChanges) != 1 || len(bridge.MetadataChanges) != 1 {
		t.Fatalf("retained bridge = %#v", bridge)
	}
	if bridge.DocumentChanges[0].Revision != 1 || bridge.MetadataChanges[0].Name != "renamed" {
		t.Fatalf("unexpected bridge changes = %#v", bridge)
	}

	if _, err := writer.Session.SubmitDocument(ctx, live.DocumentOperation{
		OperationID: "compacting-edit", ClientID: "writer", DocumentID: "main", BaseVersion: 1,
		Changes: mustChangeSet(t, `[6,[0,"?"]]`),
	}, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	bridge, err = reader.Session.Bridge(live.KnownRevisions{Metadata: 1, Documents: map[string]int{"main": 0, "notes": 0}}, now.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !bridge.Resync {
		t.Fatalf("compacted bridge should require HTTP resync: %#v", bridge)
	}
}

func TestHubEnforcesWriterViewerCapacityAndWatchOnlyRole(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateRoom(ctx, testRoom(now)); err != nil {
		t.Fatal(err)
	}
	options := testHubOptions([]string{"creator", "viewer-a", "viewer-b", "after-kick"}, nil)
	options.MaxWriters = 1
	options.MaxViewers = 2
	options.MaxParticipants = 3
	hub, err := live.NewHub(store, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	creatorCapability, err := hub.IssueCreatorCapability("calmbrightotter", now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	creator, err := hub.JoinWithCreator(ctx, "calmbrightotter", "creator-session", creatorCapability, now)
	if err != nil {
		t.Fatal(err)
	}
	if !creator.Session.IsCreator() || creator.Participant.Role != live.ParticipantWriter {
		t.Fatalf("creator session = %#v", creator)
	}
	viewerA, err := hub.Join(ctx, "calmbrightotter", "viewer-a-session", now)
	if err != nil {
		t.Fatal(err)
	}
	viewerB, err := hub.Join(ctx, "calmbrightotter", "viewer-b-session", now)
	if err != nil {
		t.Fatal(err)
	}
	if viewerA.Participant.Role != live.ParticipantWatchOnly || viewerB.Participant.Role != live.ParticipantWatchOnly {
		t.Fatalf("viewer roles = %q, %q", viewerA.Participant.Role, viewerB.Participant.Role)
	}
	if _, err := hub.Join(ctx, "calmbrightotter", "overflow-session", now); !errors.Is(err, live.ErrParticipantLimit) {
		t.Fatalf("overflow join error = %v", err)
	}
	if _, err := viewerA.Session.SubmitDocument(ctx, live.DocumentOperation{
		OperationID: "viewer-edit", ClientID: "viewer-a", DocumentID: "main", BaseVersion: 0,
		Changes: mustChangeSet(t, `[5,[0,"!"]]`),
	}, now); !errors.Is(err, live.ErrWatchOnly) {
		t.Fatalf("watch-only edit error = %v", err)
	}
	if _, err := viewerA.Session.SetWatchOnly(true, now); !errors.Is(err, live.ErrCreatorRequired) {
		t.Fatalf("non-creator mode error = %v", err)
	}
	if err := viewerA.Session.RemoveParticipant(creator.Participant.ID, now); !errors.Is(err, live.ErrCreatorRequired) {
		t.Fatalf("non-creator removal error = %v", err)
	}
	if _, err := creator.Session.SetWatchOnly(true, now); err != nil {
		t.Fatal(err)
	}
	if _, err := creator.Session.SubmitDocument(ctx, live.DocumentOperation{
		OperationID: "creator-after-watch-only", ClientID: "creator", DocumentID: "main", BaseVersion: 0,
		Changes: mustChangeSet(t, `[5,[0,"!"]]`),
	}, now); !errors.Is(err, live.ErrWatchOnly) {
		t.Fatalf("watch-only creator edit error = %v", err)
	}
	writableState, err := creator.Session.SetWatchOnly(false, now)
	if err != nil {
		t.Fatal(err)
	}
	creatorRole := live.ParticipantRole("")
	for _, participant := range writableState.Participants {
		if participant.ID == creator.Participant.ID {
			creatorRole = participant.Role
		}
	}
	if creatorRole != live.ParticipantWriter {
		t.Fatalf("creator role after restoring writable mode = %q", creatorRole)
	}
	if err := creator.Session.RemoveParticipant(viewerA.Participant.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := viewerA.Session.Heartbeat(now); !errors.Is(err, live.ErrParticipantNotFound) {
		t.Fatalf("removed session heartbeat error = %v", err)
	}
	if _, err := hub.Join(ctx, "calmbrightotter", "viewer-a-session", now); !errors.Is(err, live.ErrSessionRemoved) {
		t.Fatalf("removed session rejoin error = %v", err)
	}
	joinedAfterKick, err := hub.Join(ctx, "calmbrightotter", "new-session", now)
	if err != nil {
		t.Fatal(err)
	}
	if joinedAfterKick.Participant.Role != live.ParticipantWatchOnly {
		t.Fatalf("new session role = %q, want watch-only", joinedAfterKick.Participant.Role)
	}
}

func TestHubWatchOnlyModePreservesWriterCapacitySlots(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateRoom(ctx, testRoom(now)); err != nil {
		t.Fatal(err)
	}
	options := testHubOptions([]string{"creator", "writer", "viewer", "replacement"}, nil)
	options.MaxWriters = 2
	options.MaxViewers = 1
	options.MaxParticipants = 3
	hub, err := live.NewHub(store, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	creatorCapability, err := hub.IssueCreatorCapability("calmbrightotter", now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	creator, err := hub.JoinWithCreator(ctx, "calmbrightotter", "creator-session", creatorCapability, now)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := hub.Join(ctx, "calmbrightotter", "writer-session", now)
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := hub.Join(ctx, "calmbrightotter", "viewer-session", now)
	if err != nil {
		t.Fatal(err)
	}
	if writer.Participant.Role != live.ParticipantWriter || viewer.Participant.Role != live.ParticipantWatchOnly {
		t.Fatalf("initial roles = writer %q, viewer %q", writer.Participant.Role, viewer.Participant.Role)
	}
	if _, err := creator.Session.SetWatchOnly(true, now); err != nil {
		t.Fatal(err)
	}
	if err := creator.Session.RemoveParticipant(writer.Participant.ID, now); err != nil {
		t.Fatal(err)
	}
	replacement, err := hub.Join(ctx, "calmbrightotter", "replacement-session", now)
	if err != nil {
		t.Fatalf("replacement should use the released writer capacity slot: %v", err)
	}
	if replacement.Participant.Role != live.ParticipantWatchOnly {
		t.Fatalf("replacement role = %q, want watch-only", replacement.Participant.Role)
	}
	if _, err := hub.Join(ctx, "calmbrightotter", "overflow-session", now); !errors.Is(err, live.ErrParticipantLimit) {
		t.Fatalf("overflow join error = %v", err)
	}
}

func TestHubCreatorCapabilityAndPresenceAreProcessLocal(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateRoom(ctx, testRoom(now)); err != nil {
		t.Fatal(err)
	}
	hub, err := live.NewHub(store, nil, testHubOptions([]string{"creator", "collaborator", "after-restart"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	creatorCapability, err := hub.IssueCreatorCapability("calmbrightotter", now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	creator, err := hub.JoinWithCreator(ctx, "calmbrightotter", "creator-session", creatorCapability, now)
	if err != nil {
		t.Fatal(err)
	}
	collaborator, err := hub.Join(ctx, "calmbrightotter", "collaborator-session", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collaborator.Session.UpdatePresence(live.PresenceUpdate{
		CurrentTab: "notes", DocumentID: "notes", Revision: 0, Anchor: 1, Head: 3,
	}, now); err != nil {
		t.Fatal(err)
	}
	state, err := creator.Session.State()
	if err != nil {
		t.Fatal(err)
	}
	for index := range state.Participants {
		if state.Participants[index].ID == collaborator.Participant.ID && state.Participants[index].Cursor != nil {
			state.Participants[index].Cursor.Anchor = 99
		}
	}
	current, err := creator.Session.State()
	if err != nil {
		t.Fatal(err)
	}
	for _, participant := range current.Participants {
		if participant.ID == collaborator.Participant.ID && (participant.Cursor == nil || participant.Cursor.Anchor != 1) {
			t.Fatalf("participant clone mutated authority state: %#v", participant)
		}
	}
	if _, err := creator.Session.ApplyMetadata(ctx, live.MetadataOperation{
		OperationID: "delete-notes", ClientID: "creator", BaseVersion: 0,
		Kind: "document_delete", DocumentID: "notes",
	}, now); err != nil {
		t.Fatal(err)
	}
	current, err = creator.Session.State()
	if err != nil {
		t.Fatal(err)
	}
	for _, participant := range current.Participants {
		if participant.CurrentTab == "notes" || (participant.Cursor != nil && participant.Cursor.DocumentID == "notes") {
			t.Fatalf("deleted document remains in presence: %#v", participant)
		}
	}
	if err := hub.Shutdown(ctx, now); err != nil {
		t.Fatal(err)
	}
	restarted, err := live.NewHub(store, nil, testHubOptions([]string{"after-restart"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	joined, err := restarted.JoinWithCreator(ctx, "calmbrightotter", "after-restart-session", creatorCapability, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if joined.Session.IsCreator() || len(joined.State.Participants) != 1 {
		t.Fatalf("creator capability or presence survived restart: %#v", joined)
	}
}

func TestHubCreatorCapabilityOutlivesOrdinarySessionAndCanBeRevoked(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateRoom(ctx, testRoom(now)); err != nil {
		t.Fatal(err)
	}
	hub, err := live.NewHub(store, nil, testHubOptions([]string{"creator", "after-revoke"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	capability, err := hub.IssueCreatorCapability("calmbrightotter", now.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	joined, err := hub.JoinWithCreator(ctx, "calmbrightotter", "creator-session", capability, now.Add(16*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !joined.Session.IsCreator() {
		t.Fatal("creator capability should outlive the ordinary access session")
	}
	hub.RevokeCreatorCapability("calmbrightotter")
	joined, err = hub.JoinWithCreator(ctx, "calmbrightotter", "after-revoke-session", capability, now.Add(17*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if joined.Session.IsCreator() {
		t.Fatal("revoked creator capability retained authority")
	}
	if _, err := hub.JoinWithCreator(ctx, "calmbrightotter", "expired-room-session", capability, now.Add(24*time.Hour)); !errors.Is(err, live.ErrRoomExpired) {
		t.Fatalf("expired room join error = %v", err)
	}
}

type recordingStore struct {
	live.RoomStore
	mu          sync.Mutex
	events      []string
	failCommits int
}

func (store *recordingStore) CommitChange(ctx context.Context, commit live.ChangeCommit, now time.Time) error {
	store.mu.Lock()
	store.events = append(store.events, "commit")
	if store.failCommits > 0 {
		store.failCommits--
		store.mu.Unlock()
		return errors.New("injected atomic commit failure")
	}
	store.mu.Unlock()
	return store.RoomStore.CommitChange(ctx, commit, now)
}

func (store *recordingStore) AppendChanges(ctx context.Context, slug string, changes []live.ChangeRecord, now time.Time) error {
	store.mu.Lock()
	store.events = append(store.events, "append")
	store.mu.Unlock()
	return store.RoomStore.AppendChanges(ctx, slug, changes, now)
}

func (store *recordingStore) SaveSnapshot(ctx context.Context, snapshot live.RoomSnapshot, now time.Time) error {
	store.mu.Lock()
	store.events = append(store.events, "save")
	store.mu.Unlock()
	return store.RoomStore.SaveSnapshot(ctx, snapshot, now)
}

func (store *recordingStore) CompactChanges(ctx context.Context, slug, streamKind, streamID string, throughRevision int, now time.Time) error {
	store.mu.Lock()
	store.events = append(store.events, "compact")
	store.mu.Unlock()
	return store.RoomStore.CompactChanges(ctx, slug, streamKind, streamID, throughRevision, now)
}

func testRoom(now time.Time) live.RoomSnapshot {
	return live.RoomSnapshot{
		Slug:      "calmbrightotter",
		CreatedAt: now,
		ExpiresAt: now.Add(24 * time.Hour),
		Documents: []live.DocumentSnapshot{
			{ID: "main", Name: "main.go", Language: "go", Content: "hello", Position: 0, UpdatedAt: now},
			{ID: "notes", Name: "Notes", Language: "plaintext", Content: "notes", Position: 1, UpdatedAt: now},
		},
	}
}

func testHubOptions(participants, documents []string) live.HubOptions {
	options := live.DefaultHubOptions()
	if len(participants) > 0 {
		options.ParticipantID = sequenceGenerator(participants)
	}
	if len(documents) > 0 {
		options.DocumentID = sequenceGenerator(documents)
	}
	return options
}

func sequenceGenerator(values []string) func() (string, error) {
	index := 0
	return func() (string, error) {
		if index >= len(values) {
			return "", errors.New("sequence exhausted")
		}
		value := values[index]
		index++
		return value, nil
	}
}

func mustChangeSet(t *testing.T, value string) livecollab.ChangeSet {
	t.Helper()
	changes, err := livecollab.ParseChangeSetJSON([]byte(value))
	if err != nil {
		t.Fatal(err)
	}
	return changes
}
