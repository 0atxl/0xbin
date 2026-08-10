package live

import (
	"fmt"
	"testing"
	"time"
)

func TestRemovedSessionBookkeepingStaysBoundedAcrossRepeatedKicks(t *testing.T) {
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	room := &room{
		options:  HubOptions{ReconnectGrace: time.Minute},
		snapshot: RoomSnapshot{ExpiresAt: now.Add(24 * time.Hour)},
		participants: map[string]*participantState{
			"creator": {
				sessionID: "creator-session",
				connections: map[string]*connectionState{
					"creator-connection": {id: "creator-connection", generation: 1, lastSeenAt: now},
				},
				snapshot: ParticipantSnapshot{ID: "creator", Nickname: "Creator", Status: ParticipantConnected, LastSeenAt: now},
			},
		},
		sessions:        map[string]string{"creator-session": "creator"},
		removedSessions: make(map[string]time.Time),
		names:           map[string]string{"creator": "creator"},
	}

	for index := 0; index < maxRemovedSessionRecords+32; index++ {
		participantID := fmt.Sprintf("participant-%04d", index)
		sessionID := fmt.Sprintf("session-%04d", index)
		room.participants[participantID] = &participantState{
			sessionID: sessionID,
			connections: map[string]*connectionState{
				participantID: {id: participantID, generation: 1, lastSeenAt: now},
			},
			snapshot: ParticipantSnapshot{ID: participantID, Nickname: participantID, Status: ParticipantConnected, LastSeenAt: now},
		}
		room.sessions[sessionID] = participantID
		room.names[NameKey(participantID)] = participantID
		if err := room.removeParticipant("creator", "creator-connection", 1, true, participantID, now); err != nil {
			t.Fatalf("kick %d: %v", index, err)
		}
	}
	if len(room.removedSessions) != maxRemovedSessionRecords {
		t.Fatalf("removed sessions = %d, want %d", len(room.removedSessions), maxRemovedSessionRecords)
	}
	if _, ok := room.removedSessions["session-1055"]; !ok {
		t.Fatal("most recently removed session was not retained")
	}

	room.pruneRemovedSessionsLocked(now.Add(time.Minute + time.Nanosecond))
	if len(room.removedSessions) != 0 {
		t.Fatalf("expired removed sessions = %d, want 0", len(room.removedSessions))
	}
}
