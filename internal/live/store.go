package live

import (
	"context"
	"errors"
	"time"
)

const (
	StreamMetadata   = "metadata"
	StreamDocument   = "document"
	MetadataStreamID = "metadata"
)

var (
	ErrRoomNotFound      = errors.New("live room not found")
	ErrRoomSlugCollision = errors.New("live room slug collision")
	ErrInvalidSnapshot   = errors.New("invalid live room snapshot")
	ErrInvalidChange     = errors.New("invalid live room change")
	ErrRevisionConflict  = errors.New("live room revision conflict")
	ErrHistoryCompacted  = errors.New("live room change history compacted")
)

// RoomSnapshot is the durable state of a live room. Presence, connections, and
// participant names are intentionally not part of this type.
type RoomSnapshot struct {
	Slug                     string
	PasswordHash             string
	ContentSize              int64
	MetadataRevision         int
	MetadataSnapshotRevision int
	ExpiresAt                time.Time
	CreatedAt                time.Time
	Documents                []DocumentSnapshot
}

type DocumentSnapshot struct {
	ID               string
	Name             string
	Language         string
	Content          string
	Position         int
	CurrentRevision  int
	SnapshotRevision int
	UpdatedAt        time.Time
}

type ChangeRecord struct {
	StreamKind string
	StreamID   string
	Revision   int
	Kind       string
	Payload    string
	CreatedAt  time.Time
}

// OperationRecord is the durable identity and authoritative result of one
// accepted mutation. Presence and session identity are deliberately excluded.
type OperationRecord struct {
	OperationID   string
	ClientID      string
	Fingerprint   string
	StreamKind    string
	StreamID      string
	BaseRevision  int
	Revision      int
	OperationKind string
	ResultPayload string
	CreatedAt     time.Time
}

// ChangeCommit atomically persists one accepted authority operation together
// with the current room snapshot. CompactThrough is zero during the normal
// edit path and advances only when the retained history threshold is crossed.
type ChangeCommit struct {
	Snapshot         RoomSnapshot
	Change           ChangeRecord
	Operation        OperationRecord
	CompactThrough   int
	RetainOperations int
}

// RoomStore is the durable live-room boundary. Session-only presence belongs
// to the in-memory room hub and is never represented here.
type RoomStore interface {
	CreateRoom(context.Context, RoomSnapshot) error
	GetRoomSnapshot(context.Context, string, time.Time) (RoomSnapshot, error)
	GetRoomSnapshotWithClientOperations(context.Context, string, string, int, time.Time) (RoomSnapshot, []OperationRecord, error)
	SaveSnapshot(context.Context, RoomSnapshot, time.Time) error
	CommitChange(context.Context, ChangeCommit, time.Time) error
	AppendChanges(context.Context, string, []ChangeRecord, time.Time) error
	LoadChangesSince(context.Context, string, string, string, int, time.Time) ([]ChangeRecord, error)
	LoadRecentOperations(context.Context, string, int, time.Time) ([]OperationRecord, error)
	LoadClientOperations(context.Context, string, string, int, time.Time) ([]OperationRecord, error)
	CompactChanges(context.Context, string, string, string, int, time.Time) error
	DeleteExpiredRooms(context.Context, time.Time, int) (int64, error)
}
