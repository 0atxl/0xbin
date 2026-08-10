package live_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/0atxl/0xbin/internal/live"
)

func TestParticipantIDIsStableAndRoomScopedWithoutExposingCredential(t *testing.T) {
	credential := "browser-credential-abcdefghijklmnopqrstuvwxyz012345"
	first, err := live.ParticipantIDForRoom("calmbrightotter", credential)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := live.ParticipantIDForRoom("calmbrightotter", credential)
	if err != nil {
		t.Fatal(err)
	}
	otherRoom, err := live.ParticipantIDForRoom("quietquickwren", credential)
	if err != nil {
		t.Fatal(err)
	}
	if first != repeated || first == otherRoom {
		t.Fatalf("participant IDs = first %q, repeated %q, other room %q", first, repeated, otherRoom)
	}
	if len(first) != 32 || strings.Contains(first, credential) || strings.Contains(credential, first) {
		t.Fatalf("public participant ID exposes or malformed credential: %q", first)
	}
}

func TestJoinIdentityRejectsMalformedIdentifiersAndNames(t *testing.T) {
	valid := live.JoinIdentity{
		ParticipantCredential: "browser-credential",
		ConnectionID:          "connection-one",
		ClientID:              "client-one",
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		mutate   func(*live.JoinIdentity)
		sentinel error
	}{
		{"empty participant credential", func(value *live.JoinIdentity) { value.ParticipantCredential = "" }, live.ErrInvalidParticipantCredential},
		{"long participant credential", func(value *live.JoinIdentity) {
			value.ParticipantCredential = strings.Repeat("a", live.MaxParticipantCredentialBytes+1)
		}, live.ErrInvalidParticipantCredential},
		{"participant control", func(value *live.JoinIdentity) { value.ParticipantCredential = "browser\ncredential" }, live.ErrInvalidParticipantCredential},
		{"participant unicode", func(value *live.JoinIdentity) { value.ParticipantCredential = "browser-世界" }, live.ErrInvalidParticipantCredential},
		{"empty connection", func(value *live.JoinIdentity) { value.ConnectionID = "" }, live.ErrInvalidConnectionID},
		{"long connection", func(value *live.JoinIdentity) { value.ConnectionID = strings.Repeat("c", live.MaxConnectionIDBytes+1) }, live.ErrInvalidConnectionID},
		{"connection whitespace", func(value *live.JoinIdentity) { value.ConnectionID = "connection one" }, live.ErrInvalidConnectionID},
		{"empty client", func(value *live.JoinIdentity) { value.ClientID = "" }, live.ErrInvalidClientID},
		{"long client", func(value *live.JoinIdentity) { value.ClientID = strings.Repeat("c", live.MaxClientIDBytes+1) }, live.ErrInvalidClientID},
		{"client control", func(value *live.JoinIdentity) { value.ClientID = "client\x00one" }, live.ErrInvalidClientID},
		{"empty preferred name", func(value *live.JoinIdentity) { value.PreferredNameSet = true }, live.ErrInvalidNickname},
		{"long preferred name", func(value *live.JoinIdentity) {
			value.PreferredNameSet = true
			value.PreferredName = strings.Repeat("n", live.MaxNicknameBytes+1)
		}, live.ErrInvalidNickname},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			if err := candidate.Validate(); !errors.Is(err, test.sentinel) {
				t.Fatalf("Validate() error = %v, want %v", err, test.sentinel)
			}
		})
	}
}
