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
	alice, err := joinHub(hub, ctx, "calmbrightotter", "session-a", now)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := joinHub(hub, ctx, "calmbrightotter", "session-b", now)
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
	joined, err := joinHub(restarted, ctx, "calmbrightotter", "new-session", now.Add(3*time.Second))
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

func TestHubJoinIdentitySeparatesParticipantConnectionAndClient(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateRoom(ctx, testRoom(now)); err != nil {
		t.Fatal(err)
	}
	hub, err := live.NewHub(store, nil, testHubOptions(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	identity := live.JoinIdentity{
		ParticipantCredential: "browser-credential",
		ConnectionID:          "connection-one",
		ClientID:              "operation-client-one",
		PreferredName:         "Quiet Otter",
		PreferredNameSet:      true,
	}
	joined, err := hub.JoinWithIdentity(ctx, "calmbrightotter", identity, live.CreatorCapability{}, now)
	if err != nil {
		t.Fatal(err)
	}
	wantID, err := live.ParticipantIDForRoom("calmbrightotter", identity.ParticipantCredential)
	if err != nil {
		t.Fatal(err)
	}
	if joined.Participant.ID != wantID || joined.Participant.Nickname != identity.PreferredName || joined.Participant.AccessClass != live.ParticipantCollaborator || !joined.Participant.CanEdit || joined.Participant.ConnectionCount != 1 {
		t.Fatalf("joined participant = %#v", joined.Participant)
	}
	if joined.Session.ConnectionID() != identity.ConnectionID || joined.Session.ClientID() != identity.ClientID {
		t.Fatalf("session identities = connection %q, client %q", joined.Session.ConnectionID(), joined.Session.ClientID())
	}
	overlap := identity
	overlap.ConnectionID = "connection-two"
	overlap.ClientID = "operation-client-two"
	overlapped, err := hub.JoinWithIdentity(ctx, "calmbrightotter", overlap, live.CreatorCapability{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if overlapped.Participant.ID != wantID || overlapped.Participant.ConnectionCount != 2 || len(overlapped.State.Participants) != 1 {
		t.Fatalf("grouped connection identity = %#v", overlapped)
	}
	if err := joined.Session.Disconnect(now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	afterDisconnect, err := overlapped.Session.Participant()
	if err != nil {
		t.Fatal(err)
	}
	if afterDisconnect.Status != live.ParticipantConnected || afterDisconnect.ConnectionCount != 1 {
		t.Fatalf("remaining grouped connection = %#v", afterDisconnect)
	}
	reconnected, err := hub.JoinWithIdentity(ctx, "calmbrightotter", identity, live.CreatorCapability{}, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !reconnected.Reconnected || reconnected.Participant.ID != wantID || reconnected.Participant.ConnectionCount != 2 || reconnected.Session.ConnectionID() != identity.ConnectionID || reconnected.Session.ClientID() != identity.ClientID {
		t.Fatalf("reconnected identity = %#v", reconnected)
	}
	if err := joined.Session.Heartbeat(now.Add(2 * time.Second)); !errors.Is(err, live.ErrParticipantInactive) {
		t.Fatalf("stale connection heartbeat error = %v", err)
	}
	if err := hub.Shutdown(ctx, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	restarted, err := live.NewHub(store, nil, testHubOptions(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Shutdown(ctx, now.Add(time.Hour))
	afterRestart, err := restarted.JoinWithIdentity(ctx, "calmbrightotter", identity, live.CreatorCapability{}, now.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if afterRestart.Reconnected || afterRestart.Participant.ID != wantID || afterRestart.Participant.Color != joined.Participant.Color || afterRestart.Participant.Nickname != identity.PreferredName {
		t.Fatalf("restart identity = %#v", afterRestart)
	}
	collision := identity
	collision.ParticipantCredential = "another-browser"
	collision.ConnectionID = "another-connection"
	collision.ClientID = "another-client"
	colliding, err := restarted.JoinWithIdentity(ctx, "calmbrightotter", collision, live.CreatorCapability{}, now.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if colliding.Participant.Nickname == identity.PreferredName {
		t.Fatal("preferred-name collision bypassed active-room uniqueness")
	}
}

func TestHubGroupsBoundedConnectionsAndKeepsPresenceConnectionScoped(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 10, 13, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateRoom(ctx, testRoom(now)); err != nil {
		t.Fatal(err)
	}
	options := testHubOptions(nil, nil)
	options.MaxConnectionsPerParticipant = 3
	hub, err := live.NewHub(store, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Shutdown(ctx, now.Add(time.Hour))

	identity := func(connection string) live.JoinIdentity {
		return live.JoinIdentity{
			ParticipantCredential: "shared-browser-credential",
			ConnectionID:          connection,
			ClientID:              "client-" + connection,
			PreferredName:         "Quiet Otter",
			PreferredNameSet:      true,
		}
	}
	first, err := hub.JoinWithIdentity(ctx, "calmbrightotter", identity("one"), live.CreatorCapability{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hub.JoinWithIdentity(ctx, "calmbrightotter", identity("one"), live.CreatorCapability{}, now); !errors.Is(err, live.ErrSessionActive) {
		t.Fatalf("duplicate connection error = %v, want %v", err, live.ErrSessionActive)
	}
	second, err := hub.JoinWithIdentity(ctx, "calmbrightotter", identity("two"), live.CreatorCapability{}, now)
	if err != nil {
		t.Fatal(err)
	}
	third, err := hub.JoinWithIdentity(ctx, "calmbrightotter", identity("three"), live.CreatorCapability{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.Participant.ID != second.Participant.ID || first.Participant.ID != third.Participant.ID || third.Participant.ConnectionCount != 3 || len(third.State.Participants) != 1 {
		t.Fatalf("grouped participants = first %#v, second %#v, third %#v", first.Participant, second.Participant, third.Participant)
	}
	if _, err := hub.JoinWithIdentity(ctx, "calmbrightotter", identity("four"), live.CreatorCapability{}, now); !errors.Is(err, live.ErrConnectionLimit) {
		t.Fatalf("connection overflow error = %v, want %v", err, live.ErrConnectionLimit)
	}

	firstEdit, err := first.Session.SubmitDocument(ctx, live.DocumentOperation{
		OperationID: "first-connection-edit", ClientID: "client-one", DocumentID: "main", BaseVersion: 0,
		Changes: mustChangeSet(t, `[5,[0,"!"]]`),
	}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	typingPresence, err := first.Session.UpdatePresence(live.PresenceUpdate{
		CurrentTab: "main", DocumentID: "main", Revision: 0, Anchor: 5, Head: 5,
	}, now.Add(1500*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if typingPresence.Cursor == nil || typingPresence.Cursor.Anchor != 6 || typingPresence.Cursor.Head != 6 {
		t.Fatalf("typing cursor = %#v, want collapsed position 6 after accepted insertion", typingPresence.Cursor)
	}
	secondEdit, err := second.Session.SubmitDocument(ctx, live.DocumentOperation{
		OperationID: "second-connection-edit", ClientID: "client-two", DocumentID: "main", BaseVersion: 1,
		Changes: mustChangeSet(t, `[6,[0,"?"]]`),
	}, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if firstEdit.ClientID != "client-one" || secondEdit.ClientID != "client-two" || secondEdit.Revision != 2 {
		t.Fatalf("connection operation streams = %#v, %#v", firstEdit, secondEdit)
	}
	if _, err := first.Session.UpdatePresence(live.PresenceUpdate{
		CurrentTab: "notes", DocumentID: "notes", Revision: 0, Anchor: 1, Head: 3,
	}, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	presence, err := second.Session.UpdatePresence(live.PresenceUpdate{
		CurrentTab: "main", DocumentID: "main", Revision: 2, Anchor: 2, Head: 4,
	}, now.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if presence.CurrentTab != "main" || presence.Cursor == nil || presence.Cursor.DocumentID != "main" || len(presence.Cursors) != 2 || presence.Cursors[0].ConnectionID != "one" || presence.Cursors[1].ConnectionID != "two" {
		t.Fatalf("connection-scoped presence = %#v", presence)
	}
	if err := first.Session.Heartbeat(now.Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	afterHeartbeat, err := second.Session.Participant()
	if err != nil {
		t.Fatal(err)
	}
	if afterHeartbeat.CurrentTab != "main" || afterHeartbeat.Cursor == nil || afterHeartbeat.Cursor.DocumentID != "main" {
		t.Fatalf("heartbeat changed latest active tab = %#v", afterHeartbeat)
	}

	if err := first.Session.Disconnect(now.Add(6 * time.Second)); err != nil {
		t.Fatal(err)
	}
	remaining, err := second.Session.Participant()
	if err != nil {
		t.Fatal(err)
	}
	if remaining.Status != live.ParticipantConnected || remaining.ConnectionCount != 2 || len(remaining.Cursors) != 1 || remaining.Cursors[0].ConnectionID != "two" {
		t.Fatalf("participant after one connection closes = %#v", remaining)
	}
	reloaded, err := hub.JoinWithIdentity(ctx, "calmbrightotter", identity("one"), live.CreatorCapability{}, now.Add(6*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.Reconnected || reloaded.Participant.ConnectionCount != 3 || len(reloaded.Participant.Cursors) != 1 {
		t.Fatalf("reload overlap = %#v", reloaded.Participant)
	}
	if err := first.Session.Disconnect(now.Add(7 * time.Second)); !errors.Is(err, live.ErrParticipantInactive) {
		t.Fatalf("stale disconnect error = %v", err)
	}
	if err := first.Session.Heartbeat(now.Add(7 * time.Second)); !errors.Is(err, live.ErrParticipantInactive) {
		t.Fatalf("stale heartbeat error = %v", err)
	}
	afterStale, err := reloaded.Session.Participant()
	if err != nil {
		t.Fatal(err)
	}
	if afterStale.ConnectionCount != 3 || afterStale.Status != live.ParticipantConnected {
		t.Fatalf("stale connection changed participant = %#v", afterStale)
	}

	if err := second.Session.Disconnect(now.Add(8 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := third.Session.Disconnect(now.Add(9 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := reloaded.Session.Disconnect(now.Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	disconnected, err := hub.State("calmbrightotter")
	if err != nil {
		t.Fatal(err)
	}
	if len(disconnected.Participants) != 1 || disconnected.Participants[0].Status != live.ParticipantConnectionLost || disconnected.Participants[0].ConnectionCount != 0 || disconnected.Participants[0].Cursor != nil || len(disconnected.Participants[0].Cursors) != 0 {
		t.Fatalf("final disconnect state = %#v", disconnected.Participants)
	}
	reopened, err := hub.JoinWithIdentity(ctx, "calmbrightotter", identity("four"), live.CreatorCapability{}, now.Add(11*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !reopened.Reconnected || reopened.Participant.ID != first.Participant.ID || !reopened.Participant.JoinedAt.Equal(first.Participant.JoinedAt) || reopened.Participant.ConnectionCount != 1 {
		t.Fatalf("final-connection grace reclaim = %#v", reopened.Participant)
	}
	if err := hub.Shutdown(ctx, now.Add(12*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Session.State(); !errors.Is(err, live.ErrRoomExpired) {
		t.Fatalf("grouped session after shutdown error = %v", err)
	}
}

func TestHubParticipantCapacityCountsBrowserIdentityUntilFinalConnectionGraceExpires(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 10, 14, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateRoom(ctx, testRoom(now)); err != nil {
		t.Fatal(err)
	}
	options := testHubOptions(nil, nil)
	options.MaxWriters, options.MaxViewers, options.MaxParticipants = 1, 1, 2
	hub, err := live.NewHub(store, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Shutdown(ctx, now.Add(time.Hour))
	join := func(credential, connection string, at time.Time) (live.JoinResult, error) {
		return hub.JoinWithIdentity(ctx, "calmbrightotter", live.JoinIdentity{
			ParticipantCredential: credential, ConnectionID: connection, ClientID: "client-" + connection,
		}, live.CreatorCapability{}, at)
	}
	first, err := join("browser-a", "a-one", now)
	if err != nil {
		t.Fatal(err)
	}
	secondTab, err := join("browser-a", "a-two", now)
	if err != nil {
		t.Fatal(err)
	}
	secondBrowser, err := join("browser-b", "b-one", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondBrowser.State.Participants) != 2 || secondTab.Participant.ConnectionCount != 2 || first.Participant.ID == secondBrowser.Participant.ID {
		t.Fatalf("participant-scoped capacity = %#v", secondBrowser.State.Participants)
	}
	if _, err := join("browser-c", "c-one", now); !errors.Is(err, live.ErrParticipantLimit) {
		t.Fatalf("full room error = %v", err)
	}
	if err := first.Session.Disconnect(now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := join("browser-c", "c-one", now.Add(time.Second)); !errors.Is(err, live.ErrParticipantLimit) {
		t.Fatalf("single-tab close released participant capacity: %v", err)
	}
	if err := secondTab.Session.Disconnect(now.Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := join("browser-c", "c-one", now.Add(2*time.Second)); !errors.Is(err, live.ErrParticipantLimit) {
		t.Fatalf("reconnect grace released participant capacity: %v", err)
	}
	if _, err := hub.Sweep(ctx, now.Add(2*time.Second+options.ReconnectGrace+time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	thirdBrowser, err := join("browser-c", "c-one", now.Add(3*time.Second+options.ReconnectGrace))
	if err != nil {
		t.Fatal(err)
	}
	if thirdBrowser.Participant.ID == first.Participant.ID || thirdBrowser.Participant.ConnectionCount != 1 {
		t.Fatalf("capacity replacement = %#v", thirdBrowser.Participant)
	}
}

func TestHubSweepExpiresOnlyStaleConnections(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 10, 15, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateRoom(ctx, testRoom(now)); err != nil {
		t.Fatal(err)
	}
	options := testHubOptions(nil, nil)
	options.HeartbeatInterval = time.Second
	options.ReconnectGrace = 2 * time.Second
	options.ParticipantTimeout = 3 * time.Second
	hub, err := live.NewHub(store, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Shutdown(ctx, now.Add(time.Hour))
	identity := func(connection string) live.JoinIdentity {
		return live.JoinIdentity{ParticipantCredential: "browser", ConnectionID: connection, ClientID: "client-" + connection}
	}
	stale, err := hub.JoinWithIdentity(ctx, "calmbrightotter", identity("stale"), live.CreatorCapability{}, now)
	if err != nil {
		t.Fatal(err)
	}
	healthy, err := hub.JoinWithIdentity(ctx, "calmbrightotter", identity("healthy"), live.CreatorCapability{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := healthy.Session.Heartbeat(now.Add(2500 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := hub.Sweep(ctx, now.Add(3*time.Second+time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	participant, err := healthy.Session.Participant()
	if err != nil {
		t.Fatal(err)
	}
	if participant.Status != live.ParticipantConnected || participant.ConnectionCount != 1 {
		t.Fatalf("healthy grouped participant = %#v", participant)
	}
	if err := stale.Session.Heartbeat(now.Add(3 * time.Second)); !errors.Is(err, live.ErrParticipantInactive) {
		t.Fatalf("stale connection heartbeat error = %v", err)
	}
	if err := healthy.Session.Heartbeat(now.Add(4 * time.Second)); err != nil {
		t.Fatalf("healthy connection was invalidated: %v", err)
	}
}

func TestHubConcurrentConnectionAdmissionAndFinalDisconnectStayGrouped(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 10, 16, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateRoom(ctx, testRoom(now)); err != nil {
		t.Fatal(err)
	}
	hub, err := live.NewHub(store, nil, testHubOptions(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Shutdown(ctx, now.Add(time.Hour))

	type joinOutcome struct {
		result live.JoinResult
		err    error
	}
	outcomes := make(chan joinOutcome, 16)
	var joins sync.WaitGroup
	for index := 0; index < 16; index++ {
		joins.Add(1)
		go func(index int) {
			defer joins.Done()
			connection := "connection-" + strconv.Itoa(index)
			result, joinErr := hub.JoinWithIdentity(ctx, "calmbrightotter", live.JoinIdentity{
				ParticipantCredential: "concurrent-browser", ConnectionID: connection, ClientID: "client-" + connection,
			}, live.CreatorCapability{}, now)
			outcomes <- joinOutcome{result: result, err: joinErr}
		}(index)
	}
	joins.Wait()
	close(outcomes)
	var sessions []*live.RoomSession
	limited := 0
	for outcome := range outcomes {
		switch {
		case outcome.err == nil:
			sessions = append(sessions, outcome.result.Session)
		case errors.Is(outcome.err, live.ErrConnectionLimit):
			limited++
		default:
			t.Fatalf("concurrent join error = %v", outcome.err)
		}
	}
	if len(sessions) != 8 || limited != 8 {
		t.Fatalf("concurrent admission = %d joined, %d limited", len(sessions), limited)
	}
	state, err := sessions[0].State()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Participants) != 1 || state.Participants[0].ConnectionCount != 8 {
		t.Fatalf("concurrent grouped state = %#v", state.Participants)
	}

	var disconnects sync.WaitGroup
	disconnectErrs := make(chan error, len(sessions))
	for _, session := range sessions {
		disconnects.Add(1)
		go func(session *live.RoomSession) {
			defer disconnects.Done()
			disconnectErrs <- session.Disconnect(now.Add(time.Second))
		}(session)
	}
	disconnects.Wait()
	close(disconnectErrs)
	for disconnectErr := range disconnectErrs {
		if disconnectErr != nil {
			t.Fatalf("concurrent disconnect error = %v", disconnectErr)
		}
	}
	disconnected, err := hub.State("calmbrightotter")
	if err != nil {
		t.Fatal(err)
	}
	if len(disconnected.Participants) != 1 || disconnected.Participants[0].Status != live.ParticipantConnectionLost || disconnected.Participants[0].ConnectionCount != 0 {
		t.Fatalf("concurrent final disconnect = %#v", disconnected.Participants)
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
	alice, err := joinHub(hub, ctx, "calmbrightotter", "session-a", now)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := joinHub(hub, ctx, "calmbrightotter", "session-b", now)
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
	joined, err := joinHub(hub, ctx, "calmbrightotter", "session-a", now)
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
	rejoined, err := joinHub(restarted, ctx, "calmbrightotter", "new-session", now.Add(time.Second))
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
	first, err := joinHub(hub, ctx, "calmbrightotter", "session-a", now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := joinHub(hub, ctx, "calmbrightotter", "session-b", now)
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
	if disconnected.Status != live.ParticipantConnectionLost || disconnected.Cursor != nil || len(disconnected.Cursors) != 0 || disconnected.CurrentTab != "notes" {
		t.Fatalf("disconnected presence = %#v", disconnected)
	}
	reconnected, err := joinHub(hub, ctx, "calmbrightotter", "session-a", now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !reconnected.Reconnected || reconnected.Participant.ID != first.Participant.ID || reconnected.Participant.Nickname != first.Participant.Nickname || reconnected.Participant.Color != first.Participant.Color || !reconnected.Participant.JoinedAt.Equal(first.Participant.JoinedAt) {
		t.Fatalf("reconnected participant = %#v; first = %#v", reconnected.Participant, first.Participant)
	}
	if err := first.Session.Heartbeat(now.Add(3 * time.Second)); !errors.Is(err, live.ErrParticipantInactive) {
		t.Fatalf("stale connection heartbeat error = %v", err)
	}
	newHub, err := live.NewHub(store, nil, testHubOptions([]string{"participant-after"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	joined, err := joinHub(newHub, ctx, "calmbrightotter", "new-session", now.Add(5*time.Second))
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
		joined, err := joinHub(hub, ctx, "calmbrightotter", "session-"+strconv.Itoa(visit), joinedAt)
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

	stale, err := joinHub(hub, ctx, "calmbrightotter", "stale-session", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := joinHub(hub, ctx, "calmbrightotter", "active-session", now); err != nil {
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
	joined, err := joinHub(hub, ctx, room.Slug, "session-a", now)
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
	joined, err := joinHub(hub, ctx, "calmbrightotter", "session-a", now)
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
	joined, err := joinHub(hub, ctx, "calmbrightotter", "session-a", now)
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
	joined, err := joinHub(hub, ctx, "calmbrightotter", "session-before", now)
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
	rejoined, err := joinHub(restarted, ctx, "calmbrightotter", "session-after", now.Add(4*time.Second))
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
	options := testHubOptions([]string{"first", "second"}, nil)
	hub, err := live.NewHub(store, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	joined, err := joinHub(hub, ctx, "calmbrightotter", "first-session", now)
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
	if err := joined.Session.Disconnect(now.Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if removed, err := hub.Sweep(ctx, now.Add(2*time.Second+options.ReconnectGrace+time.Nanosecond)); err != nil || removed != 1 {
		t.Fatalf("sweep after reconnect grace = %d, %v; want 1, nil", removed, err)
	}
	if hub.RoomCount() != 0 {
		t.Fatalf("room count after leave = %d, want eviction", hub.RoomCount())
	}
	rejoined, err := joinHub(hub, ctx, "calmbrightotter", "second-session", now.Add(3*time.Second))
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
	joined, err := joinHub(hub, ctx, "calmbrightotter", "session-a", now)
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
			_, err := joinHub(hub, ctx, "calmbrightotter", "session-"+strconv.Itoa(index), now)
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
	if _, err := joinHub(hub, ctx, "calmbrightotter", "after-shutdown", now); !errors.Is(err, live.ErrHubClosed) {
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
	joined, err := joinHub(hub, ctx, "calmbrightotter", "session-a", now)
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
	joined, err := joinHub(hub, ctx, "calmbrightotter", "session-a", now)
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
	writer, err := joinHub(hub, ctx, "calmbrightotter", "writer-session", now)
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
	reader, err := joinHub(hub, ctx, "calmbrightotter", "reader-session", now.Add(2*time.Second))
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
	creatorCapability := createTestRoomWithCreator(t, ctx, store, now)
	options := testHubOptions([]string{"creator", "viewer-a", "viewer-b"}, nil)
	options.MaxWriters = 1
	options.MaxViewers = 2
	options.MaxParticipants = 3
	hub, err := live.NewHub(store, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	creator, err := joinHubWithCreator(hub, ctx, "calmbrightotter", "creator-session", creatorCapability, now)
	if err != nil {
		t.Fatal(err)
	}
	if !creator.Session.IsCreator() || creator.Participant.AccessClass != live.ParticipantCreator || !creator.Participant.CanEdit || creator.Participant.ConnectionCount != 1 {
		t.Fatalf("creator session = %#v", creator)
	}
	viewerA, err := joinHub(hub, ctx, "calmbrightotter", "viewer-a-session", now)
	if err != nil {
		t.Fatal(err)
	}
	viewerB, err := joinHub(hub, ctx, "calmbrightotter", "viewer-b-session", now)
	if err != nil {
		t.Fatal(err)
	}
	if viewerA.Participant.AccessClass != live.ParticipantViewer || viewerB.Participant.AccessClass != live.ParticipantViewer || viewerA.Participant.CanEdit || viewerB.Participant.CanEdit {
		t.Fatalf("viewer authority = %#v, %#v", viewerA.Participant, viewerB.Participant)
	}
	if _, err := joinHub(hub, ctx, "calmbrightotter", "overflow-session", now); !errors.Is(err, live.ErrParticipantLimit) {
		t.Fatalf("overflow join error = %v", err)
	}
	if _, err := viewerA.Session.SubmitDocument(ctx, live.DocumentOperation{
		OperationID: "viewer-edit", ClientID: "viewer-a", DocumentID: "main", BaseVersion: 0,
		Changes: mustChangeSet(t, `[5,[0,"!"]]`),
	}, now); !errors.Is(err, live.ErrWatchOnly) {
		t.Fatalf("watch-only edit error = %v", err)
	}
	if _, err := viewerA.Session.SetWatchOnly(ctx, true, now); !errors.Is(err, live.ErrCreatorRequired) {
		t.Fatalf("non-creator mode error = %v", err)
	}
	if _, err := creator.Session.SetWatchOnly(ctx, true, now); err != nil {
		t.Fatal(err)
	}
	lockedCreatorEdit, err := creator.Session.SubmitDocument(ctx, live.DocumentOperation{
		OperationID: "creator-after-watch-only", ClientID: "creator", DocumentID: "main", BaseVersion: 0,
		Changes: mustChangeSet(t, `[5,[0,"!"]]`),
	}, now)
	if err != nil || lockedCreatorEdit.Revision != 1 {
		t.Fatalf("locked creator edit = %#v, %v", lockedCreatorEdit, err)
	}
	writableState, err := creator.Session.SetWatchOnly(ctx, false, now)
	if err != nil {
		t.Fatal(err)
	}
	creatorCanEdit := false
	for _, participant := range writableState.Participants {
		if participant.ID == creator.Participant.ID {
			creatorCanEdit = participant.CanEdit
		}
	}
	if !creatorCanEdit {
		t.Fatal("creator cannot edit after restoring writable mode")
	}
}

func TestHubAccessClassAndLockTruthTable(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 10, 17, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	creatorCapability := createTestRoomWithCreator(t, ctx, store, now)
	options := testHubOptions(nil, nil)
	options.MaxWriters, options.MaxViewers, options.MaxParticipants = 2, 2, 4
	hub, err := live.NewHub(store, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Shutdown(ctx, now.Add(time.Hour))

	creator, err := joinHubWithCreator(hub, ctx, "calmbrightotter", "creator-session", creatorCapability, now)
	if err != nil {
		t.Fatal(err)
	}
	locked, err := creator.Session.SetWatchOnly(ctx, true, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !locked.WatchOnly {
		t.Fatal("room did not lock")
	}
	collaborator, err := joinHub(hub, ctx, "calmbrightotter", "collaborator-session", now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	viewerA, err := joinHub(hub, ctx, "calmbrightotter", "viewer-a-session", now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	viewerB, err := joinHub(hub, ctx, "calmbrightotter", "viewer-b-session", now.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for name, participant := range map[string]live.ParticipantSnapshot{
		"creator": creator.Participant, "collaborator": collaborator.Participant,
		"viewer-a": viewerA.Participant, "viewer-b": viewerB.Participant,
	} {
		switch name {
		case "creator":
			if participant.AccessClass != live.ParticipantCreator || !participant.CanEdit {
				t.Fatalf("locked creator = %#v", participant)
			}
		case "collaborator":
			if participant.AccessClass != live.ParticipantCollaborator || participant.CanEdit {
				t.Fatalf("locked collaborator = %#v", participant)
			}
		default:
			if participant.AccessClass != live.ParticipantViewer || participant.CanEdit {
				t.Fatalf("locked viewer = %#v", participant)
			}
		}
	}

	creatorEdit, err := creator.Session.SubmitDocument(ctx, live.DocumentOperation{
		OperationID: "locked-creator-edit", ClientID: "creator", DocumentID: "main", BaseVersion: 0,
		Changes: mustChangeSet(t, `[5,[0,"!"]]`),
	}, now.Add(5*time.Second))
	if err != nil || creatorEdit.Revision != 1 {
		t.Fatalf("locked creator edit = %#v, %v", creatorEdit, err)
	}
	for name, session := range map[string]*live.RoomSession{
		"collaborator": collaborator.Session, "viewer": viewerA.Session,
	} {
		if _, err := session.SubmitDocument(ctx, live.DocumentOperation{
			OperationID: "locked-" + name + "-edit", ClientID: name, DocumentID: "main", BaseVersion: 1,
			Changes: mustChangeSet(t, `[6,[0,"?"]]`),
		}, now.Add(6*time.Second)); !errors.Is(err, live.ErrWatchOnly) {
			t.Fatalf("locked %s edit error = %v", name, err)
		}
	}

	if err := collaborator.Session.Disconnect(now.Add(7 * time.Second)); err != nil {
		t.Fatal(err)
	}
	reconnected, err := joinHub(hub, ctx, "calmbrightotter", "collaborator-session", now.Add(8*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !reconnected.Reconnected || reconnected.Participant.AccessClass != live.ParticipantCollaborator || reconnected.Participant.CanEdit {
		t.Fatalf("locked collaborator reconnect = %#v", reconnected.Participant)
	}

	unlocked, err := creator.Session.SetWatchOnly(ctx, false, now.Add(9*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	participants := make(map[string]live.ParticipantSnapshot, len(unlocked.Participants))
	for _, participant := range unlocked.Participants {
		participants[participant.ID] = participant
	}
	if unlocked.WatchOnly || !participants[creator.Participant.ID].CanEdit || !participants[reconnected.Participant.ID].CanEdit || participants[viewerA.Participant.ID].CanEdit || participants[creator.Participant.ID].AccessClass != live.ParticipantCreator || participants[reconnected.Participant.ID].AccessClass != live.ParticipantCollaborator || participants[viewerA.Participant.ID].AccessClass != live.ParticipantViewer {
		t.Fatalf("unlocked truth table = %#v", unlocked.Participants)
	}
	collaboratorEdit, err := reconnected.Session.SubmitDocument(ctx, live.DocumentOperation{
		OperationID: "unlocked-collaborator-edit", ClientID: "collaborator", DocumentID: "main", BaseVersion: 1,
		Changes: mustChangeSet(t, `[6,[0,"?"]]`),
	}, now.Add(10*time.Second))
	if err != nil || collaboratorEdit.Revision != 2 {
		t.Fatalf("unlocked collaborator edit = %#v, %v", collaboratorEdit, err)
	}
}

func TestHubReservesWriterCapacityForLateCreatorJoin(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 10, 18, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	creatorCapability := createTestRoomWithCreator(t, ctx, store, now)
	options := testHubOptions(nil, nil)
	options.MaxWriters, options.MaxViewers, options.MaxParticipants = 2, 1, 3
	hub, err := live.NewHub(store, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Shutdown(ctx, now.Add(time.Hour))

	first, err := joinHub(hub, ctx, "calmbrightotter", "first-session", now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := joinHub(hub, ctx, "calmbrightotter", "second-session", now)
	if err != nil {
		t.Fatal(err)
	}
	if first.Participant.AccessClass != live.ParticipantCollaborator || second.Participant.AccessClass != live.ParticipantViewer {
		t.Fatalf("reserved creator capacity = first %#v, second %#v", first.Participant, second.Participant)
	}
	creator, err := joinHubWithCreator(hub, ctx, "calmbrightotter", "creator-session", creatorCapability, now)
	if err != nil {
		t.Fatal(err)
	}
	if creator.Participant.AccessClass != live.ParticipantCreator || !creator.Participant.CanEdit || len(creator.State.Participants) != 3 {
		t.Fatalf("late creator join = %#v", creator)
	}
	if _, err := joinHub(hub, ctx, "calmbrightotter", "overflow-session", now); !errors.Is(err, live.ErrParticipantLimit) {
		t.Fatalf("late creator capacity overflow error = %v", err)
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
	creatorCapability := createTestRoomWithCreator(t, ctx, store, now)
	options := testHubOptions([]string{"creator", "writer", "viewer", "replacement"}, nil)
	options.MaxWriters = 2
	options.MaxViewers = 1
	options.MaxParticipants = 3
	hub, err := live.NewHub(store, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	creator, err := joinHubWithCreator(hub, ctx, "calmbrightotter", "creator-session", creatorCapability, now)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := joinHub(hub, ctx, "calmbrightotter", "writer-session", now)
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := joinHub(hub, ctx, "calmbrightotter", "viewer-session", now)
	if err != nil {
		t.Fatal(err)
	}
	if writer.Participant.AccessClass != live.ParticipantCollaborator || !writer.Participant.CanEdit || viewer.Participant.AccessClass != live.ParticipantViewer || viewer.Participant.CanEdit {
		t.Fatalf("initial participant authority = writer %#v, viewer %#v", writer.Participant, viewer.Participant)
	}
	if _, err := creator.Session.SetWatchOnly(ctx, true, now); err != nil {
		t.Fatal(err)
	}
	if err := writer.Session.Disconnect(now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := hub.Sweep(ctx, now.Add(time.Second+options.ReconnectGrace+time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	replacement, err := joinHub(hub, ctx, "calmbrightotter", "replacement-session", now.Add(time.Second+options.ReconnectGrace+time.Nanosecond))
	if err != nil {
		t.Fatalf("replacement should use the released writer capacity slot: %v", err)
	}
	if replacement.Participant.AccessClass != live.ParticipantCollaborator || replacement.Participant.CanEdit {
		t.Fatalf("locked replacement participant = %#v", replacement.Participant)
	}
	if _, err := joinHub(hub, ctx, "calmbrightotter", "overflow-session", now); !errors.Is(err, live.ErrParticipantLimit) {
		t.Fatalf("overflow join error = %v", err)
	}
}

func TestHubCreatorCapabilityAndLockSurviveRestartWhilePresenceDoesNot(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	creatorCapability := createTestRoomWithCreator(t, ctx, store, now)
	hub, err := live.NewHub(store, nil, testHubOptions([]string{"creator", "collaborator", "after-restart"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	creator, err := joinHubWithCreator(hub, ctx, "calmbrightotter", "creator-session", creatorCapability, now)
	if err != nil {
		t.Fatal(err)
	}
	collaborator, err := joinHub(hub, ctx, "calmbrightotter", "collaborator-session", now)
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
	if _, err := creator.Session.SetWatchOnly(ctx, true, now); err != nil {
		t.Fatal(err)
	}
	if err := hub.Shutdown(ctx, now); err != nil {
		t.Fatal(err)
	}
	restarted, err := live.NewHub(store, nil, testHubOptions([]string{"after-restart"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	joined, err := joinHubWithCreator(restarted, ctx, "calmbrightotter", "after-restart-session", creatorCapability, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !joined.Session.IsCreator() || !joined.State.WatchOnly || len(joined.State.Participants) != 1 || joined.Participant.AccessClass != live.ParticipantCreator || !joined.Participant.CanEdit {
		t.Fatalf("durable creator/lock or transient presence after restart = %#v", joined)
	}
}

func TestHubRejectsWrongCreatorCapabilityAndExpiredRoom(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	capability := createTestRoomWithCreator(t, ctx, store, now)
	hub, err := live.NewHub(store, nil, testHubOptions([]string{"creator", "after-revoke"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	joined, err := joinHubWithCreator(hub, ctx, "calmbrightotter", "creator-session", capability, now.Add(16*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !joined.Session.IsCreator() {
		t.Fatal("creator capability should outlive the ordinary access session")
	}
	wrong, err := live.NewCreatorCapability()
	if err != nil {
		t.Fatal(err)
	}
	joined, err = joinHubWithCreator(hub, ctx, "calmbrightotter", "wrong-capability-session", wrong, now.Add(17*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if joined.Session.IsCreator() {
		t.Fatal("wrong creator capability acquired authority")
	}
	if _, err := joinHubWithCreator(hub, ctx, "calmbrightotter", "expired-room-session", capability, now.Add(24*time.Hour)); !errors.Is(err, live.ErrRoomExpired) {
		t.Fatalf("expired room join error = %v", err)
	}
}

func TestHubLockPersistenceFailureDoesNotChangeAuthorityState(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	capability := createTestRoomWithCreator(t, ctx, store, now)
	failing := &failLockStore{RoomStore: store}
	hub, err := live.NewHub(failing, nil, testHubOptions([]string{"creator"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	joined, err := joinHubWithCreator(hub, ctx, "calmbrightotter", "creator-session", capability, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := joined.Session.SetWatchOnly(ctx, true, now); !errors.Is(err, live.ErrPersistence) {
		t.Fatalf("lock persistence error = %v", err)
	}
	state, err := joined.Session.State()
	if err != nil {
		t.Fatal(err)
	}
	if state.WatchOnly {
		t.Fatal("failed lock persistence changed in-memory authority")
	}
	persisted, err := store.GetRoomSnapshot(ctx, "calmbrightotter", now)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Locked {
		t.Fatal("failed lock persistence changed SQLite state")
	}
}

func TestHubSerializesConcurrentCreatorConnectionLockTransitions(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 10, 19, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	capability := createTestRoomWithCreator(t, ctx, store, now)
	blocking := &blockingLockStore{
		RoomStore: store, firstEntered: make(chan struct{}), releaseFirst: make(chan struct{}),
	}
	hub, err := live.NewHub(blocking, nil, testHubOptions(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Shutdown(ctx, now.Add(time.Hour))
	identity := func(connection string) live.JoinIdentity {
		return live.JoinIdentity{
			ParticipantCredential: "creator-browser", ConnectionID: connection, ClientID: "client-" + connection,
		}
	}
	first, err := hub.JoinWithIdentity(ctx, "calmbrightotter", identity("first"), capability, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := hub.JoinWithIdentity(ctx, "calmbrightotter", identity("second"), capability, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.Participant.ID != second.Participant.ID || second.Participant.ConnectionCount != 2 || second.Participant.AccessClass != live.ParticipantCreator {
		t.Fatalf("grouped creator connections = %#v", second)
	}

	firstDone := make(chan error, 1)
	go func() {
		_, lockErr := first.Session.SetWatchOnly(ctx, true, now.Add(time.Second))
		firstDone <- lockErr
	}()
	<-blocking.firstEntered
	secondStarted := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		close(secondStarted)
		_, lockErr := second.Session.SetWatchOnly(ctx, false, now.Add(2*time.Second))
		secondDone <- lockErr
	}()
	<-secondStarted
	close(blocking.releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}

	blocking.mu.Lock()
	transitions := append([]bool(nil), blocking.transitions...)
	blocking.mu.Unlock()
	if !reflect.DeepEqual(transitions, []bool{true, false}) {
		t.Fatalf("durable transition order = %#v", transitions)
	}
	state, err := first.Session.State()
	if err != nil {
		t.Fatal(err)
	}
	if state.WatchOnly || len(state.Participants) != 1 || !state.Participants[0].CanEdit || state.Participants[0].AccessClass != live.ParticipantCreator {
		t.Fatalf("final concurrent lock state = %#v", state)
	}
	persisted, err := store.GetRoomSnapshot(ctx, "calmbrightotter", now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Locked {
		t.Fatal("last durable unlock did not win")
	}
}

type failLockStore struct {
	live.RoomStore
}

func (store *failLockStore) SetRoomLocked(context.Context, string, bool, time.Time) error {
	return errors.New("injected lock persistence failure")
}

type blockingLockStore struct {
	live.RoomStore
	firstEntered chan struct{}
	releaseFirst chan struct{}
	mu           sync.Mutex
	transitions  []bool
}

func (store *blockingLockStore) SetRoomLocked(ctx context.Context, slug string, locked bool, now time.Time) error {
	store.mu.Lock()
	first := len(store.transitions) == 0
	store.mu.Unlock()
	if first {
		close(store.firstEntered)
		<-store.releaseFirst
	}
	if err := store.RoomStore.SetRoomLocked(ctx, slug, locked, now); err != nil {
		return err
	}
	store.mu.Lock()
	store.transitions = append(store.transitions, locked)
	store.mu.Unlock()
	return nil
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

func createTestRoomWithCreator(t *testing.T, ctx context.Context, store *sqlite.Store, now time.Time) live.CreatorCapability {
	t.Helper()
	capability, err := live.NewCreatorCapability()
	if err != nil {
		t.Fatal(err)
	}
	room := testRoom(now)
	room.CreatorTokenHash = capability.HashForRoom(room.Slug)
	if err := store.CreateRoom(ctx, room); err != nil {
		t.Fatal(err)
	}
	return capability
}

func joinHub(hub *live.Hub, ctx context.Context, slug, participantCredential string, now time.Time) (live.JoinResult, error) {
	identity := live.JoinIdentity{
		ParticipantCredential: participantCredential,
		ConnectionID:          participantCredential,
		ClientID:              participantCredential,
	}
	return hub.JoinWithIdentity(ctx, slug, identity, live.CreatorCapability{}, now)
}

func joinHubWithCreator(hub *live.Hub, ctx context.Context, slug, participantCredential string, capability live.CreatorCapability, now time.Time) (live.JoinResult, error) {
	identity := live.JoinIdentity{
		ParticipantCredential: participantCredential,
		ConnectionID:          participantCredential,
		ClientID:              participantCredential,
	}
	return hub.JoinWithIdentity(ctx, slug, identity, capability, now)
}

func testHubOptions(participants, documents []string) live.HubOptions {
	options := live.DefaultHubOptions()
	_ = participants // Participant IDs are derived from the room credential.
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
