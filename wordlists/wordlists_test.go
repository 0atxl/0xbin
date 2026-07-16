package wordlists

import (
	"fmt"
	"testing"
)

func TestReviewedLists(t *testing.T) {
	adjectives := Adjectives()
	nouns := Nouns()

	if got := len(adjectives); got != AdjectiveCount {
		t.Fatalf("adjective count = %d, want %d", got, AdjectiveCount)
	}
	if got := len(nouns); got != NounCount {
		t.Fatalf("noun count = %d, want %d", got, NounCount)
	}
	if got := len(adjectives) * len(adjectives) * len(nouns); got != CombinationCount {
		t.Fatalf("combination count = %d, want %d", got, CombinationCount)
	}

	validateWords(t, "adjective", adjectives)
	validateWords(t, "noun", nouns)
	validateUniqueSlugs(t, adjectives, nouns)
}

func validateWords(t *testing.T, kind string, words []string) {
	t.Helper()
	seen := make(map[string]struct{}, len(words))
	for index, word := range words {
		if word == "" {
			t.Errorf("%s %d is empty", kind, index)
			continue
		}
		for _, character := range word {
			if character < 'a' || character > 'z' {
				t.Errorf("%s %q contains invalid character %q", kind, word, character)
			}
		}
		if _, exists := seen[word]; exists {
			t.Errorf("duplicate %s %q", kind, word)
		}
		seen[word] = struct{}{}
	}
}

func validateUniqueSlugs(t *testing.T, adjectives, nouns []string) {
	t.Helper()
	seen := make(map[string]struct{}, CombinationCount)
	for _, first := range adjectives {
		for _, second := range adjectives {
			prefix := first + second
			for _, noun := range nouns {
				slug := prefix + noun
				if _, exists := seen[slug]; exists {
					t.Fatalf("duplicate resulting slug %q", slug)
				}
				seen[slug] = struct{}{}
			}
		}
	}
	if got := len(seen); got != CombinationCount {
		t.Fatal(fmt.Errorf("unique slug count = %d, want %d", got, CombinationCount))
	}
}
