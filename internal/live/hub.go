package live

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/0atxl/0xbin/internal/livecollab"
)

const (
	defaultHubMaxTabs            = 8
	defaultHubMaxBytes           = int64(1 << 20)
	defaultHubMaxWriters         = 10
	defaultHubMaxViewers         = 100
	defaultHubMaxParticipants    = defaultHubMaxWriters + defaultHubMaxViewers
	defaultHubMaxConnections     = 8
	defaultHubMaxMessageBytes    = 64 << 10
	defaultHubMaxHistoryRows     = 1000
	defaultHubMaxHistoryBytes    = int64(4 << 20)
	defaultHubReconnectGrace     = 30 * time.Second
	defaultHubHeartbeatInterval  = 20 * time.Second
	defaultHubParticipantTimeout = 60 * time.Second
	maxOperationIDBytes          = 128
	documentIDGenerationTries    = 8
)

var (
	ErrHubClosed           = errors.New("live hub is closed")
	ErrRoomExpired         = errors.New("live room expired")
	ErrRoomLimit           = errors.New("live room limit reached")
	ErrParticipantLimit    = errors.New("live participant limit reached")
	ErrConnectionLimit     = errors.New("live participant connection limit reached")
	ErrParticipantNotFound = errors.New("live participant not found")
	ErrParticipantInactive = errors.New("live participant is not connected")
	ErrSessionActive       = errors.New("live session is already connected")
	ErrCreatorRequired     = errors.New("live creator capability is required")
	ErrWatchOnly           = errors.New("live participant is watch-only")
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

// ParticipantRole is the legacy transport representation derived from whether
// a participant may currently edit the room.
type ParticipantRole string

const (
	ParticipantWriter    ParticipantRole = "writer"
	ParticipantWatchOnly ParticipantRole = "watch_only"
)

// ParticipantAccessClass describes capacity/authority independently from the
// participant's effective edit permission.
type ParticipantAccessClass string

const (
	ParticipantCreator      ParticipantAccessClass = "creator"
	ParticipantCollaborator ParticipantAccessClass = "collaborator"
	ParticipantViewer       ParticipantAccessClass = "viewer"
)

// HubOptions bounds all process-local room state. The HTTP/configuration layer
// can map environment settings into this type without exposing config internals
// to the live domain package.
type HubOptions struct {
	MaxTabs                      int
	MaxBytes                     int64
	MaxWriters                   int
	MaxViewers                   int
	MaxParticipants              int
	MaxConnectionsPerParticipant int
	MaxMessageBytes              int
	MaxHistoryRows               int
	MaxHistoryBytes              int64
	ReconnectGrace               time.Duration
	HeartbeatInterval            time.Duration
	ParticipantTimeout           time.Duration
	DocumentID                   func() (string, error)
}

func DefaultHubOptions() HubOptions {
	return HubOptions{
		MaxTabs:                      defaultHubMaxTabs,
		MaxBytes:                     defaultHubMaxBytes,
		MaxWriters:                   defaultHubMaxWriters,
		MaxViewers:                   defaultHubMaxViewers,
		MaxParticipants:              defaultHubMaxParticipants,
		MaxConnectionsPerParticipant: defaultHubMaxConnections,
		MaxMessageBytes:              defaultHubMaxMessageBytes,
		MaxHistoryRows:               defaultHubMaxHistoryRows,
		MaxHistoryBytes:              defaultHubMaxHistoryBytes,
		ReconnectGrace:               defaultHubReconnectGrace,
		HeartbeatInterval:            defaultHubHeartbeatInterval,
		ParticipantTimeout:           defaultHubParticipantTimeout,
	}
}

func (options HubOptions) validate() error {
	if options.MaxTabs < 1 || options.MaxBytes < 1 || options.MaxWriters < 1 || options.MaxViewers < 0 || options.MaxParticipants < 1 || options.MaxConnectionsPerParticipant < 1 || options.MaxMessageBytes < 1 || options.MaxHistoryRows < 1 || options.MaxHistoryBytes < 1 {
		return errors.New("live hub limits must be positive")
	}
	if options.MaxWriters+options.MaxViewers != options.MaxParticipants {
		return errors.New("live writer, viewer, and total limits are incoherent")
	}
	if int64(options.MaxMessageBytes) > options.MaxBytes {
		return errors.New("live message limit must not exceed room content limit")
	}
	if options.ReconnectGrace <= 0 || options.HeartbeatInterval <= 0 || options.ParticipantTimeout <= options.ReconnectGrace || options.ParticipantTimeout < 2*options.HeartbeatInterval {
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

// ConnectionCursor is one mounted page's ephemeral selection. The connection
// identifier is public only within the room and never grants authority.
type ConnectionCursor struct {
	ConnectionID string
	CursorSelection
}

type ParticipantSnapshot struct {
	ID              string
	Nickname        string
	JoinedAt        time.Time
	Color           string
	CurrentTab      string
	Cursor          *CursorSelection
	Cursors         []ConnectionCursor
	Status          ParticipantStatus
	AccessClass     ParticipantAccessClass
	CanEdit         bool
	ConnectionCount int
	Role            ParticipantRole
	LastSeenAt      time.Time
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
	WatchOnly                bool
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

// KnownRevisions identifies the HTTP room snapshot a joining client already
// holds. Room contents remain on HTTP; WebSocket join only bridges deltas.
type KnownRevisions struct {
	Metadata  int
	Documents map[string]int
}

// BridgeResult contains retained authority operations newer than a client's
// HTTP snapshot. Resync is true when bounded history cannot bridge safely.
type BridgeResult struct {
	DocumentChanges []AcceptedDocumentOperation
	MetadataChanges []AcceptedMetadataOperation
	Resync          bool
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
	Name        string
	Language    string
	Content     string
	Order       []string
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
	return hub.JoinWithIdentity(ctx, slug, legacyJoinIdentity(sessionID), CreatorCapability{}, now)
}

// JoinWithCreator grants creator authority only when the capability matches
// the room-bound hash loaded from durable storage.
func (hub *Hub) JoinWithCreator(ctx context.Context, slug, sessionID string, capability CreatorCapability, now time.Time) (JoinResult, error) {
	return hub.JoinWithIdentity(ctx, slug, legacyJoinIdentity(sessionID), capability, now)
}

// JoinWithIdentity groups bounded mounted-page connections under one stable
// browser participant while keeping operation clients connection-scoped.
func (hub *Hub) JoinWithIdentity(ctx context.Context, slug string, identity JoinIdentity, capability CreatorCapability, now time.Time) (JoinResult, error) {
	if strings.TrimSpace(slug) != slug || slug == "" {
		return JoinResult{}, ErrParticipantNotFound
	}
	if err := identity.Validate(); err != nil {
		return JoinResult{}, err
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

	result, err := room.join(ctx, hub, identity, capability, now)
	if errors.Is(err, ErrRoomExpired) {
		hub.removeRoom(slug, room)
	}
	return result, err
}

func legacyJoinIdentity(sessionID string) JoinIdentity {
	return JoinIdentity{ParticipantCredential: sessionID, ConnectionID: sessionID, ClientID: sessionID}
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

// SweepInterval is short enough to observe heartbeat loss and reconnect-grace
// expiry without retaining inactive rooms for an additional full interval.
func (hub *Hub) SweepInterval() time.Duration {
	interval := min(hub.options.HeartbeatInterval, hub.options.ReconnectGrace) / 2
	if interval <= 0 {
		return time.Second
	}
	return interval
}

// Sweep disconnects silent participants, removes expired rooms, and evicts
// rooms whose reconnect grace period has elapsed.
func (hub *Hub) Sweep(ctx context.Context, now time.Time) (int, error) {
	return hub.sweep(ctx, now, nil)
}

// SweepWithParticipantRemovals invokes publish for each participant whose
// reconnect grace has expired. The callback runs before the participant's
// process-local session and cursor state are removed, so transports can make
// the removal visible to remaining clients deterministically.
func (hub *Hub) SweepWithParticipantRemovals(ctx context.Context, now time.Time, publish func(slug, participantID string)) (int, error) {
	return hub.sweep(ctx, now, publish)
}

func (hub *Hub) sweep(ctx context.Context, now time.Time, publish func(slug, participantID string)) (int, error) {
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
		if err := room.sweep(ctx, now, publish); err != nil {
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
	hub          *Hub
	room         *room
	participant  string
	sessionID    string
	connectionID string
	clientID     string
	generation   uint64
	creator      bool
}

func (session *RoomSession) State() (RoomState, error) {
	if session == nil || session.room == nil {
		return RoomState{}, ErrParticipantNotFound
	}
	return session.room.stateFor(session.participant, session.connectionID, session.generation)
}

// Bridge returns only retained operations newer than the supplied HTTP
// snapshot. The caller must fetch HTTP again when history is unavailable.
func (session *RoomSession) Bridge(known KnownRevisions, now time.Time) (BridgeResult, error) {
	if session == nil || session.room == nil {
		return BridgeResult{}, ErrParticipantNotFound
	}
	return session.room.bridge(session.participant, session.connectionID, session.generation, known, normalizedNow(now))
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
	return session.room.submitDocument(ctx, session.participant, session.connectionID, session.generation, operation, normalizedNow(now))
}

func (session *RoomSession) ApplyMetadata(ctx context.Context, operation MetadataOperation, now time.Time) (AcceptedMetadataOperation, error) {
	if session == nil || session.room == nil {
		return AcceptedMetadataOperation{}, ErrParticipantNotFound
	}
	return session.room.applyMetadata(ctx, session.participant, session.connectionID, session.generation, operation, normalizedNow(now))
}

func (session *RoomSession) UpdatePresence(update PresenceUpdate, now time.Time) (ParticipantSnapshot, error) {
	if session == nil || session.room == nil {
		return ParticipantSnapshot{}, ErrParticipantNotFound
	}
	return session.room.updatePresence(session.participant, session.connectionID, session.generation, update, normalizedNow(now))
}

func (session *RoomSession) Rename(name string, now time.Time) (ParticipantSnapshot, error) {
	if session == nil || session.room == nil {
		return ParticipantSnapshot{}, ErrParticipantNotFound
	}
	return session.room.rename(session.participant, session.connectionID, session.generation, name, normalizedNow(now))
}

func (session *RoomSession) Heartbeat(now time.Time) error {
	if session == nil || session.room == nil {
		return ErrParticipantNotFound
	}
	return session.room.heartbeat(session.participant, session.connectionID, session.generation, normalizedNow(now))
}

func (session *RoomSession) Disconnect(now time.Time) error {
	if session == nil || session.room == nil {
		return ErrParticipantNotFound
	}
	return session.room.disconnect(session.participant, session.connectionID, session.generation, normalizedNow(now))
}

func (session *RoomSession) Leave(now time.Time) error {
	if session == nil || session.room == nil {
		return ErrParticipantNotFound
	}
	err := session.room.leave(session.participant, session.connectionID, session.generation, normalizedNow(now))
	if err == nil && session.hub != nil && session.room.shouldEvict(normalizedNow(now)) {
		session.hub.removeRoom(session.room.snapshotSlug(), session.room)
	}
	return err
}

// IsCreator reports whether this temporary session holds the room-scoped
// creator capability.
func (session *RoomSession) IsCreator() bool {
	return session != nil && session.creator
}

// ConnectionID identifies the mounted page represented by this session.
func (session *RoomSession) ConnectionID() string {
	if session == nil {
		return ""
	}
	return session.connectionID
}

// ClientID identifies this connection's durable operation stream.
func (session *RoomSession) ClientID() string {
	if session == nil {
		return ""
	}
	return session.clientID
}

// SetWatchOnly durably changes room mode. The creator remains editable while
// collaborators follow the room lock and viewers remain read-only.
func (session *RoomSession) SetWatchOnly(ctx context.Context, enabled bool, now time.Time) (RoomState, error) {
	if session == nil || session.room == nil {
		return RoomState{}, ErrParticipantNotFound
	}
	return session.room.setWatchOnly(ctx, session.participant, session.connectionID, session.generation, session.creator, enabled, normalizedNow(now))
}

type room struct {
	mu                        sync.Mutex
	store                     RoomStore
	options                   HubOptions
	snapshot                  RoomSnapshot
	documents                 map[string]*documentState
	order                     []string
	participants              map[string]*participantState
	sessions                  map[string]string
	names                     map[string]string
	watchOnly                 bool
	operations                map[string]operationRecord
	operationOrder            []string
	metadataHistory           []AcceptedMetadataOperation
	metadataHistorySizes      []int64
	metadataHistoryBytes      int64
	metadataCompactedRevision int
	dirty                     bool
	closed                    bool
}

type documentState struct {
	snapshot          DocumentSnapshot
	history           []documentHistory
	historyBytes      int64
	compactedRevision int
}

type documentHistory struct {
	OperationID string
	ClientID    string
	Revision    int
	BaseVersion int
	BeforeLen   int
	Bytes       int64
	Changes     livecollab.ChangeSet
}

type participantState struct {
	snapshot       ParticipantSnapshot
	sessionID      string
	accessClass    ParticipantAccessClass
	connections    map[string]*connectionState
	nextGeneration uint64
	disconnectedAt time.Time
}

type connectionState struct {
	id             string
	clientID       string
	generation     uint64
	currentTab     string
	cursor         *CursorSelection
	lastSeenAt     time.Time
	lastActivityAt time.Time
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
		store:                     store,
		options:                   options,
		snapshot:                  snapshot,
		documents:                 make(map[string]*documentState, len(snapshot.Documents)),
		participants:              make(map[string]*participantState),
		sessions:                  make(map[string]string),
		names:                     make(map[string]string),
		watchOnly:                 snapshot.Locked,
		operations:                make(map[string]operationRecord),
		metadataCompactedRevision: snapshot.MetadataSnapshotRevision,
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
	if err := room.loadOperations(ctx); err != nil {
		return nil, err
	}
	return room, nil
}

func (room *room) loadOperations(ctx context.Context) error {
	operations, err := room.store.LoadRecentOperations(ctx, room.snapshot.Slug, operationRecordLimit(room.options), beforeExpiry(room.snapshot.ExpiresAt))
	if err != nil {
		return fmt.Errorf("load live operation ledger: %w", err)
	}
	for _, operation := range operations {
		record, err := room.operationRecordFromDurable(operation)
		if err != nil {
			return fmt.Errorf("decode live operation %q: %w", operation.OperationID, err)
		}
		if _, exists := room.operations[operation.OperationID]; exists {
			return fmt.Errorf("%w: duplicate durable operation %q", ErrPersistence, operation.OperationID)
		}
		room.operations[operation.OperationID] = record
		room.operationOrder = append(room.operationOrder, operation.OperationID)
	}
	return nil
}

func (room *room) operationRecordFromDurable(operation OperationRecord) (operationRecord, error) {
	if err := room.ensureOperationIDs(operation.OperationID, operation.ClientID); err != nil || len(operation.Fingerprint) != sha256.Size*2 || operation.Revision <= 0 || operation.BaseRevision < 0 {
		return operationRecord{}, ErrPersistence
	}
	switch operation.StreamKind {
	case StreamDocument:
		if operation.OperationKind != "push_changes" || ValidateDocumentID(operation.StreamID) != nil {
			return operationRecord{}, ErrPersistence
		}
		var persisted persistedDocumentChange
		if err := json.Unmarshal([]byte(operation.ResultPayload), &persisted); err != nil {
			return operationRecord{}, err
		}
		changes, err := livecollab.ParseChangeSetJSON(persisted.Changes)
		if err != nil {
			return operationRecord{}, err
		}
		accepted := AcceptedDocumentOperation{
			OperationID: operation.OperationID, ClientID: operation.ClientID,
			DocumentID: operation.StreamID, BaseVersion: operation.BaseRevision,
			Revision: operation.Revision, Changes: changes,
		}
		if document := room.documents[operation.StreamID]; document != nil && document.snapshot.CurrentRevision == operation.Revision {
			accepted.Document = document.snapshot.Content
		}
		return operationRecord{fingerprint: operation.Fingerprint, document: &accepted}, nil
	case StreamMetadata:
		if operation.StreamID != MetadataStreamID {
			return operationRecord{}, ErrPersistence
		}
		var persisted persistedMetadataChange
		if err := json.Unmarshal([]byte(operation.ResultPayload), &persisted); err != nil {
			return operationRecord{}, err
		}
		if persisted.Kind != operation.OperationKind {
			return operationRecord{}, ErrPersistence
		}
		accepted := AcceptedMetadataOperation{
			OperationID: operation.OperationID, ClientID: operation.ClientID,
			Kind: persisted.Kind, DocumentID: persisted.DocumentID,
			Revision: operation.Revision, Name: persisted.Name,
			Language: persisted.Language, Content: persisted.Content,
			Order: append([]string(nil), persisted.Order...), State: room.stateLocked(),
		}
		return operationRecord{fingerprint: operation.Fingerprint, metadata: &accepted}, nil
	default:
		return operationRecord{}, ErrPersistence
	}
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

func (room *room) join(ctx context.Context, hub *Hub, identity JoinIdentity, capability CreatorCapability, now time.Time) (JoinResult, error) {
	room.mu.Lock()
	defer room.mu.Unlock()
	if err := room.ensureActiveLocked(now); err != nil {
		return JoinResult{}, err
	}
	creator := capability.MatchesRoomHash(room.snapshot.Slug, room.snapshot.CreatorTokenHash)
	sessionID := identity.ParticipantCredential
	room.expireConnectionsLocked(now)
	room.pruneDisconnectedLocked(now)
	if existingID, exists := room.sessions[sessionID]; exists {
		participant := room.participants[existingID]
		if participant != nil {
			if _, active := participant.connections[identity.ConnectionID]; active {
				return JoinResult{}, ErrSessionActive
			}
			if len(participant.connections) >= room.options.MaxConnectionsPerParticipant {
				return JoinResult{}, ErrConnectionLimit
			}
			if creator && participant.accessClass != ParticipantCreator {
				if participant.accessClass == ParticipantViewer && room.writerSlotCountLocked() >= room.options.MaxWriters {
					return JoinResult{}, ErrParticipantLimit
				}
				participant.accessClass = ParticipantCreator
			}
			connection := room.addConnectionLocked(participant, identity, now)
			participant.disconnectedAt = time.Time{}
			room.refreshParticipantLocked(participant)
			return room.joinResultLocked(hub, participant, connection, creator, true), nil
		}
		delete(room.sessions, sessionID)
	}
	if len(room.participants) >= room.options.MaxParticipants {
		return JoinResult{}, ErrParticipantLimit
	}
	accessClass, err := room.accessClassForJoinLocked(creator)
	if err != nil {
		return JoinResult{}, err
	}
	participantID, err := ParticipantIDForRoom(room.snapshot.Slug, identity.ParticipantCredential)
	if err != nil {
		return JoinResult{}, err
	}
	if _, exists := room.participants[participantID]; exists {
		return JoinResult{}, ErrOperationConflict
	}
	usedNames := make(map[string]struct{}, len(room.names))
	for name := range room.names {
		usedNames[name] = struct{}{}
	}
	nickname := ""
	if identity.PreferredNameSet {
		if _, exists := usedNames[NameKey(identity.PreferredName)]; !exists {
			nickname = identity.PreferredName
		}
	}
	if nickname == "" {
		nickname, err = hub.names.Generate(usedNames)
		if err != nil {
			return JoinResult{}, err
		}
	}
	participant := &participantState{
		sessionID: sessionID, accessClass: accessClass,
		connections: make(map[string]*connectionState),
		snapshot: ParticipantSnapshot{
			ID: participantID, Nickname: nickname, JoinedAt: now,
			Color: participantColor(participantID), CurrentTab: room.order[0],
			Status: ParticipantConnected, ConnectionCount: 1, LastSeenAt: now,
		},
	}
	connection := room.addConnectionLocked(participant, identity, now)
	room.refreshParticipantLocked(participant)
	room.participants[participantID] = participant
	room.sessions[sessionID] = participantID
	room.names[NameKey(nickname)] = participantID
	return room.joinResultLocked(hub, participant, connection, creator, false), nil
}

func (room *room) addConnectionLocked(participant *participantState, identity JoinIdentity, now time.Time) *connectionState {
	participant.nextGeneration++
	currentTab := participant.snapshot.CurrentTab
	if room.documents[currentTab] == nil {
		currentTab = room.order[0]
	}
	connection := &connectionState{
		id: identity.ConnectionID, clientID: identity.ClientID,
		generation: participant.nextGeneration, currentTab: currentTab,
		lastSeenAt: now, lastActivityAt: now,
	}
	participant.connections[connection.id] = connection
	return connection
}

func (room *room) joinResultLocked(hub *Hub, participant *participantState, connection *connectionState, creator, reconnected bool) JoinResult {
	return JoinResult{
		Session: &RoomSession{
			hub: hub, room: room, participant: participant.snapshot.ID,
			sessionID: participant.sessionID, connectionID: connection.id,
			clientID: connection.clientID, generation: connection.generation, creator: creator,
		},
		Participant: cloneParticipant(participant.snapshot),
		State:       room.stateLocked(), Reconnected: reconnected,
	}
}

func (room *room) refreshParticipantLocked(participant *participantState) {
	participant.snapshot.AccessClass = participant.accessClass
	participant.snapshot.CanEdit = participantCanEdit(participant.accessClass, room.watchOnly)
	if participant.snapshot.CanEdit {
		participant.snapshot.Role = ParticipantWriter
	} else {
		participant.snapshot.Role = ParticipantWatchOnly
	}
	participant.snapshot.ConnectionCount = len(participant.connections)
	participant.snapshot.Cursors = participant.snapshot.Cursors[:0]
	participant.snapshot.Cursor = nil
	if len(participant.connections) == 0 {
		if participant.snapshot.Status != ParticipantOffline {
			participant.snapshot.Status = ParticipantConnectionLost
		}
		return
	}

	participant.snapshot.Status = ParticipantConnected
	var latestActivity *connectionState
	var latestSeen *connectionState
	for _, connection := range participant.connections {
		if latestActivity == nil || connection.lastActivityAt.After(latestActivity.lastActivityAt) ||
			(connection.lastActivityAt.Equal(latestActivity.lastActivityAt) && connection.generation > latestActivity.generation) ||
			(connection.lastActivityAt.Equal(latestActivity.lastActivityAt) && connection.generation == latestActivity.generation && connection.id < latestActivity.id) {
			latestActivity = connection
		}
		if latestSeen == nil || connection.lastSeenAt.After(latestSeen.lastSeenAt) ||
			(connection.lastSeenAt.Equal(latestSeen.lastSeenAt) && connection.generation > latestSeen.generation) ||
			(connection.lastSeenAt.Equal(latestSeen.lastSeenAt) && connection.generation == latestSeen.generation && connection.id < latestSeen.id) {
			latestSeen = connection
		}
		if connection.cursor != nil {
			participant.snapshot.Cursors = append(participant.snapshot.Cursors, ConnectionCursor{
				ConnectionID: connection.id, CursorSelection: *connection.cursor,
			})
		}
	}
	sort.Slice(participant.snapshot.Cursors, func(left, right int) bool {
		return participant.snapshot.Cursors[left].ConnectionID < participant.snapshot.Cursors[right].ConnectionID
	})
	participant.snapshot.CurrentTab = latestActivity.currentTab
	participant.snapshot.LastSeenAt = latestSeen.lastSeenAt
	if latestActivity.cursor != nil {
		cursor := *latestActivity.cursor
		participant.snapshot.Cursor = &cursor
	}
}

func participantCanEdit(accessClass ParticipantAccessClass, locked bool) bool {
	switch accessClass {
	case ParticipantCreator:
		return true
	case ParticipantCollaborator:
		return !locked
	default:
		return false
	}
}

func (room *room) markConnectionActivityLocked(participant *participantState, connection *connectionState, now time.Time) {
	connection.lastSeenAt = now
	connection.lastActivityAt = now
	room.refreshParticipantLocked(participant)
}

func (room *room) writerSlotCountLocked() int {
	count := 0
	for _, participant := range room.participants {
		if participant.accessClass == ParticipantCreator || participant.accessClass == ParticipantCollaborator {
			count++
		}
	}
	return count
}

func (room *room) viewerSlotCountLocked() int {
	count := 0
	for _, participant := range room.participants {
		if participant.accessClass == ParticipantViewer {
			count++
		}
	}
	return count
}

func (room *room) accessClassForJoinLocked(creator bool) (ParticipantAccessClass, error) {
	writers := room.writerSlotCountLocked()
	if creator {
		if writers >= room.options.MaxWriters {
			return "", ErrParticipantLimit
		}
		return ParticipantCreator, nil
	}

	collaboratorLimit := room.options.MaxWriters
	if len(room.snapshot.CreatorTokenHash) > 0 && !room.hasCreatorLocked() {
		collaboratorLimit--
	}
	if writers < collaboratorLimit {
		return ParticipantCollaborator, nil
	}
	if room.viewerSlotCountLocked() >= room.options.MaxViewers {
		return "", ErrParticipantLimit
	}
	return ParticipantViewer, nil
}

func (room *room) hasCreatorLocked() bool {
	for _, participant := range room.participants {
		if participant.accessClass == ParticipantCreator {
			return true
		}
	}
	return false
}

func (room *room) setWatchOnly(ctx context.Context, participantID, connectionID string, generation uint64, creator, enabled bool, now time.Time) (RoomState, error) {
	room.mu.Lock()
	defer room.mu.Unlock()
	actor, connection, err := room.ensureConnectionLocked(participantID, connectionID, generation, now)
	if err != nil {
		return RoomState{}, err
	}
	room.markConnectionActivityLocked(actor, connection, now)
	if !creator {
		return RoomState{}, ErrCreatorRequired
	}
	if room.watchOnly == enabled {
		return room.stateLocked(), nil
	}
	if err := room.store.SetRoomLocked(ctx, room.snapshot.Slug, enabled, now); err != nil {
		return RoomState{}, fmt.Errorf("%w: persist live room lock: %v", ErrPersistence, err)
	}
	room.watchOnly = enabled
	room.snapshot.Locked = enabled
	for _, participant := range room.participants {
		room.refreshParticipantLocked(participant)
	}
	return room.stateLocked(), nil
}

func (room *room) submitDocument(ctx context.Context, participantID, connectionID string, generation uint64, operation DocumentOperation, now time.Time) (AcceptedDocumentOperation, error) {
	room.mu.Lock()
	defer room.mu.Unlock()
	participant, connection, err := room.ensureConnectionLocked(participantID, connectionID, generation, now)
	if err != nil {
		return AcceptedDocumentOperation{}, err
	}
	room.markConnectionActivityLocked(participant, connection, now)
	if !room.participants[participantID].snapshot.CanEdit {
		return AcceptedDocumentOperation{}, ErrWatchOnly
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

func (room *room) applyMetadata(ctx context.Context, participantID, connectionID string, generation uint64, operation MetadataOperation, now time.Time) (AcceptedMetadataOperation, error) {
	room.mu.Lock()
	defer room.mu.Unlock()
	participant, connection, err := room.ensureConnectionLocked(participantID, connectionID, generation, now)
	if err != nil {
		return AcceptedMetadataOperation{}, err
	}
	room.markConnectionActivityLocked(participant, connection, now)
	if !room.participants[participantID].snapshot.CanEdit {
		return AcceptedMetadataOperation{}, ErrWatchOnly
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
		accepted.Name, accepted.Language, accepted.Content = operation.Name, operation.Language, operation.Content
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
		accepted.Name, accepted.Language = operation.Name, operation.Language
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
		fallbackTab := room.order[0]
		for _, participant := range room.participants {
			for _, connection := range participant.connections {
				if connection.currentTab == operation.DocumentID {
					connection.currentTab = fallbackTab
				}
				if connection.cursor != nil && connection.cursor.DocumentID == operation.DocumentID {
					connection.cursor = nil
				}
			}
			room.refreshParticipantLocked(participant)
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
		accepted.Order = append([]string(nil), operation.Order...)
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

func (room *room) updatePresence(participantID, connectionID string, generation uint64, update PresenceUpdate, now time.Time) (ParticipantSnapshot, error) {
	room.mu.Lock()
	defer room.mu.Unlock()
	participant, connection, err := room.ensureConnectionLocked(participantID, connectionID, generation, now)
	if err != nil {
		return ParticipantSnapshot{}, err
	}
	if update.CurrentTab != "" {
		if room.documents[update.CurrentTab] == nil {
			return ParticipantSnapshot{}, ErrDocumentNotFound
		}
		connection.currentTab = update.CurrentTab
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
		connection.cursor = &cursor
	}
	connection.lastSeenAt = now
	connection.lastActivityAt = now
	room.refreshParticipantLocked(participant)
	return cloneParticipant(participant.snapshot), nil
}

func (room *room) rename(participantID, connectionID string, generation uint64, name string, now time.Time) (ParticipantSnapshot, error) {
	room.mu.Lock()
	defer room.mu.Unlock()
	participant, connection, err := room.ensureConnectionLocked(participantID, connectionID, generation, now)
	if err != nil {
		return ParticipantSnapshot{}, err
	}
	if err := ValidateNickname(name); err != nil {
		return ParticipantSnapshot{}, err
	}
	key := NameKey(name)
	if owner, exists := room.names[key]; exists && owner != participantID {
		return ParticipantSnapshot{}, ErrNameTaken
	}
	delete(room.names, NameKey(participant.snapshot.Nickname))
	room.names[key] = participantID
	participant.snapshot.Nickname = name
	room.markConnectionActivityLocked(participant, connection, now)
	return cloneParticipant(participant.snapshot), nil
}

func (room *room) heartbeat(participantID, connectionID string, generation uint64, now time.Time) error {
	room.mu.Lock()
	defer room.mu.Unlock()
	participant, connection, err := room.ensureConnectionLocked(participantID, connectionID, generation, now)
	if err != nil {
		return err
	}
	connection.lastSeenAt = now
	room.refreshParticipantLocked(participant)
	return nil
}

func (room *room) disconnect(participantID, connectionID string, generation uint64, now time.Time) error {
	room.mu.Lock()
	defer room.mu.Unlock()
	participant := room.participants[participantID]
	if participant == nil {
		return ErrParticipantNotFound
	}
	connection := participant.connections[connectionID]
	if connection == nil || connection.generation != generation {
		return ErrParticipantInactive
	}
	delete(participant.connections, connectionID)
	if len(participant.connections) == 0 {
		participant.snapshot.LastSeenAt = now
		participant.disconnectedAt = now
	}
	room.refreshParticipantLocked(participant)
	return nil
}

func (room *room) leave(participantID, connectionID string, generation uint64, now time.Time) error {
	room.mu.Lock()
	defer room.mu.Unlock()
	if err := room.ensureActiveLocked(now); err != nil {
		return err
	}
	participant := room.participants[participantID]
	if participant == nil {
		return ErrParticipantNotFound
	}
	connection := participant.connections[connectionID]
	if connection == nil || connection.generation != generation {
		return ErrParticipantInactive
	}
	delete(participant.connections, connectionID)
	if len(participant.connections) > 0 {
		room.refreshParticipantLocked(participant)
		return nil
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

func (room *room) stateFor(participantID, connectionID string, generation uint64) (RoomState, error) {
	room.mu.Lock()
	defer room.mu.Unlock()
	if room.closed {
		return RoomState{}, ErrRoomExpired
	}
	participant := room.participants[participantID]
	if participantID == "" || participant == nil {
		return RoomState{}, ErrParticipantNotFound
	}
	connection := participant.connections[connectionID]
	if connection == nil || connection.generation != generation {
		return RoomState{}, ErrParticipantInactive
	}
	return room.stateLocked(), nil
}

func (room *room) bridge(participantID, connectionID string, generation uint64, known KnownRevisions, now time.Time) (BridgeResult, error) {
	room.mu.Lock()
	defer room.mu.Unlock()
	if _, _, err := room.ensureConnectionLocked(participantID, connectionID, generation, now); err != nil {
		return BridgeResult{}, err
	}
	if known.Metadata < room.metadataCompactedRevision || known.Metadata > room.snapshot.MetadataRevision {
		return BridgeResult{Resync: true}, nil
	}
	result := BridgeResult{}
	knownDocuments := make(map[string]int, len(known.Documents))
	for documentID, revision := range known.Documents {
		knownDocuments[documentID] = revision
	}
	created, deleted := make(map[string]bool), make(map[string]bool)
	for _, accepted := range room.metadataHistory {
		if accepted.Revision <= known.Metadata {
			continue
		}
		result.MetadataChanges = append(result.MetadataChanges, cloneAcceptedMetadata(accepted))
		switch accepted.Kind {
		case "document_create":
			created[accepted.DocumentID] = true
			knownDocuments[accepted.DocumentID] = 0
		case "document_delete":
			deleted[accepted.DocumentID] = true
		}
	}
	for _, documentID := range room.order {
		document := room.documents[documentID]
		version, ok := knownDocuments[documentID]
		if !ok || version < document.compactedRevision || version > document.snapshot.CurrentRevision {
			return BridgeResult{Resync: true}, nil
		}
	}
	for documentID := range knownDocuments {
		if room.documents[documentID] == nil && !deleted[documentID] {
			return BridgeResult{Resync: true}, nil
		}
	}
	for _, documentID := range room.order {
		document := room.documents[documentID]
		for _, accepted := range document.history {
			if accepted.Revision <= knownDocuments[documentID] {
				continue
			}
			result.DocumentChanges = append(result.DocumentChanges, AcceptedDocumentOperation{
				OperationID: accepted.OperationID,
				ClientID:    accepted.ClientID,
				DocumentID:  documentID,
				BaseVersion: accepted.BaseVersion,
				Revision:    accepted.Revision,
				Changes:     accepted.Changes,
			})
		}
	}
	return result, nil
}

func (room *room) sweep(ctx context.Context, now time.Time, publish func(slug, participantID string)) error {
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
			clear(participant.connections)
			participant.snapshot.Status = ParticipantOffline
			room.refreshParticipantLocked(participant)
		}
		room.closed = true
		return nil
	}
	room.expireConnectionsLocked(now)
	room.pruneDisconnectedLocked(now, publish)
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
		clear(participant.connections)
		participant.snapshot.Status = ParticipantOffline
		room.refreshParticipantLocked(participant)
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

func (room *room) ensureConnectionLocked(participantID, connectionID string, generation uint64, now time.Time) (*participantState, *connectionState, error) {
	if err := room.ensureActiveLocked(now); err != nil {
		return nil, nil, err
	}
	participant := room.participants[participantID]
	if participant == nil {
		return nil, nil, ErrParticipantNotFound
	}
	connection := participant.connections[connectionID]
	if connection == nil || connection.generation != generation {
		return nil, nil, ErrParticipantInactive
	}
	connection.lastSeenAt = now
	room.refreshParticipantLocked(participant)
	return participant, connection, nil
}

func (room *room) pruneDisconnectedLocked(now time.Time, publish ...func(slug, participantID string)) {
	participantIDs := make([]string, 0)
	for participantID, participant := range room.participants {
		if len(participant.connections) > 0 || participant.disconnectedAt.IsZero() || now.Sub(participant.disconnectedAt) <= room.options.ReconnectGrace {
			continue
		}
		participantIDs = append(participantIDs, participantID)
	}
	sort.Strings(participantIDs)
	for _, participantID := range participantIDs {
		participant := room.participants[participantID]
		if len(publish) > 0 && publish[0] != nil {
			publish[0](room.snapshot.Slug, participantID)
		}
		delete(room.participants, participantID)
		delete(room.sessions, participant.sessionID)
		delete(room.names, NameKey(participant.snapshot.Nickname))
	}
}

func (room *room) expireConnectionsLocked(now time.Time) {
	for _, participant := range room.participants {
		removed := false
		var finalLostAt time.Time
		for connectionID, connection := range participant.connections {
			if now.Sub(connection.lastSeenAt) > room.options.ParticipantTimeout {
				delete(participant.connections, connectionID)
				removed = true
				timedOutAt := connection.lastSeenAt.Add(room.options.ParticipantTimeout)
				if timedOutAt.After(finalLostAt) {
					finalLostAt = timedOutAt
				}
			}
		}
		if !removed {
			continue
		}
		if len(participant.connections) == 0 {
			participant.snapshot.LastSeenAt = finalLostAt
			participant.disconnectedAt = finalLostAt
		}
		room.refreshParticipantLocked(participant)
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
		WatchOnly:                room.watchOnly,
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
	compactThrough := 0
	if documentResult != nil {
		document := room.documents[documentResult.DocumentID]
		if len(document.history)+1 > room.options.MaxHistoryRows || document.historyBytes+int64(len(change.Payload)) > room.options.MaxHistoryBytes {
			compactThrough = documentResult.Revision
		}
	} else if metadataResult != nil {
		if len(room.metadataHistory)+1 > room.options.MaxHistoryRows || room.metadataHistoryBytes+int64(len(change.Payload)) > room.options.MaxHistoryBytes {
			compactThrough = metadataResult.Revision
		}
	}
	durable := room.durableSnapshotLocked()
	operation := OperationRecord{
		Fingerprint: fingerprint, StreamKind: change.StreamKind,
		StreamID: change.StreamID, Revision: change.Revision,
		OperationKind: change.Kind, ResultPayload: change.Payload,
		CreatedAt: change.CreatedAt,
	}
	if documentResult != nil {
		operation.OperationID, operation.ClientID = documentResult.OperationID, documentResult.ClientID
		operation.BaseRevision = documentResult.BaseVersion
	} else if metadataResult != nil {
		operation.OperationID, operation.ClientID = metadataResult.OperationID, metadataResult.ClientID
		operation.BaseRevision = backup.snapshot.MetadataRevision
	}
	if err := room.store.CommitChange(ctx, ChangeCommit{Snapshot: durable, Change: change, Operation: operation, CompactThrough: compactThrough, RetainOperations: operationRecordLimit(room.options)}, now); err != nil {
		room.restoreLocked(backup)
		return fmt.Errorf("%w: commit: %w", ErrPersistence, err)
	}
	room.snapshot = durable
	room.markDocumentsDurableLocked()
	if documentResult != nil {
		room.recordDocumentOperation(fingerprint, *documentResult, beforeLength, int64(len(change.Payload)))
		if compactThrough > 0 {
			document := room.documents[documentResult.DocumentID]
			document.history = nil
			document.historyBytes = 0
			document.compactedRevision = compactThrough
		}
	} else if metadataResult != nil {
		room.recordMetadataOperation(fingerprint, *metadataResult, int64(len(change.Payload)))
		if compactThrough > 0 {
			room.metadataHistory = nil
			room.metadataHistorySizes = nil
			room.metadataHistoryBytes = 0
			room.metadataCompactedRevision = compactThrough
		}
	}
	room.dirty = false
	return nil
}

func (room *room) recordDocumentOperation(fingerprint string, accepted AcceptedDocumentOperation, beforeLength int, payloadBytes int64) {
	room.operations[accepted.OperationID] = operationRecord{fingerprint: fingerprint, document: cloneAcceptedDocumentPtr(accepted)}
	room.operationOrder = append(room.operationOrder, accepted.OperationID)
	document := room.documents[accepted.DocumentID]
	document.addHistory(documentHistory{
		OperationID: accepted.OperationID, ClientID: accepted.ClientID,
		Revision: accepted.Revision, BaseVersion: accepted.Revision - 1,
		BeforeLen: beforeLength, Bytes: payloadBytes, Changes: accepted.Changes,
	})
	document.pruneHistory(room.options.MaxHistoryRows, room.options.MaxHistoryBytes)
	room.pruneOperationRecords()
}

func (room *room) recordMetadataOperation(fingerprint string, accepted AcceptedMetadataOperation, payloadBytes int64) {
	room.operations[accepted.OperationID] = operationRecord{fingerprint: fingerprint, metadata: cloneAcceptedMetadataPtr(accepted)}
	room.operationOrder = append(room.operationOrder, accepted.OperationID)
	room.metadataHistory = append(room.metadataHistory, cloneAcceptedMetadata(accepted))
	room.metadataHistorySizes = append(room.metadataHistorySizes, payloadBytes)
	room.metadataHistoryBytes += payloadBytes
	room.pruneMetadataHistory()
	room.pruneOperationRecords()
}

func (room *room) pruneMetadataHistory() {
	for len(room.metadataHistory) > room.options.MaxHistoryRows || room.metadataHistoryBytes > room.options.MaxHistoryBytes {
		if len(room.metadataHistory) == 0 {
			break
		}
		room.metadataHistoryBytes -= room.metadataHistorySizes[0]
		room.metadataHistory = room.metadataHistory[1:]
		room.metadataHistorySizes = room.metadataHistorySizes[1:]
	}
	if len(room.metadataHistory) > 0 {
		room.metadataCompactedRevision = room.metadataHistory[0].Revision - 1
		return
	}
	room.metadataCompactedRevision = room.snapshot.MetadataRevision
}

func (document *documentState) addHistory(accepted documentHistory) {
	document.history = append(document.history, accepted)
	document.historyBytes += accepted.Bytes
}

func (room *room) pruneOperationRecords() {
	maxRecords := operationRecordLimit(room.options)
	for len(room.operationOrder) > maxRecords {
		id := room.operationOrder[0]
		room.operationOrder = room.operationOrder[1:]
		delete(room.operations, id)
	}
}

func operationRecordLimit(options HubOptions) int {
	maxRecords := options.MaxHistoryRows * 2
	if maxRecords < 32 {
		return 32
	}
	return maxRecords
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
	if operationID == "" || len(operationID) > maxOperationIDBytes || !utf8.ValidString(operationID) || clientID == "" || len(clientID) > MaxClientIDBytes || !utf8.ValidString(clientID) {
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
		data = []byte(fmt.Sprintf("%T", operation))
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
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
	if selection.Anchor == selection.Head {
		position, err := changes.MapPos(selection.Head, 1)
		if err != nil {
			return livecollab.SelectionRange{}, err
		}
		return livecollab.SelectionRange{Anchor: position, Head: position}, nil
	}
	return selection.Map(changes)
}

func cloneParticipant(participant ParticipantSnapshot) ParticipantSnapshot {
	copyParticipant := participant
	copyParticipant.Cursors = append([]ConnectionCursor(nil), participant.Cursors...)
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
	accepted.Order = append([]string(nil), accepted.Order...)
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
