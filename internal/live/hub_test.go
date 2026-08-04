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
	if !reflect.DeepEqual(recording.events, []string{"append", "save", "compact", "append", "save", "compact"}) {
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

func TestHubReplaysCommittedHistoryWhenSnapshotSaveIsInterrupted(t *testing.T) {
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
	recording := &recordingStore{RoomStore: store, failSaves: 1}
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
	persisted, err := store.GetRoomSnapshot(ctx, "calmbrightotter", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Documents[0].Content != "hello" || persisted.Documents[0].CurrentRevision != 1 || persisted.Documents[0].SnapshotRevision != 0 {
		t.Fatalf("interrupted durable state = %#v", persisted.Documents[0])
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

type recordingStore struct {
	live.RoomStore
	mu        sync.Mutex
	events    []string
	failSaves int
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
	if store.failSaves > 0 {
		store.failSaves--
		store.mu.Unlock()
		return errors.New("injected snapshot failure")
	}
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
