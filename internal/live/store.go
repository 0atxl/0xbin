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

// RoomStore is the durable live-room boundary. Session-only presence belongs
// to the in-memory room hub and is never represented here.
type RoomStore interface {
	CreateRoom(context.Context, RoomSnapshot) error
	GetRoomSnapshot(context.Context, string, time.Time) (RoomSnapshot, error)
	SaveSnapshot(context.Context, RoomSnapshot, time.Time) error
	AppendChanges(context.Context, string, []ChangeRecord, time.Time) error
	LoadChangesSince(context.Context, string, string, string, int, time.Time) ([]ChangeRecord, error)
	CompactChanges(context.Context, string, string, string, int, time.Time) error
	DeleteExpiredRooms(context.Context, time.Time, int) (int64, error)
}
