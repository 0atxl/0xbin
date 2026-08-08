package livecollab

import (
	"testing"
	"unicode/utf8"
)

func FuzzParseApplyMapChangeSets(f *testing.F) {
	f.Add("a🙂b", `[1,[2,"😀"],1]`, `[3,[0,"!"]]`, true)
	f.Add("hello", `[5,[0,"!"]]`, `[0,[0,"?"]]`, false)
	f.Add("", `[[0,"🙂"]]`, `[[0,"x"]]`, true)

	f.Fuzz(func(t *testing.T, document, firstJSON, secondJSON string, before bool) {
		if !utf8.ValidString(document) || len(document) > 4<<10 || len(firstJSON) > 8<<10 || len(secondJSON) > 8<<10 {
			return
		}
		first, err := ParseChangeSetJSON([]byte(firstJSON))
		if err != nil || first.ValidateForDocument(document) != nil {
			return
		}
		second, err := ParseChangeSetJSON([]byte(secondJSON))
		if err != nil || second.ValidateForDocument(document) != nil {
			return
		}
		if _, err := first.Apply(document); err != nil {
			t.Fatalf("valid first changeset did not apply: %v", err)
		}
		if _, err := second.Apply(document); err != nil {
			t.Fatalf("valid second changeset did not apply: %v", err)
		}
		mapped, err := first.Map(second, before)
		if err != nil {
			return
		}
		afterSecond, err := second.Apply(document)
		if err != nil {
			t.Fatalf("valid second changeset did not apply: %v", err)
		}
		if _, err := mapped.Apply(afterSecond); err != nil {
			t.Fatalf("mapped changeset did not apply: %v", err)
		}
		mappedSelection, err := (SelectionRange{Anchor: 0, Head: first.OldLen()}).Map(first)
		if err != nil {
			t.Fatalf("selection mapping failed: %v", err)
		}
		if mappedSelection.Anchor < 0 || mappedSelection.Head < 0 || mappedSelection.Anchor > first.NewLen() || mappedSelection.Head > first.NewLen() {
			t.Fatalf("mapped selection out of bounds: %+v", mappedSelection)
		}
	})
}
