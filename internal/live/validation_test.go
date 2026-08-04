package live

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestValidationAcceptsSettledLiveInputs(t *testing.T) {
	if err := ValidateNickname("Quiet Otter"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateNickname("नदी"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateTabName("main.go"); err != nil {
		t.Fatal(err)
	}
	for _, language := range SupportedLanguageIDs() {
		if err := ValidateLanguageID(language); err != nil {
			t.Fatalf("language %q: %v", language, err)
		}
	}
	if err := ValidateDocumentID("doc-01"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDocumentContent("package main\n", 100); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"join", "push_changes", "document_create", "document_update", "document_delete", "document_reorder", "presence", "participant_rename", "ack"} {
		if err := ValidateRoomOperation(operation); err != nil {
			t.Fatalf("operation %q: %v", operation, err)
		}
	}
	if err := ValidateDocumentOrder([]string{"doc-a", "doc-b"}, 8); err != nil {
		t.Fatal(err)
	}
}

func TestValidationRejectsUnsafeOrUnsupportedInputs(t *testing.T) {
	longName := strings.Repeat("a", MaxNicknameBytes+1)
	longTab := strings.Repeat("a", MaxTabNameBytes+1)
	longID := strings.Repeat("a", MaxDocumentIDBytes+1)
	tests := []struct {
		name string
		got  error
		want error
	}{
		{"empty nickname", ValidateNickname(""), ErrInvalidNickname},
		{"long nickname", ValidateNickname(longName), ErrInvalidNickname},
		{"surrounding nickname whitespace", ValidateNickname(" Quiet Otter"), ErrInvalidNickname},
		{"nickname control", ValidateNickname("Quiet\nOtter"), ErrInvalidNickname},
		{"long tab name", ValidateTabName(longTab), ErrInvalidTabName},
		{"unsupported language", ValidateLanguageID("ruby"), ErrInvalidLanguage},
		{"invalid document ID", ValidateDocumentID("../doc"), ErrInvalidDocumentID},
		{"long document ID", ValidateDocumentID(longID), ErrInvalidDocumentID},
		{"oversized content", ValidateDocumentContent("hello", 4), ErrInvalidContent},
		{"invalid content UTF-8", ValidateDocumentContent(string([]byte{0xff}), 10), ErrInvalidContent},
		{"unsupported operation", ValidateRoomOperation("unknown"), ErrInvalidOperation},
		{"duplicate document IDs", ValidateDocumentOrder([]string{"doc-a", "doc-a"}, 8), ErrInvalidOperation},
		{"empty document order", ValidateDocumentOrder(nil, 8), ErrInvalidOperation},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if !errors.Is(test.got, test.want) {
				t.Fatalf("error = %v, want %v", test.got, test.want)
			}
		})
	}
}

func TestNameGeneratorCreatesUniqueTitleCasedNames(t *testing.T) {
	generator, err := NewNameGenerator(bytes.NewReader(bytes.Repeat([]byte{0}, 16)), []string{"calm", "bright"}, []string{"otter", "fox"})
	if err != nil {
		t.Fatal(err)
	}
	name, err := generator.Generate(map[string]struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if name != "Calm Otter" {
		t.Fatalf("generated name = %q, want Calm Otter", name)
	}
	if _, err := generator.Generate(map[string]struct{}{NameKey(name): {}}); err == nil {
		t.Fatal("collision was not rejected after bounded retries")
	}
}

func TestNameGeneratorRejectsInvalidWordLists(t *testing.T) {
	tests := []struct {
		name       string
		adjectives []string
		nouns      []string
	}{
		{"empty adjectives", nil, []string{"otter"}},
		{"empty nouns", []string{"calm"}, nil},
		{"non-ascii adjective", []string{"cálm"}, []string{"otter"}},
		{"empty noun", []string{"calm"}, []string{""}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewNameGenerator(bytes.NewReader(nil), test.adjectives, test.nouns); err == nil {
				t.Fatal("NewNameGenerator() error = nil")
			}
		})
	}
}
