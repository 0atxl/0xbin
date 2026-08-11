package live

import (
	"errors"
	"time"

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
