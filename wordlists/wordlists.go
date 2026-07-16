// Package wordlists embeds 0xbin's reviewed slug vocabulary.
package wordlists

import (
	_ "embed"
	"strings"
)

const (
	// AdjectiveCount is the reviewed size of the version 1 adjective list.
	AdjectiveCount = 128
	// NounCount is the reviewed size of the version 1 noun list.
	NounCount = 128
	// CombinationCount is the number of adjective-adjective-noun combinations.
	CombinationCount = AdjectiveCount * AdjectiveCount * NounCount
)

var (
	//go:embed adjectives.txt
	adjectiveData string
	//go:embed nouns.txt
	nounData string

	adjectives = parse(adjectiveData)
	nouns      = parse(nounData)
)

// Adjectives returns a copy of the reviewed adjective list.
func Adjectives() []string { return append([]string(nil), adjectives...) }

// Nouns returns a copy of the reviewed noun list.
func Nouns() []string { return append([]string(nil), nouns...) }

func parse(data string) []string {
	return strings.Split(strings.TrimSuffix(data, "\n"), "\n")
}
