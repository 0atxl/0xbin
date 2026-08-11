package httpapi

import (
	"time"

	"github.com/0atxl/0xbin/internal/live"
)

// liveRoomResponse is shared by HTTP snapshots and WebSocket reconciliation
// events so both transports expose one authoritative wire representation.
type liveRoomResponse struct {
	Slug                     string                    `json:"slug"`
	URL                      string                    `json:"url,omitempty"`
	ExpiresAt                time.Time                 `json:"expires_at"`
	PasswordRequired         bool                      `json:"password_required"`
	MetadataRevision         int                       `json:"metadata_revision"`
	MetadataSnapshotRevision int                       `json:"metadata_snapshot_revision"`
	MaxBytes                 int64                     `json:"max_bytes"`
	MaxDocumentBytes         int64                     `json:"max_document_bytes"` // Always identical to MaxBytes.
	MaxTabs                  int                       `json:"max_tabs"`
	MaxWriters               int                       `json:"max_writers"`
	MaxViewers               int                       `json:"max_viewers"`
	MaxParticipants          int                       `json:"max_participants"`
	RoomLifetimeSeconds      int64                     `json:"room_lifetime_seconds"`
	Creator                  bool                      `json:"creator"`
	Locked                   bool                      `json:"locked"`
	Documents                []liveDocumentResponse    `json:"documents,omitempty"`
	Participants             []liveParticipantResponse `json:"participants,omitempty"`
	AcceptedOperationIDs     []string                  `json:"accepted_operation_ids,omitempty"`
}

type liveDocumentResponse struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Language         string `json:"language"`
	Content          string `json:"content"`
	Position         int    `json:"position"`
	Revision         int    `json:"revision"`
	SnapshotRevision int    `json:"snapshot_revision"`
}

type liveCursorResponse struct {
	DocumentID string `json:"document_id"`
	Revision   int    `json:"revision"`
	Anchor     int    `json:"anchor"`
	Head       int    `json:"head"`
}

type liveConnectionCursorResponse struct {
	ConnectionID string `json:"connection_id"`
	liveCursorResponse
}

type liveParticipantResponse struct {
	ID              string                         `json:"id"`
	Nickname        string                         `json:"nickname"`
	JoinedAt        time.Time                      `json:"joined_at"`
	Color           string                         `json:"color"`
	CurrentTab      string                         `json:"current_tab"`
	Cursor          *liveCursorResponse            `json:"cursor,omitempty"`
	Cursors         []liveConnectionCursorResponse `json:"cursors,omitempty"`
	Status          live.ParticipantStatus         `json:"status"`
	AccessClass     live.ParticipantAccessClass    `json:"access_class"`
	CanEdit         bool                           `json:"can_edit"`
	ConnectionCount int                            `json:"connection_count"`
	LastSeenAt      time.Time                      `json:"last_seen_at"`
}

func responseForLiveSnapshot(snapshot live.RoomSnapshot) liveRoomResponse {
	response := liveRoomResponse{Slug: snapshot.Slug, ExpiresAt: snapshot.ExpiresAt, PasswordRequired: false, MetadataRevision: snapshot.MetadataRevision, MetadataSnapshotRevision: snapshot.MetadataSnapshotRevision, Locked: snapshot.Locked, Documents: make([]liveDocumentResponse, 0, len(snapshot.Documents))}
	for _, document := range snapshot.Documents {
		response.Documents = append(response.Documents, liveDocumentResponse{ID: document.ID, Name: document.Name, Language: document.Language, Content: document.Content, Position: document.Position, Revision: document.CurrentRevision, SnapshotRevision: document.SnapshotRevision})
	}
	return response
}

func (api *liveAPI) responseForLiveSnapshot(snapshot live.RoomSnapshot) liveRoomResponse {
	response := responseForLiveSnapshot(snapshot)
	response.MaxBytes = api.cfg.LiveMaxBytes
	response.MaxDocumentBytes = api.cfg.LiveMaxBytes
	response.MaxTabs = api.cfg.LiveMaxTabs
	response.MaxWriters = api.cfg.LiveMaxWriters
	response.MaxViewers = api.cfg.LiveMaxViewers
	response.MaxParticipants = api.cfg.LiveMaxParticipants
	response.RoomLifetimeSeconds = int64(api.cfg.LiveRoomLifetime.Seconds())
	return response
}

func responseForLiveState(state live.RoomState) liveRoomResponse {
	response := liveRoomResponse{Slug: state.Slug, ExpiresAt: state.ExpiresAt, MetadataRevision: state.MetadataRevision, MetadataSnapshotRevision: state.MetadataSnapshotRevision, Locked: state.WatchOnly, Documents: make([]liveDocumentResponse, 0, len(state.Documents)), Participants: make([]liveParticipantResponse, 0, len(state.Participants))}
	for _, document := range state.Documents {
		response.Documents = append(response.Documents, liveDocumentResponse{ID: document.ID, Name: document.Name, Language: document.Language, Content: document.Content, Position: document.Position, Revision: document.Revision, SnapshotRevision: document.SnapshotRevision})
	}
	for _, participant := range state.Participants {
		response.Participants = append(response.Participants, responseForLiveParticipant(participant))
	}
	return response
}

func responseForLiveParticipant(participant live.ParticipantSnapshot) liveParticipantResponse {
	response := liveParticipantResponse{
		ID: participant.ID, Nickname: participant.Nickname, JoinedAt: participant.JoinedAt,
		Color: participant.Color, CurrentTab: participant.CurrentTab, Status: participant.Status,
		AccessClass: participant.AccessClass, CanEdit: participant.CanEdit,
		ConnectionCount: participant.ConnectionCount, LastSeenAt: participant.LastSeenAt,
	}
	if participant.Cursor != nil {
		response.Cursor = &liveCursorResponse{DocumentID: participant.Cursor.DocumentID, Revision: participant.Cursor.Revision, Anchor: participant.Cursor.Anchor, Head: participant.Cursor.Head}
	}
	for _, cursor := range participant.Cursors {
		response.Cursors = append(response.Cursors, liveConnectionCursorResponse{
			ConnectionID:       cursor.ConnectionID,
			liveCursorResponse: liveCursorResponse{DocumentID: cursor.DocumentID, Revision: cursor.Revision, Anchor: cursor.Anchor, Head: cursor.Head},
		})
	}
	return response
}
