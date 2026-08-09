package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/0atxl/0xbin/internal/live"
	"github.com/0atxl/0xbin/internal/livecollab"
)

// liveWireMessage is the canonical snake_case client-to-server envelope. The
// type field selects which other fields are meaningful.
type liveWireMessage struct {
	Type              string                     `json:"type"`
	SessionID         string                     `json:"session_id,omitempty"`
	ClientID          string                     `json:"client_id,omitempty"`
	OperationID       string                     `json:"operation_id,omitempty"`
	DocumentID        string                     `json:"document_id,omitempty"`
	BaseVersion       int                        `json:"base_version,omitempty"`
	Changes           json.RawMessage            `json:"changes,omitempty"`
	Name              string                     `json:"name,omitempty"`
	Language          string                     `json:"language,omitempty"`
	Content           string                     `json:"content,omitempty"`
	Order             []string                   `json:"order,omitempty"`
	CurrentTab        string                     `json:"current_tab,omitempty"`
	Revision          int                        `json:"revision,omitempty"`
	Anchor            int                        `json:"anchor,omitempty"`
	Head              int                        `json:"head,omitempty"`
	ParticipantID     string                     `json:"participant_id,omitempty"`
	WatchOnly         bool                       `json:"watch_only,omitempty"`
	MetadataRevision  int                        `json:"metadata_revision,omitempty"`
	DocumentRevisions []liveWireDocumentRevision `json:"document_revisions,omitempty"`
}

type liveWireDocumentRevision struct {
	DocumentID string `json:"document_id"`
	Revision   int    `json:"revision"`
}

type liveJoinedEvent struct {
	Type              string                     `json:"type"`
	ExpiresAt         time.Time                  `json:"expires_at"`
	MetadataRevision  int                        `json:"metadata_revision"`
	DocumentRevisions []liveWireDocumentRevision `json:"document_revisions"`
	Participants      []liveParticipantResponse  `json:"participants"`
	Participant       liveParticipantResponse    `json:"participant"`
	Creator           bool                       `json:"creator"`
	WatchOnly         bool                       `json:"watch_only"`
	Reconnected       bool                       `json:"reconnected"`
}

type liveRoomModeEvent struct {
	Type         string                    `json:"type"`
	WatchOnly    bool                      `json:"watch_only"`
	Participants []liveParticipantResponse `json:"participants"`
}

type liveParticipantRemovedEvent struct {
	Type          string `json:"type"`
	ParticipantID string `json:"participant_id"`
}

type liveChangesEvent struct {
	Type        string          `json:"type"`
	OperationID string          `json:"operation_id"`
	ClientID    string          `json:"client_id"`
	DocumentID  string          `json:"document_id"`
	BaseVersion int             `json:"base_version"`
	Revision    int             `json:"revision"`
	Changes     json.RawMessage `json:"changes"`
	Duplicate   bool            `json:"duplicate,omitempty"`
}

type liveStatusEvent struct {
	Type   string `json:"type"`
	Status string `json:"status"`
}

type liveMetadataEvent struct {
	Type             string                `json:"type"`
	OperationID      string                `json:"operation_id"`
	ClientID         string                `json:"client_id"`
	DocumentID       string                `json:"document_id,omitempty"`
	MetadataRevision int                   `json:"metadata_revision"`
	Document         *liveDocumentResponse `json:"document,omitempty"`
	Name             string                `json:"name,omitempty"`
	Language         string                `json:"language,omitempty"`
	Order            []string              `json:"order,omitempty"`
	Duplicate        bool                  `json:"duplicate,omitempty"`
}

func decodeLiveWireMessage(data []byte) (liveWireMessage, error) {
	var message liveWireMessage
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&message); err != nil {
		return liveWireMessage{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return liveWireMessage{}, errors.New("invalid live wire message")
	}
	if message.Type == "" {
		return liveWireMessage{}, errors.New("live wire message type is required")
	}
	switch message.Type {
	case "join", "heartbeat", "ack", "push_changes", "document_create", "document_update", "document_delete", "document_reorder", "room_watch_only", "participant_remove", "presence", "participant_rename":
	default:
		return liveWireMessage{}, errors.New("unknown live wire message")
	}
	return message, nil
}

func knownLiveRevisions(message liveWireMessage) (live.KnownRevisions, bool) {
	if message.MetadataRevision < 0 || len(message.DocumentRevisions) == 0 {
		return live.KnownRevisions{}, false
	}
	known := live.KnownRevisions{Metadata: message.MetadataRevision, Documents: make(map[string]int, len(message.DocumentRevisions))}
	for _, document := range message.DocumentRevisions {
		if document.DocumentID == "" || document.Revision < 0 {
			return live.KnownRevisions{}, false
		}
		if _, exists := known.Documents[document.DocumentID]; exists {
			return live.KnownRevisions{}, false
		}
		known.Documents[document.DocumentID] = document.Revision
	}
	return known, true
}

func liveDocumentRevisions(state live.RoomState) []liveWireDocumentRevision {
	revisions := make([]liveWireDocumentRevision, 0, len(state.Documents))
	for _, document := range state.Documents {
		revisions = append(revisions, liveWireDocumentRevision{DocumentID: document.ID, Revision: document.Revision})
	}
	return revisions
}

func documentWireEvent(accepted live.AcceptedDocumentOperation) liveChangesEvent {
	return liveChangesEvent{
		Type: "changes", OperationID: accepted.OperationID, ClientID: accepted.ClientID,
		DocumentID: accepted.DocumentID, BaseVersion: accepted.BaseVersion,
		Revision: accepted.Revision, Changes: encodeLiveChangeSet(accepted.Changes), Duplicate: accepted.Duplicate,
	}
}

func metadataWireEvent(accepted live.AcceptedMetadataOperation) liveMetadataEvent {
	event := liveMetadataEvent{
		OperationID: accepted.OperationID, ClientID: accepted.ClientID,
		DocumentID: accepted.DocumentID, MetadataRevision: accepted.Revision,
		Duplicate: accepted.Duplicate,
	}
	switch accepted.Kind {
	case "document_create":
		event.Type = "document_created"
		for _, document := range accepted.State.Documents {
			if document.ID == accepted.DocumentID {
				event.Document = &liveDocumentResponse{ID: document.ID, Name: document.Name, Language: document.Language, Content: document.Content, Position: document.Position, Revision: document.Revision, SnapshotRevision: document.SnapshotRevision}
				break
			}
		}
	case "document_update":
		event.Type, event.Name, event.Language = "document_updated", accepted.Name, accepted.Language
	case "document_delete":
		event.Type = "document_deleted"
	case "document_reorder":
		event.Type, event.Order = "document_reordered", append([]string(nil), accepted.Order...)
	}
	return event
}

func encodeLiveWireEvent(event any) []byte {
	data, err := json.Marshal(event)
	if err != nil {
		return []byte(`{"type":"error","code":"service_unavailable","message":"Service is temporarily unavailable"}`)
	}
	return data
}

func liveEvent(kind string, fields map[string]any) []byte {
	fields["type"] = kind
	return encodeLiveWireEvent(fields)
}

func encodeLiveChangeSet(changes livecollab.ChangeSet) json.RawMessage {
	parts := make([]any, 0, len(changes.Sections))
	for _, section := range changes.Sections {
		if section.NewLen < 0 {
			parts = append(parts, section.OldLen)
			continue
		}
		values := []any{section.OldLen}
		for _, line := range strings.Split(section.Insert, "\n") {
			values = append(values, line)
		}
		parts = append(parts, values)
	}
	data, _ := json.Marshal(parts)
	return data
}
