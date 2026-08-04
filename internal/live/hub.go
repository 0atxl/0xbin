package live

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/0atxl/0xbin/internal/livecollab"
)

const (
	defaultHubMaxTabs            = 8
	defaultHubMaxBytes           = int64(1 << 20)
	defaultHubMaxParticipants    = 32
	defaultHubMaxMessageBytes    = 64 << 10
	defaultHubMaxHistoryRows     = 1000
	defaultHubMaxHistoryBytes    = int64(4 << 20)
	defaultHubReconnectGrace     = 30 * time.Second
	defaultHubHeartbeatInterval  = 20 * time.Second
	defaultHubParticipantTimeout = 60 * time.Second
	maxOperationIDBytes          = 128
	maxClientIDBytes             = 128
	participantIDGenerationTries = 8
	documentIDGenerationTries    = 8
)

var (
	ErrHubClosed           = errors.New("live hub is closed")
	ErrRoomExpired         = errors.New("live room expired")
	ErrRoomLimit           = errors.New("live room limit reached")
	ErrParticipantLimit    = errors.New("live participant limit reached")
	ErrParticipantNotFound = errors.New("live participant not found")
	ErrParticipantInactive = errors.New("live participant is not connected")
	ErrSessionActive       = errors.New("live session is already connected")
	ErrDocumentNotFound    = errors.New("live document not found")
	ErrDocumentDeleted     = errors.New("live document was deleted")
	ErrLastDocument        = errors.New("the last live document cannot be deleted")
	ErrMetadataResync      = errors.New("live metadata resynchronization required")
	ErrDocumentResync      = errors.New("live document resynchronization required")
	ErrNameTaken           = errors.New("live participant name is already in use")
	ErrInvalidPresence     = errors.New("invalid live presence update")
	ErrPersistence         = errors.New("live room persistence failed")
	ErrOperationConflict   = errors.New("live operation conflicts with current room state")
	ErrOperationLimit      = errors.New("live operation exceeds the room limit")
)

type ParticipantStatus string

const (
	ParticipantConnected      ParticipantStatus = "connected"
	ParticipantConnectionLost ParticipantStatus = "connection_lost"
	ParticipantOffline        ParticipantStatus = "offline"
)

// HubOptions bounds all process-local room state. The HTTP/configuration layer
// can map environment settings into this type without exposing config internals
// to the live domain package.
type HubOptions struct {
	MaxTabs            int
	MaxBytes           int64
	MaxParticipants    int
	MaxMessageBytes    int
	MaxHistoryRows     int
	MaxHistoryBytes    int64
	ReconnectGrace     time.Duration
	HeartbeatInterval  time.Duration
	ParticipantTimeout time.Duration
	ParticipantID      func() (string, error)
	DocumentID         func() (string, error)
}

func DefaultHubOptions() HubOptions {
	return HubOptions{
		MaxTabs:            defaultHubMaxTabs,
		MaxBytes:           defaultHubMaxBytes,
		MaxParticipants:    defaultHubMaxParticipants,
		MaxMessageBytes:    defaultHubMaxMessageBytes,
		MaxHistoryRows:     defaultHubMaxHistoryRows,
		MaxHistoryBytes:    defaultHubMaxHistoryBytes,
		ReconnectGrace:     defaultHubReconnectGrace,
		HeartbeatInterval:  defaultHubHeartbeatInterval,
		ParticipantTimeout: defaultHubParticipantTimeout,
	}
}

func (options HubOptions) validate() error {
	if options.MaxTabs < 1 || options.MaxBytes < 1 || options.MaxParticipants < 1 || options.MaxMessageBytes < 1 || options.MaxHistoryRows < 1 || options.MaxHistoryBytes < 1 {
		return errors.New("live hub limits must be positive")
	}
	if int64(options.MaxMessageBytes) > options.MaxBytes {
		return errors.New("live message limit must not exceed room content limit")
	}
	if options.ReconnectGrace <= 0 || options.HeartbeatInterval <= 0 || options.ParticipantTimeout <= options.ReconnectGrace {
		return errors.New("live connection timing limits are incoherent")
	}
	return nil
}

type CursorSelection struct {
	DocumentID string
	Revision   int
	Anchor     int
	Head       int
}

type ParticipantSnapshot struct {
	ID         string
	Nickname   string
	JoinedAt   time.Time
	Color      string
	CurrentTab string
	Cursor     *CursorSelection
	Status     ParticipantStatus
	LastSeenAt time.Time
}

type DocumentState struct {
	ID               string
	Name             string
	Language         string
	Content          string
	Position         int
	Revision         int
	SnapshotRevision int
}

type RoomState struct {
	Slug                     string
	ExpiresAt                time.Time
	MetadataRevision         int
	MetadataSnapshotRevision int
	Documents                []DocumentState
	Participants             []ParticipantSnapshot
}

type JoinResult struct {
	Session     *RoomSession
	Participant ParticipantSnapshot
	State       RoomState
	Reconnected bool
}

type DocumentOperation struct {
	OperationID string
	ClientID    string
	DocumentID  string
	BaseVersion int
	Changes     livecollab.ChangeSet
}

type AcceptedDocumentOperation struct {
	OperationID string
	ClientID    string
	DocumentID  string
	BaseVersion int
	Revision    int
	Changes     livecollab.ChangeSet
	Document    string
	Duplicate   bool
}

type MetadataOperation struct {
	OperationID string
	ClientID    string
	BaseVersion int
	Kind        string
	DocumentID  string
	Name        string
	Language    string
	Content     string
	Order       []string
}

type AcceptedMetadataOperation struct {
	OperationID string
	ClientID    string
	Kind        string
	DocumentID  string
	Revision    int
	State       RoomState
	Duplicate   bool
}

type PresenceUpdate struct {
	CurrentTab string
	DocumentID string
	Revision   int
	Anchor     int
	Head       int
}

type MetadataResyncError struct{ State RoomState }

func (err *MetadataResyncError) Error() string { return ErrMetadataResync.Error() }
func (err *MetadataResyncError) Unwrap() error { return ErrMetadataResync }

type DocumentResyncError struct {
	State      RoomState
	DocumentID string
}

func (err *DocumentResyncError) Error() string { return ErrDocumentResync.Error() }
func (err *DocumentResyncError) Unwrap() error { return ErrDocumentResync }

// Hub owns process-local room state. It intentionally does not persist
// participants, cursors, connection state, or session identities.
type Hub struct {
	store   RoomStore
	names   *NameGenerator
	options HubOptions
	mu      sync.Mutex
	rooms   map[string]*room
	closed  bool
}

// NewHub creates an empty process-local registry. Rooms are loaded lazily when
// the first session joins them.
func NewHub(store RoomStore, names *NameGenerator, options HubOptions) (*Hub, error) {
	if store == nil {
		return nil, errors.New("live room store is required")
	}
	if names == nil {
		names = NewDefaultNameGenerator()
	}
	if err := options.validate(); err != nil {
		return nil, err
	}
	return &Hub{store: store, names: names, options: options, rooms: make(map[string]*room)}, nil
}

// Join loads a room once, assigns or reclaims a temporary identity, and
// returns the complete current state for the joining client.
func (hub *Hub) Join(ctx context.Context, slug, sessionID string, now time.Time) (JoinResult, error) {
	if strings.TrimSpace(slug) != slug || slug == "" || sessionID == "" || len(sessionID) > maxClientIDBytes || strings.TrimSpace(sessionID) != sessionID || !utf8.ValidString(sessionID) {
		return JoinResult{}, ErrParticipantNotFound
	}
	now = normalizedNow(now)
	hub.mu.Lock()
	if hub.closed {
		hub.mu.Unlock()
		return JoinResult{}, ErrHubClosed
	}
	room := hub.rooms[slug]
	if room == nil {
		snapshot, err := hub.store.GetRoomSnapshot(ctx, slug, now)
		if err != nil {
			hub.mu.Unlock()
			if errors.Is(err, ErrRoomNotFound) {
				return JoinResult{}, ErrRoomNotFound
			}
			return JoinResult{}, err
		}
		room, err = newRoom(ctx, hub.store, snapshot, hub.options)
		if err != nil {
			hub.mu.Unlock()
			return JoinResult{}, err
		}
		hub.rooms[slug] = room
	}
	hub.mu.Unlock()

	result, err := room.join(ctx, hub, sessionID, now)
	if errors.Is(err, ErrRoomExpired) {
		hub.removeRoom(slug, room)
	}
	return result, err
}

func (hub *Hub) State(slug string) (RoomState, error) {
	hub.mu.Lock()
	room := hub.rooms[slug]
	hub.mu.Unlock()
	if room == nil {
		return RoomState{}, ErrRoomNotFound
	}
	return room.state()
}

func (hub *Hub) RoomCount() int {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	return len(hub.rooms)
}

// Sweep disconnects silent participants, removes expired rooms, and evicts
// rooms whose reconnect grace period has elapsed.
func (hub *Hub) Sweep(ctx context.Context, now time.Time) (int, error) {
	now = normalizedNow(now)
	hub.mu.Lock()
	if hub.closed {
		hub.mu.Unlock()
		return 0, ErrHubClosed
	}
	rooms := make([]*room, 0, len(hub.rooms))
	for _, room := range hub.rooms {
		rooms = append(rooms, room)
	}
	hub.mu.Unlock()

	removed := 0
	for _, room := range rooms {
		if err := room.sweep(ctx, now); err != nil {
			return removed, err
		}
		if room.shouldEvict(now) {
			hub.removeRoom(room.snapshotSlug(), room)
			removed++
		}
	}
	return removed, nil
}

// Shutdown stops new joins, flushes dirty snapshots, and clears all
// process-local identities. The SQLite store is owned by the application and
// is closed by its outer shutdown sequence.
func (hub *Hub) Shutdown(ctx context.Context, now time.Time) error {
	now = normalizedNow(now)
	hub.mu.Lock()
	if hub.closed {
		hub.mu.Unlock()
		return nil
	}
	hub.closed = true
	rooms := make([]*room, 0, len(hub.rooms))
	for _, room := range hub.rooms {
		rooms = append(rooms, room)
	}
	hub.rooms = make(map[string]*room)
	hub.mu.Unlock()

	var firstErr error
	for _, room := range rooms {
		if err := room.shutdown(ctx, now); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (hub *Hub) removeRoom(slug string, target *room) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.rooms[slug] == target {
		delete(hub.rooms, slug)
	}
}

type RoomSession struct {
	hub         *Hub
	room        *room
	participant string
	sessionID   string
	generation  uint64
}

func (session *RoomSession) State() (RoomState, error) {
	if session == nil || session.room == nil {
		return RoomState{}, ErrParticipantNotFound
	}
	return session.room.stateFor(session.participant, session.generation)
}

func (session *RoomSession) Participant() (ParticipantSnapshot, error) {
	state, err := session.State()
	if err != nil {
		return ParticipantSnapshot{}, err
	}
	for _, participant := range state.Participants {
		if participant.ID == session.participant {
			return participant, nil
		}
	}
	return ParticipantSnapshot{}, ErrParticipantNotFound
}

func (session *RoomSession) SubmitDocument(ctx context.Context, operation DocumentOperation, now time.Time) (AcceptedDocumentOperation, error) {
	if session == nil || session.room == nil {
		return AcceptedDocumentOperation{}, ErrParticipantNotFound
	}
	return session.room.submitDocument(ctx, session.participant, session.generation, operation, normalizedNow(now))
}

func (session *RoomSession) ApplyMetadata(ctx context.Context, operation MetadataOperation, now time.Time) (AcceptedMetadataOperation, error) {
	if session == nil || session.room == nil {
		return AcceptedMetadataOperation{}, ErrParticipantNotFound
	}
	return session.room.applyMetadata(ctx, session.participant, session.generation, operation, normalizedNow(now))
}

func (session *RoomSession) UpdatePresence(update PresenceUpdate, now time.Time) (ParticipantSnapshot, error) {
	if session == nil || session.room == nil {
		return ParticipantSnapshot{}, ErrParticipantNotFound
	}
	return session.room.updatePresence(session.participant, session.generation, update, normalizedNow(now))
}

func (session *RoomSession) Rename(name string, now time.Time) (ParticipantSnapshot, error) {
	if session == nil || session.room == nil {
		return ParticipantSnapshot{}, ErrParticipantNotFound
	}
	return session.room.rename(session.participant, session.generation, name, normalizedNow(now))
}

func (session *RoomSession) Heartbeat(now time.Time) error {
	if session == nil || session.room == nil {
		return ErrParticipantNotFound
	}
	return session.room.heartbeat(session.participant, session.generation, normalizedNow(now))
}

func (session *RoomSession) Disconnect(now time.Time) error {
	if session == nil || session.room == nil {
		return ErrParticipantNotFound
	}
	return session.room.disconnect(session.participant, session.generation, normalizedNow(now))
}

func (session *RoomSession) Leave(now time.Time) error {
	if session == nil || session.room == nil {
		return ErrParticipantNotFound
	}
	err := session.room.leave(session.participant, session.generation, normalizedNow(now))
	if err == nil && session.hub != nil && session.room.shouldEvict(normalizedNow(now)) {
		session.hub.removeRoom(session.room.snapshotSlug(), session.room)
	}
	return err
}

type room struct {
	mu             sync.Mutex
	store          RoomStore
	options        HubOptions
	snapshot       RoomSnapshot
	documents      map[string]*documentState
	order          []string
	participants   map[string]*participantState
	sessions       map[string]string
	names          map[string]string
	operations     map[string]operationRecord
	operationOrder []string
	dirty          bool
	closed         bool
}

type documentState struct {
	snapshot          DocumentSnapshot
	history           []documentHistory
	historyBytes      int64
	compactedRevision int
}

type documentHistory struct {
	Revision    int
	BaseVersion int
	BeforeLen   int
	Bytes       int64
	Changes     livecollab.ChangeSet
}

type participantState struct {
	snapshot       ParticipantSnapshot
	sessionID      string
	generation     uint64
	disconnectedAt time.Time
}

type operationRecord struct {
	fingerprint string
	document    *AcceptedDocumentOperation
	metadata    *AcceptedMetadataOperation
}

type roomBackup struct {
	snapshot  RoomSnapshot
	documents map[string]*documentState
	order     []string
}

func newRoom(ctx context.Context, store RoomStore, snapshot RoomSnapshot, options HubOptions) (*room, error) {
	if snapshot.Slug == "" || len(snapshot.Documents) == 0 || snapshot.ExpiresAt.IsZero() || !snapshot.ExpiresAt.After(snapshot.CreatedAt) {
		return nil, ErrInvalidSnapshot
	}
	room := &room{
		store:        store,
		options:      options,
		snapshot:     snapshot,
		documents:    make(map[string]*documentState, len(snapshot.Documents)),
		participants: make(map[string]*participantState),
		sessions:     make(map[string]string),
		names:        make(map[string]string),
		operations:   make(map[string]operationRecord),
	}
	for _, document := range snapshot.Documents {
		if _, exists := room.documents[document.ID]; exists || ValidateDocumentID(document.ID) != nil || ValidateTabName(document.Name) != nil || ValidateLanguageID(document.Language) != nil || ValidateDocumentContent(document.Content, options.MaxBytes) != nil {
			return nil, ErrInvalidSnapshot
		}
		copyDocument := document
		room.documents[document.ID] = &documentState{snapshot: copyDocument, compactedRevision: document.SnapshotRevision}
		room.order = append(room.order, document.ID)
	}
	if snapshot.ContentSize > options.MaxBytes {
		return nil, ErrRoomLimit
	}
	repairSnapshot := snapshot.MetadataRevision > snapshot.MetadataSnapshotRevision
	for _, document := range snapshot.Documents {
		if document.CurrentRevision > document.SnapshotRevision {
			repairSnapshot = true
			break
		}
	}
	if err := room.replay(ctx); err != nil {
		return nil, err
	}
	if repairSnapshot {
		durable := room.durableSnapshotLocked()
		if err := store.SaveSnapshot(ctx, durable, beforeExpiry(snapshot.ExpiresAt)); err != nil {
			return nil, fmt.Errorf("repair live room snapshot: %w", err)
		}
		room.snapshot = durable
		room.markDocumentsDurableLocked()
	}
	return room, nil
}

func (room *room) replay(ctx context.Context) error {
	if room.snapshot.MetadataRevision > room.snapshot.MetadataSnapshotRevision {
		changes, err := room.store.LoadChangesSince(ctx, room.snapshot.Slug, StreamMetadata, MetadataStreamID, room.snapshot.MetadataSnapshotRevision, beforeExpiry(room.snapshot.ExpiresAt))
		if err != nil {
			return fmt.Errorf("replay live metadata: %w", err)
		}
		for _, change := range changes {
			if err := room.applyPersistedMetadata(change); err != nil {
				return err
			}
		}
	}
	for _, documentID := range append([]string(nil), room.order...) {
		document := room.documents[documentID]
		if document.snapshot.CurrentRevision <= document.snapshot.SnapshotRevision {
			continue
		}
		targetRevision := document.snapshot.CurrentRevision
		document.snapshot.CurrentRevision = document.snapshot.SnapshotRevision
		changes, err := room.store.LoadChangesSince(ctx, room.snapshot.Slug, StreamDocument, documentID, document.snapshot.SnapshotRevision, beforeExpiry(room.snapshot.ExpiresAt))
		if err != nil {
			return fmt.Errorf("replay live document %q: %w", documentID, err)
		}
		for _, change := range changes {
			if err := room.applyPersistedDocument(document, change); err != nil {
				return err
			}
		}
		if document.snapshot.CurrentRevision != targetRevision {
			return fmt.Errorf("%w: live document %q replay stopped at revision %d, want %d", ErrPersistence, documentID, document.snapshot.CurrentRevision, targetRevision)
		}
	}
	room.syncSnapshotLocked()
	return nil
}

func (room *room) join(ctx context.Context, hub *Hub, sessionID string, now time.Time) (JoinResult, error) {
	room.mu.Lock()
	defer room.mu.Unlock()
	if err := room.ensureActiveLocked(now); err != nil {
		return JoinResult{}, err
	}
	room.pruneDisconnectedLocked(now)
	if existingID, exists := room.sessions[sessionID]; exists {
		participant := room.participants[existingID]
		if participant != nil && participant.snapshot.Status == ParticipantConnected {
			return JoinResult{}, ErrSessionActive
		}
		if participant != nil && now.Sub(participant.disconnectedAt) <= room.options.ReconnectGrace {
			participant.snapshot.Status = ParticipantConnected
			participant.snapshot.LastSeenAt = now
			participant.disconnectedAt = time.Time{}
			participant.generation++
			return room.joinResultLocked(hub, participant, true), nil
		}
		delete(room.participants, existingID)
		delete(room.sessions, sessionID)
		if participant != nil {
			delete(room.names, NameKey(participant.snapshot.Nickname))
		}
	}
	if len(room.participants) >= room.options.MaxParticipants {
		return JoinResult{}, ErrParticipantLimit
	}
	participantID, err := uniqueGeneratedID(room.options.ParticipantID, func(id string) bool {
		_, exists := room.participants[id]
		return exists
	}, participantIDGenerationTries, false)
	if err != nil {
		return JoinResult{}, fmt.Errorf("generate live participant ID: %w", err)
	}
	usedNames := make(map[string]struct{}, len(room.names))
	for name := range room.names {
		usedNames[name] = struct{}{}
	}
	nickname, err := hub.names.Generate(usedNames)
	if err != nil {
		return JoinResult{}, err
	}
	participant := &participantState{
		sessionID:  sessionID,
		generation: 1,
		snapshot: ParticipantSnapshot{
			ID: participantID, Nickname: nickname, JoinedAt: now,
			Color: participantColor(participantID), CurrentTab: room.order[0],
			Status: ParticipantConnected, LastSeenAt: now,
		},
	}
	room.participants[participantID] = participant
	room.sessions[sessionID] = participantID
	room.names[NameKey(nickname)] = participantID
	return room.joinResultLocked(hub, participant, false), nil
}

func (room *room) joinResultLocked(hub *Hub, participant *participantState, reconnected bool) JoinResult {
	return JoinResult{
		Session:     &RoomSession{hub: hub, room: room, participant: participant.snapshot.ID, sessionID: participant.sessionID, generation: participant.generation},
		Participant: cloneParticipant(participant.snapshot),
		State:       room.stateLocked(), Reconnected: reconnected,
	}
}

func (room *room) submitDocument(ctx context.Context, participantID string, generation uint64, operation DocumentOperation, now time.Time) (AcceptedDocumentOperation, error) {
	room.mu.Lock()
	defer room.mu.Unlock()
	if err := room.ensureParticipantLocked(participantID, generation, now); err != nil {
		return AcceptedDocumentOperation{}, err
	}
	if err := room.ensureOperationIDs(operation.OperationID, operation.ClientID); err != nil {
		return AcceptedDocumentOperation{}, err
	}
	fingerprint := operationFingerprint(operation)
	if record, exists := room.operations[operation.OperationID]; exists {
		if record.fingerprint != fingerprint || record.document == nil {
			return AcceptedDocumentOperation{}, livecollab.ErrDuplicateOperation
		}
		duplicate := cloneAcceptedDocument(*record.document)
		duplicate.Duplicate = true
		return duplicate, nil
	}
	if err := ValidateDocumentID(operation.DocumentID); err != nil {
		return AcceptedDocumentOperation{}, ErrDocumentNotFound
	}
	document := room.documents[operation.DocumentID]
	if document == nil {
		return AcceptedDocumentOperation{}, ErrDocumentNotFound
	}
	if operation.BaseVersion < document.compactedRevision {
		return AcceptedDocumentOperation{}, &DocumentResyncError{State: room.stateLocked(), DocumentID: operation.DocumentID}
	}
	if operation.BaseVersion > document.snapshot.CurrentRevision {
		return AcceptedDocumentOperation{}, livecollab.ErrRevisionConflict
	}
	baseLength, ok := document.lengthAt(operation.BaseVersion)
	if !ok {
		return AcceptedDocumentOperation{}, &DocumentResyncError{State: room.stateLocked(), DocumentID: operation.DocumentID}
	}
	if err := operation.Changes.ValidateInsertedBytes(room.options.MaxMessageBytes); err != nil {
		return AcceptedDocumentOperation{}, ErrOperationLimit
	}
	encoded, err := encodeChangeSet(operation.Changes)
	if err != nil || len(encoded) > room.options.MaxMessageBytes {
		return AcceptedDocumentOperation{}, ErrOperationLimit
	}
	if operation.Changes.OldLen() != baseLength {
		return AcceptedDocumentOperation{}, livecollab.ErrRevisionConflict
	}
	if err := operation.Changes.ValidateForDocument(strings.Repeat("x", baseLength)); err != nil {
		return AcceptedDocumentOperation{}, livecollab.ErrInvalidChangeSet
	}
	changes := operation.Changes
	for _, accepted := range document.history {
		if accepted.Revision > operation.BaseVersion {
			changes, err = changes.Map(accepted.Changes, false)
			if err != nil {
				return AcceptedDocumentOperation{}, livecollab.ErrRevisionConflict
			}
		}
	}
	newContent, err := changes.Apply(document.snapshot.Content)
	if err != nil {
		return AcceptedDocumentOperation{}, livecollab.ErrInvalidChangeSet
	}
	if room.contentSizeWith(operation.DocumentID, newContent) > room.options.MaxBytes {
		return AcceptedDocumentOperation{}, ErrRoomLimit
	}
	backup := room.backupLocked()
	accepted := AcceptedDocumentOperation{
		OperationID: operation.OperationID, ClientID: operation.ClientID,
		DocumentID: operation.DocumentID, BaseVersion: operation.BaseVersion,
		Revision: document.snapshot.CurrentRevision + 1, Changes: changes,
		Document: newContent,
	}
	document.snapshot.Content = newContent
	document.snapshot.CurrentRevision = accepted.Revision
	document.snapshot.UpdatedAt = now
	room.syncSnapshotLocked()
	payload, err := encodeDocumentChange(changes)
	if err != nil {
		room.restoreLocked(backup)
		return AcceptedDocumentOperation{}, err
	}
	if len(payload) > room.options.MaxMessageBytes {
		room.restoreLocked(backup)
		return AcceptedDocumentOperation{}, ErrOperationLimit
	}
	change := ChangeRecord{StreamKind: StreamDocument, StreamID: operation.DocumentID, Revision: accepted.Revision, Kind: "push_changes", Payload: payload, CreatedAt: now}
	beforeLength := livecollab.UTF16Len(backup.documents[operation.DocumentID].snapshot.Content)
	if err := room.appendAndSave(ctx, change, fingerprint, &accepted, nil, backup, beforeLength, now); err != nil {
		return AcceptedDocumentOperation{}, err
	}
	return accepted, nil
}

func (room *room) applyMetadata(ctx context.Context, participantID string, generation uint64, operation MetadataOperation, now time.Time) (AcceptedMetadataOperation, error) {
	room.mu.Lock()
	defer room.mu.Unlock()
	if err := room.ensureParticipantLocked(participantID, generation, now); err != nil {
		return AcceptedMetadataOperation{}, err
	}
	if err := room.ensureOperationIDs(operation.OperationID, operation.ClientID); err != nil {
		return AcceptedMetadataOperation{}, err
	}
	fingerprint := operationFingerprint(operation)
	if record, exists := room.operations[operation.OperationID]; exists {
		if record.fingerprint != fingerprint || record.metadata == nil {
			return AcceptedMetadataOperation{}, livecollab.ErrDuplicateOperation
		}
		duplicate := cloneAcceptedMetadata(*record.metadata)
		duplicate.Duplicate = true
		return duplicate, nil
	}
	if operation.BaseVersion < 0 || operation.BaseVersion > room.snapshot.MetadataRevision {
		return AcceptedMetadataOperation{}, livecollab.ErrRevisionConflict
	}
	if operation.Kind == "document_reorder" && operation.BaseVersion != room.snapshot.MetadataRevision {
		return AcceptedMetadataOperation{}, &MetadataResyncError{State: room.stateLocked()}
	}
	if err := ValidateRoomOperation(operation.Kind); err != nil || operation.Kind == "push_changes" || operation.Kind == "join" || operation.Kind == "presence" || operation.Kind == "participant_rename" || operation.Kind == "ack" {
		return AcceptedMetadataOperation{}, ErrOperationConflict
	}
	backup := room.backupLocked()
	accepted := AcceptedMetadataOperation{OperationID: operation.OperationID, ClientID: operation.ClientID, Kind: operation.Kind, Revision: room.snapshot.MetadataRevision + 1}
	persisted := persistedMetadataChange{Kind: operation.Kind}
	switch operation.Kind {
	case "document_create":
		if len(room.order) >= room.options.MaxTabs {
			return AcceptedMetadataOperation{}, ErrRoomLimit
		}
		if err := ValidateTabName(operation.Name); err != nil {
			return AcceptedMetadataOperation{}, err
		}
		if err := ValidateLanguageID(operation.Language); err != nil {
			return AcceptedMetadataOperation{}, err
		}
		if err := ValidateDocumentContent(operation.Content, int64(room.options.MaxMessageBytes)); err != nil {
			return AcceptedMetadataOperation{}, ErrOperationLimit
		}
		if room.contentSizeWith("", operation.Content) > room.options.MaxBytes {
			return AcceptedMetadataOperation{}, ErrRoomLimit
		}
		documentID, err := room.newDocumentID()
		if err != nil {
			return AcceptedMetadataOperation{}, err
		}
		document := DocumentSnapshot{ID: documentID, Name: operation.Name, Language: operation.Language, Content: operation.Content, Position: len(room.order), UpdatedAt: now}
		room.documents[documentID] = &documentState{snapshot: document}
		room.order = append(room.order, documentID)
		accepted.DocumentID = documentID
		persisted.DocumentID, persisted.Name, persisted.Language, persisted.Content = documentID, operation.Name, operation.Language, operation.Content
	case "document_update":
		document := room.documents[operation.DocumentID]
		if document == nil {
			return AcceptedMetadataOperation{}, ErrDocumentDeleted
		}
		if err := ValidateTabName(operation.Name); err != nil {
			return AcceptedMetadataOperation{}, err
		}
		if err := ValidateLanguageID(operation.Language); err != nil {
			return AcceptedMetadataOperation{}, err
		}
		document.snapshot.Name, document.snapshot.Language, document.snapshot.UpdatedAt = operation.Name, operation.Language, now
		accepted.DocumentID = operation.DocumentID
		persisted.DocumentID, persisted.Name, persisted.Language = operation.DocumentID, operation.Name, operation.Language
	case "document_delete":
		if len(room.order) == 1 {
			return AcceptedMetadataOperation{}, ErrLastDocument
		}
		if room.documents[operation.DocumentID] == nil {
			return AcceptedMetadataOperation{}, ErrDocumentDeleted
		}
		delete(room.documents, operation.DocumentID)
		for index, id := range room.order {
			if id == operation.DocumentID {
				room.order = append(room.order[:index], room.order[index+1:]...)
				break
			}
		}
		accepted.DocumentID = operation.DocumentID
		persisted.DocumentID = operation.DocumentID
	case "document_reorder":
		if err := ValidateDocumentOrder(operation.Order, room.options.MaxTabs); err != nil || len(operation.Order) != len(room.order) {
			return AcceptedMetadataOperation{}, &MetadataResyncError{State: room.stateLocked()}
		}
		for index, id := range operation.Order {
			if room.documents[id] == nil || operation.Order[index] == "" {
				return AcceptedMetadataOperation{}, &MetadataResyncError{State: room.stateLocked()}
			}
		}
		room.order = append([]string(nil), operation.Order...)
		persisted.Order = append([]string(nil), operation.Order...)
	default:
		room.restoreLocked(backup)
		return AcceptedMetadataOperation{}, ErrOperationConflict
	}
	room.snapshot.MetadataRevision = accepted.Revision
	room.syncSnapshotLocked()
	payload, err := json.Marshal(persisted)
	if err != nil {
		room.restoreLocked(backup)
		return AcceptedMetadataOperation{}, err
	}
	if len(payload) > room.options.MaxMessageBytes {
		room.restoreLocked(backup)
		return AcceptedMetadataOperation{}, ErrOperationLimit
	}
	accepted.State = room.stateLocked()
	change := ChangeRecord{StreamKind: StreamMetadata, StreamID: MetadataStreamID, Revision: accepted.Revision, Kind: operation.Kind, Payload: string(payload), CreatedAt: now}
	if err := room.appendAndSave(ctx, change, fingerprint, nil, &accepted, backup, 0, now); err != nil {
		return AcceptedMetadataOperation{}, err
	}
	accepted.State = room.stateLocked()
	return accepted, nil
}

func (room *room) updatePresence(participantID string, generation uint64, update PresenceUpdate, now time.Time) (ParticipantSnapshot, error) {
	room.mu.Lock()
	defer room.mu.Unlock()
	if err := room.ensureParticipantLocked(participantID, generation, now); err != nil {
		return ParticipantSnapshot{}, err
	}
	if update.CurrentTab != "" {
		if room.documents[update.CurrentTab] == nil {
			return ParticipantSnapshot{}, ErrDocumentNotFound
		}
		room.participants[participantID].snapshot.CurrentTab = update.CurrentTab
	}
	if update.DocumentID != "" {
		document := room.documents[update.DocumentID]
		if document == nil {
			return ParticipantSnapshot{}, ErrDocumentNotFound
		}
		if update.Revision < document.compactedRevision {
			return ParticipantSnapshot{}, ErrInvalidPresence
		}
		selection := livecollab.SelectionRange{Anchor: update.Anchor, Head: update.Head}
		if update.Revision > document.snapshot.CurrentRevision {
			return ParticipantSnapshot{}, ErrInvalidPresence
		}
		if update.Revision != document.snapshot.CurrentRevision {
			for _, accepted := range document.history {
				if accepted.Revision > update.Revision {
					var err error
					selection, err = mapSelection(selection, accepted.Changes)
					if err != nil {
						return ParticipantSnapshot{}, ErrInvalidPresence
					}
				}
			}
		}
		if selection.Anchor < 0 || selection.Head < 0 || selection.Anchor > livecollab.UTF16Len(document.snapshot.Content) || selection.Head > livecollab.UTF16Len(document.snapshot.Content) {
			return ParticipantSnapshot{}, ErrInvalidPresence
		}
		cursor := CursorSelection{DocumentID: update.DocumentID, Revision: document.snapshot.CurrentRevision, Anchor: selection.Anchor, Head: selection.Head}
		room.participants[participantID].snapshot.Cursor = &cursor
	}
	room.participants[participantID].snapshot.LastSeenAt = now
	return cloneParticipant(room.participants[participantID].snapshot), nil
}

func (room *room) rename(participantID string, generation uint64, name string, now time.Time) (ParticipantSnapshot, error) {
	room.mu.Lock()
	defer room.mu.Unlock()
	if err := room.ensureParticipantLocked(participantID, generation, now); err != nil {
		return ParticipantSnapshot{}, err
	}
	if err := ValidateNickname(name); err != nil {
		return ParticipantSnapshot{}, err
	}
	key := NameKey(name)
	if owner, exists := room.names[key]; exists && owner != participantID {
		return ParticipantSnapshot{}, ErrNameTaken
	}
	participant := room.participants[participantID]
	delete(room.names, NameKey(participant.snapshot.Nickname))
	room.names[key] = participantID
	participant.snapshot.Nickname = name
	participant.snapshot.LastSeenAt = now
	return cloneParticipant(participant.snapshot), nil
}

func (room *room) heartbeat(participantID string, generation uint64, now time.Time) error {
	room.mu.Lock()
	defer room.mu.Unlock()
	if err := room.ensureParticipantLocked(participantID, generation, now); err != nil {
		return err
	}
	room.participants[participantID].snapshot.LastSeenAt = now
	return nil
}

func (room *room) disconnect(participantID string, generation uint64, now time.Time) error {
	room.mu.Lock()
	defer room.mu.Unlock()
	participant := room.participants[participantID]
	if participant == nil {
		return ErrParticipantNotFound
	}
	if participant.generation != generation {
		return ErrParticipantInactive
	}
	if participant.snapshot.Status != ParticipantConnected {
		return nil
	}
	participant.snapshot.Status = ParticipantConnectionLost
	participant.snapshot.LastSeenAt = now
	participant.disconnectedAt = now
	return nil
}

func (room *room) leave(participantID string, generation uint64, now time.Time) error {
	room.mu.Lock()
	defer room.mu.Unlock()
	if err := room.ensureActiveLocked(now); err != nil {
		return err
	}
	participant := room.participants[participantID]
	if participant == nil {
		return ErrParticipantNotFound
	}
	if participant.generation != generation {
		return ErrParticipantInactive
	}
	delete(room.participants, participantID)
	delete(room.sessions, participant.sessionID)
	delete(room.names, NameKey(participant.snapshot.Nickname))
	return nil
}

func (room *room) state() (RoomState, error) {
	room.mu.Lock()
	defer room.mu.Unlock()
	if room.closed {
		return RoomState{}, ErrRoomExpired
	}
	return room.stateLocked(), nil
}

func (room *room) stateFor(participantID string, generation uint64) (RoomState, error) {
	room.mu.Lock()
	defer room.mu.Unlock()
	if participantID == "" || room.participants[participantID] == nil {
		return RoomState{}, ErrParticipantNotFound
	}
	if room.participants[participantID].generation != generation || room.participants[participantID].snapshot.Status != ParticipantConnected {
		return RoomState{}, ErrParticipantInactive
	}
	return room.stateLocked(), nil
}

func (room *room) sweep(ctx context.Context, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	room.mu.Lock()
	defer room.mu.Unlock()
	if room.closed {
		return nil
	}
	if now.Unix() >= room.snapshot.ExpiresAt.Unix() {
		for _, participant := range room.participants {
			participant.snapshot.Status = ParticipantOffline
		}
		room.closed = true
		return nil
	}
	for _, participant := range room.participants {
		if participant.snapshot.Status == ParticipantConnected && now.Sub(participant.snapshot.LastSeenAt) > room.options.ParticipantTimeout {
			participant.snapshot.Status = ParticipantConnectionLost
			participant.disconnectedAt = now
		}
	}
	room.pruneDisconnectedLocked(now)
	return nil
}

func (room *room) shutdown(ctx context.Context, now time.Time) error {
	room.mu.Lock()
	defer room.mu.Unlock()
	if room.dirty && now.Unix() < room.snapshot.ExpiresAt.Unix() {
		durable := room.durableSnapshotLocked()
		if err := room.store.SaveSnapshot(ctx, durable, now); err != nil {
			return fmt.Errorf("flush live room %q: %w", room.snapshot.Slug, err)
		}
		room.snapshot = durable
		room.markDocumentsDurableLocked()
		room.dirty = false
	}
	for _, participant := range room.participants {
		participant.snapshot.Status = ParticipantOffline
	}
	room.closed = true
	return nil
}

func (room *room) ensureActiveLocked(now time.Time) error {
	if room.closed || now.Unix() >= room.snapshot.ExpiresAt.Unix() {
		room.closed = true
		return ErrRoomExpired
	}
	return nil
}

func (room *room) ensureParticipantLocked(participantID string, generation uint64, now time.Time) error {
	if err := room.ensureActiveLocked(now); err != nil {
		return err
	}
	participant := room.participants[participantID]
	if participant == nil {
		return ErrParticipantNotFound
	}
	if participant.generation != generation || participant.snapshot.Status != ParticipantConnected {
		return ErrParticipantInactive
	}
	participant.snapshot.LastSeenAt = now
	return nil
}

func (room *room) pruneDisconnectedLocked(now time.Time) {
	for participantID, participant := range room.participants {
		if participant.snapshot.Status == ParticipantConnected || participant.disconnectedAt.IsZero() || now.Sub(participant.disconnectedAt) <= room.options.ReconnectGrace {
			continue
		}
		delete(room.participants, participantID)
		delete(room.sessions, participant.sessionID)
		delete(room.names, NameKey(participant.snapshot.Nickname))
	}
}

func (room *room) shouldEvict(now time.Time) bool {
	room.mu.Lock()
	defer room.mu.Unlock()
	if room.closed {
		return true
	}
	room.pruneDisconnectedLocked(now)
	return len(room.participants) == 0
}

func (room *room) snapshotSlug() string {
	room.mu.Lock()
	defer room.mu.Unlock()
	return room.snapshot.Slug
}

func (room *room) stateLocked() RoomState {
	state := RoomState{
		Slug: room.snapshot.Slug, ExpiresAt: room.snapshot.ExpiresAt,
		MetadataRevision:         room.snapshot.MetadataRevision,
		MetadataSnapshotRevision: room.snapshot.MetadataSnapshotRevision,
		Documents:                make([]DocumentState, 0, len(room.order)),
		Participants:             make([]ParticipantSnapshot, 0, len(room.participants)),
	}
	for position, documentID := range room.order {
		document := room.documents[documentID]
		if document == nil {
			continue
		}
		state.Documents = append(state.Documents, DocumentState{
			ID: document.snapshot.ID, Name: document.snapshot.Name,
			Language: document.snapshot.Language, Content: document.snapshot.Content,
			Position: position, Revision: document.snapshot.CurrentRevision,
			SnapshotRevision: document.snapshot.SnapshotRevision,
		})
	}
	for _, participant := range room.participants {
		state.Participants = append(state.Participants, cloneParticipant(participant.snapshot))
	}
	sortParticipants(state.Participants)
	return state
}

func (room *room) syncSnapshotLocked() {
	documents := make([]DocumentSnapshot, 0, len(room.order))
	var contentSize int64
	for position, documentID := range room.order {
		document := room.documents[documentID]
		if document == nil {
			continue
		}
		document.snapshot.Position = position
		documents = append(documents, document.snapshot)
		contentSize += int64(len(document.snapshot.Content))
	}
	room.snapshot.Documents = documents
	room.snapshot.ContentSize = contentSize
}

func (room *room) durableSnapshotLocked() RoomSnapshot {
	room.syncSnapshotLocked()
	durable := room.snapshot
	durable.Documents = append([]DocumentSnapshot(nil), room.snapshot.Documents...)
	durable.MetadataSnapshotRevision = durable.MetadataRevision
	for index := range durable.Documents {
		durable.Documents[index].SnapshotRevision = durable.Documents[index].CurrentRevision
	}
	return durable
}

func (room *room) markDocumentsDurableLocked() {
	for _, document := range room.documents {
		document.snapshot.SnapshotRevision = document.snapshot.CurrentRevision
	}
}

func (room *room) contentSizeWith(documentID, content string) int64 {
	if documentID == "" {
		var size int64
		for _, document := range room.documents {
			size += int64(len(document.snapshot.Content))
		}
		return size + int64(len(content))
	}
	var size int64
	for id, document := range room.documents {
		if id == documentID {
			size += int64(len(content))
		} else {
			size += int64(len(document.snapshot.Content))
		}
	}
	return size
}

func (document *documentState) lengthAt(version int) (int, bool) {
	if version == document.snapshot.CurrentRevision {
		return livecollab.UTF16Len(document.snapshot.Content), true
	}
	for _, accepted := range document.history {
		if accepted.BaseVersion == version {
			return accepted.BeforeLen, true
		}
	}
	return 0, false
}

func (room *room) newDocumentID() (string, error) {
	return uniqueGeneratedID(room.options.DocumentID, func(id string) bool {
		_, exists := room.documents[id]
		return exists
	}, documentIDGenerationTries, true)
}

func (room *room) appendAndSave(ctx context.Context, change ChangeRecord, fingerprint string, documentResult *AcceptedDocumentOperation, metadataResult *AcceptedMetadataOperation, backup roomBackup, beforeLength int, now time.Time) error {
	if err := room.store.AppendChanges(ctx, room.snapshot.Slug, []ChangeRecord{change}, now); err != nil {
		room.restoreLocked(backup)
		return fmt.Errorf("%w: append: %w", ErrPersistence, err)
	}
	if documentResult != nil {
		room.recordDocumentOperation(fingerprint, *documentResult, beforeLength, int64(len(change.Payload)))
	} else if metadataResult != nil {
		room.recordMetadataOperation(fingerprint, *metadataResult)
	}
	room.dirty = true
	durable := room.durableSnapshotLocked()
	if err := room.store.SaveSnapshot(ctx, durable, now); err != nil {
		return fmt.Errorf("%w: snapshot: %w", ErrPersistence, err)
	}
	if documentResult != nil {
		if err := room.store.CompactChanges(ctx, room.snapshot.Slug, StreamDocument, documentResult.DocumentID, documentResult.Revision, now); err != nil {
			return fmt.Errorf("%w: compact document history: %w", ErrPersistence, err)
		}
	} else if metadataResult != nil {
		if err := room.store.CompactChanges(ctx, room.snapshot.Slug, StreamMetadata, MetadataStreamID, metadataResult.Revision, now); err != nil {
			return fmt.Errorf("%w: compact metadata history: %w", ErrPersistence, err)
		}
	}
	room.snapshot = durable
	room.markDocumentsDurableLocked()
	room.dirty = false
	return nil
}

func (room *room) recordDocumentOperation(fingerprint string, accepted AcceptedDocumentOperation, beforeLength int, payloadBytes int64) {
	room.operations[accepted.OperationID] = operationRecord{fingerprint: fingerprint, document: cloneAcceptedDocumentPtr(accepted)}
	room.operationOrder = append(room.operationOrder, accepted.OperationID)
	document := room.documents[accepted.DocumentID]
	document.addHistory(documentHistory{
		Revision: accepted.Revision, BaseVersion: accepted.Revision - 1,
		BeforeLen: beforeLength, Bytes: payloadBytes, Changes: accepted.Changes,
	})
	document.pruneHistory(room.options.MaxHistoryRows, room.options.MaxHistoryBytes)
	room.pruneOperationRecords()
}

func (room *room) recordMetadataOperation(fingerprint string, accepted AcceptedMetadataOperation) {
	room.operations[accepted.OperationID] = operationRecord{fingerprint: fingerprint, metadata: cloneAcceptedMetadataPtr(accepted)}
	room.operationOrder = append(room.operationOrder, accepted.OperationID)
	room.pruneOperationRecords()
}

func (document *documentState) addHistory(accepted documentHistory) {
	document.history = append(document.history, accepted)
	document.historyBytes += accepted.Bytes
}

func (room *room) pruneOperationRecords() {
	maxRecords := room.options.MaxHistoryRows * 2
	if maxRecords < 32 {
		maxRecords = 32
	}
	for len(room.operationOrder) > maxRecords {
		id := room.operationOrder[0]
		room.operationOrder = room.operationOrder[1:]
		delete(room.operations, id)
	}
}

func (document *documentState) pruneHistory(maxRows int, maxBytes int64) {
	for len(document.history) > maxRows || document.historyBytes > maxBytes {
		if len(document.history) == 0 {
			break
		}
		removed := document.history[0]
		document.history = document.history[1:]
		document.historyBytes -= removed.Bytes
	}
	if len(document.history) > 0 {
		document.compactedRevision = document.history[0].BaseVersion
	} else {
		document.compactedRevision = document.snapshot.CurrentRevision
	}
}

func (room *room) backupLocked() roomBackup {
	backup := roomBackup{snapshot: room.snapshot, order: append([]string(nil), room.order...), documents: make(map[string]*documentState, len(room.documents))}
	backup.snapshot.Documents = append([]DocumentSnapshot(nil), room.snapshot.Documents...)
	for id, document := range room.documents {
		copyDocument := *document
		copyDocument.history = append([]documentHistory(nil), document.history...)
		backup.documents[id] = &copyDocument
	}
	return backup
}

func (room *room) restoreLocked(backup roomBackup) {
	room.snapshot = backup.snapshot
	room.order = backup.order
	room.documents = backup.documents
}

func (room *room) ensureOperationIDs(operationID, clientID string) error {
	if operationID == "" || len(operationID) > maxOperationIDBytes || !utf8.ValidString(operationID) || clientID == "" || len(clientID) > maxClientIDBytes || !utf8.ValidString(clientID) {
		return ErrOperationConflict
	}
	return nil
}

func uniqueGeneratedID(generator func() (string, error), exists func(string) bool, attempts int, validateDocument bool) (string, error) {
	if generator == nil {
		generator = defaultOpaqueID
	}
	for range attempts {
		id, err := generator()
		if err != nil {
			return "", err
		}
		if (!validateDocument || ValidateDocumentID(id) == nil) && !exists(id) {
			return id, nil
		}
	}
	return "", ErrOperationLimit
}

func defaultOpaqueID() (string, error) {
	var bytes [12]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

func participantColor(id string) string {
	palette := []string{"#2563eb", "#0891b2", "#059669", "#65a30d", "#ca8a04", "#ea580c", "#dc2626", "#c026d3"}
	digest := sha256.Sum256([]byte(id))
	return palette[int(digest[0])%len(palette)]
}

func normalizedNow(now time.Time) time.Time {
	if now.IsZero() {
		return time.Now().UTC()
	}
	return now.UTC()
}

func beforeExpiry(expiresAt time.Time) time.Time {
	return time.Unix(expiresAt.UTC().Unix()-1, 0).UTC()
}

func operationFingerprint(operation any) string {
	data, err := json.Marshal(operation)
	if err != nil {
		return fmt.Sprintf("%T", operation)
	}
	return string(data)
}

type persistedDocumentChange struct {
	Changes json.RawMessage `json:"changes"`
}

type persistedMetadataChange struct {
	Kind       string   `json:"kind"`
	DocumentID string   `json:"document_id,omitempty"`
	Name       string   `json:"name,omitempty"`
	Language   string   `json:"language,omitempty"`
	Content    string   `json:"content,omitempty"`
	Order      []string `json:"order,omitempty"`
}

func encodeDocumentChange(changes livecollab.ChangeSet) (string, error) {
	encoded, err := encodeChangeSet(changes)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(persistedDocumentChange{Changes: json.RawMessage(encoded)})
	return string(data), err
}

func encodeChangeSet(changes livecollab.ChangeSet) ([]byte, error) {
	parts := make([]any, 0, len(changes.Sections))
	for _, section := range changes.Sections {
		if section.NewLen < 0 {
			parts = append(parts, section.OldLen)
			continue
		}
		sectionParts := []any{section.OldLen}
		for _, line := range strings.Split(section.Insert, "\n") {
			sectionParts = append(sectionParts, line)
		}
		parts = append(parts, sectionParts)
	}
	return json.Marshal(parts)
}

func (room *room) applyPersistedMetadata(change ChangeRecord) error {
	var persisted persistedMetadataChange
	if err := json.Unmarshal([]byte(change.Payload), &persisted); err != nil {
		return fmt.Errorf("decode live metadata history: %w", err)
	}
	switch persisted.Kind {
	case "document_create":
		if room.documents[persisted.DocumentID] == nil {
			if len(room.order) >= room.options.MaxTabs {
				return ErrRoomLimit
			}
			document := DocumentSnapshot{ID: persisted.DocumentID, Name: persisted.Name, Language: persisted.Language, Content: persisted.Content, Position: len(room.order), UpdatedAt: change.CreatedAt}
			room.documents[persisted.DocumentID] = &documentState{snapshot: document}
			room.order = append(room.order, persisted.DocumentID)
		}
	case "document_update":
		document := room.documents[persisted.DocumentID]
		if document == nil {
			return ErrDocumentDeleted
		}
		document.snapshot.Name, document.snapshot.Language = persisted.Name, persisted.Language
	case "document_delete":
		if room.documents[persisted.DocumentID] != nil {
			delete(room.documents, persisted.DocumentID)
			for index, id := range room.order {
				if id == persisted.DocumentID {
					room.order = append(room.order[:index], room.order[index+1:]...)
					break
				}
			}
		}
	case "document_reorder":
		if err := ValidateDocumentOrder(persisted.Order, room.options.MaxTabs); err != nil {
			return err
		}
		room.order = append([]string(nil), persisted.Order...)
	default:
		return ErrOperationConflict
	}
	room.snapshot.MetadataRevision = change.Revision
	return nil
}

func (room *room) applyPersistedDocument(document *documentState, change ChangeRecord) error {
	var persisted persistedDocumentChange
	if err := json.Unmarshal([]byte(change.Payload), &persisted); err != nil {
		return fmt.Errorf("decode live document history: %w", err)
	}
	changes, err := livecollab.ParseChangeSetJSON(persisted.Changes)
	if err != nil {
		return err
	}
	if change.Revision != document.snapshot.CurrentRevision+1 {
		return livecollab.ErrRevisionConflict
	}
	content, err := changes.Apply(document.snapshot.Content)
	if err != nil {
		return err
	}
	document.history = append(document.history, documentHistory{Revision: change.Revision, BaseVersion: document.snapshot.CurrentRevision, BeforeLen: livecollab.UTF16Len(document.snapshot.Content), Bytes: int64(len(change.Payload)), Changes: changes})
	document.historyBytes += int64(len(change.Payload))
	document.snapshot.Content = content
	document.snapshot.CurrentRevision = change.Revision
	document.snapshot.UpdatedAt = change.CreatedAt
	document.pruneHistory(room.options.MaxHistoryRows, room.options.MaxHistoryBytes)
	return nil
}

func mapSelection(selection livecollab.SelectionRange, changes livecollab.ChangeSet) (livecollab.SelectionRange, error) {
	return selection.Map(changes)
}

func (room *room) stateFromSnapshot() RoomState { return room.stateLocked() }

func cloneParticipant(participant ParticipantSnapshot) ParticipantSnapshot {
	copyParticipant := participant
	if participant.Cursor != nil {
		cursor := *participant.Cursor
		copyParticipant.Cursor = &cursor
	}
	return copyParticipant
}

func cloneAcceptedDocument(accepted AcceptedDocumentOperation) AcceptedDocumentOperation {
	accepted.Changes.Sections = append([]livecollab.Section(nil), accepted.Changes.Sections...)
	return accepted
}

func cloneAcceptedDocumentPtr(accepted AcceptedDocumentOperation) *AcceptedDocumentOperation {
	copyAccepted := cloneAcceptedDocument(accepted)
	return &copyAccepted
}

func cloneAcceptedMetadata(accepted AcceptedMetadataOperation) AcceptedMetadataOperation {
	accepted.State = cloneRoomState(accepted.State)
	return accepted
}

func cloneAcceptedMetadataPtr(accepted AcceptedMetadataOperation) *AcceptedMetadataOperation {
	copyAccepted := cloneAcceptedMetadata(accepted)
	return &copyAccepted
}

func cloneRoomState(state RoomState) RoomState {
	state.Documents = append([]DocumentState(nil), state.Documents...)
	state.Participants = make([]ParticipantSnapshot, len(state.Participants))
	for index, participant := range state.Participants {
		state.Participants[index] = cloneParticipant(participant)
	}
	return state
}

func sortParticipants(participants []ParticipantSnapshot) {
	for index := 1; index < len(participants); index++ {
		current := participants[index]
		position := index
		for position > 0 && (participants[position-1].JoinedAt.After(current.JoinedAt) || (participants[position-1].JoinedAt.Equal(current.JoinedAt) && participants[position-1].ID > current.ID)) {
			participants[position] = participants[position-1]
			position--
		}
		participants[position] = current
	}
}
