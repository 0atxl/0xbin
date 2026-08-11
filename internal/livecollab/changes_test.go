package livecollab

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

type fixtureFile struct {
	Fixtures []fixture `json:"fixtures"`
}

type fixture struct {
	Name             string           `json:"name"`
	Document         string           `json:"document"`
	Updates          []fixtureUpdate  `json:"updates"`
	Selections       []SelectionRange `json:"selections"`
	ExpectedDocument string           `json:"expectedDocument"`
	MappedSelections []SelectionRange `json:"mappedSelections"`
}

type fixtureUpdate struct {
	ClientID string          `json:"clientID"`
	Changes  json.RawMessage `json:"changes"`
}

func loadFixtures(t *testing.T) []fixture {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locating fixture source directory")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(sourceFile), "..", "..", "tests", "livecollab", "fixtures.json"))
	if err != nil {
		t.Fatalf("read generated fixtures: %v", err)
	}
	var file fixtureFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("decode generated fixtures: %v", err)
	}
	if len(file.Fixtures) == 0 {
		t.Fatal("generated fixtures are empty")
	}
	return file.Fixtures
}

func mustParseChangeSet(t *testing.T, data string) ChangeSet {
	t.Helper()
	set, err := ParseChangeSetJSON([]byte(data))
	if err != nil {
		t.Fatalf("parse changeset %s: %v", data, err)
	}
	return set
}

func TestBrowserFixturesConvergeAndMapSelections(t *testing.T) {
	for _, fixture := range loadFixtures(t) {
		t.Run(fixture.Name, func(t *testing.T) {
			if len(fixture.Updates) != 2 {
				t.Fatalf("fixture has %d updates, want 2", len(fixture.Updates))
			}
			first, err := ParseChangeSetJSON(fixture.Updates[0].Changes)
			if err != nil {
				t.Fatal(err)
			}
			second, err := ParseChangeSetJSON(fixture.Updates[1].Changes)
			if err != nil {
				t.Fatal(err)
			}
			if err := first.ValidateForDocument(fixture.Document); err != nil {
				t.Fatalf("first changeset validation: %v", err)
			}
			if err := second.ValidateForDocument(fixture.Document); err != nil {
				t.Fatalf("second changeset validation: %v", err)
			}

			firstDocument, err := first.Apply(fixture.Document)
			if err != nil {
				t.Fatal(err)
			}
			secondDocument, err := second.Apply(fixture.Document)
			if err != nil {
				t.Fatal(err)
			}
			firstAfterSecond, err := first.Map(second, true)
			if err != nil {
				t.Fatalf("map first over second: %v", err)
			}
			firstThenSecondDocument, err := firstAfterSecond.Apply(secondDocument)
			if err != nil {
				t.Fatal(err)
			}
			secondAfterFirst, err := second.Map(first, false)
			if err != nil {
				t.Fatalf("map second over first: %v", err)
			}
			secondThenFirstDocument, err := secondAfterFirst.Apply(firstDocument)
			if err != nil {
				t.Fatal(err)
			}
			if firstThenSecondDocument != secondThenFirstDocument {
				t.Fatalf("convergence mismatch: %q != %q", firstThenSecondDocument, secondThenFirstDocument)
			}
			if firstThenSecondDocument != fixture.ExpectedDocument {
				t.Fatalf("browser expected %q, Go produced %q", fixture.ExpectedDocument, firstThenSecondDocument)
			}

			for index, selection := range fixture.Selections {
				mapped, err := selection.Map(first)
				if err != nil {
					t.Fatalf("map selection %d: %v", index, err)
				}
				if mapped != fixture.MappedSelections[index] {
					t.Fatalf("selection %d: browser=%+v Go=%+v", index, fixture.MappedSelections[index], mapped)
				}
			}

		})
	}
}

func TestParseChangeSetRejectsMalformedInput(t *testing.T) {
	malformed := []string{
		"",
		"null",
		"{}",
		`[1.5]`,
		`[-1]`,
		`[[1,1]]`,
		`[[1,"ok",2]]`,
		`[1] true`,
	}
	for _, input := range malformed {
		if _, err := ParseChangeSetJSON([]byte(input)); !errors.Is(err, ErrInvalidChangeSet) {
			t.Errorf("ParseChangeSetJSON(%q) error = %v, want ErrInvalidChangeSet", input, err)
		}
	}
}

func TestUTF16LengthsProtectUnicodeDocuments(t *testing.T) {
	changes := mustParseChangeSet(t, `[1,[2,"😀"],1]`)
	if changes.OldLen() != 4 || changes.NewLen() != 4 {
		t.Fatalf("UTF-16 lengths = old %d new %d, want 4 and 4", changes.OldLen(), changes.NewLen())
	}
	result, err := changes.Apply("a🙂b")
	if err != nil {
		t.Fatal(err)
	}
	if result != "a😀b" {
		t.Fatalf("Unicode apply result = %q, want %q", result, "a😀b")
	}
}

func TestInsertedByteLimitIsSeparateFromUTF16Length(t *testing.T) {
	changes := mustParseChangeSet(t, `[0,[0,"🙂"]]`)
	if changes.InsertedBytes() != len("🙂") {
		t.Fatalf("inserted bytes = %d, want %d", changes.InsertedBytes(), len("🙂"))
	}
	if err := changes.ValidateInsertedBytes(len("🙂") - 1); !errors.Is(err, ErrInvalidChangeSet) {
		t.Fatalf("under-limit validation error = %v, want ErrInvalidChangeSet", err)
	}
	if err := changes.ValidateInsertedBytes(len("🙂")); err != nil {
		t.Fatalf("at-limit validation error = %v", err)
	}
}
