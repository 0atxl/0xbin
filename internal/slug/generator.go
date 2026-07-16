// Package slug generates and reserves 0xbin's three-word paste slugs.
package slug

import (
	"crypto/rand"
	"fmt"
	"io"
	"math/big"

	"github.com/0atxl/0xbin/wordlists"
)

// Generator selects two adjectives and one noun using an unbiased random
// source. The source is crypto/rand.Reader in production and may be injected
// for deterministic tests.
type Generator struct {
	random     io.Reader
	adjectives []string
	nouns      []string
}

// NewGenerator constructs a generator from an injected random source and word
// lists. Callers should normally use NewDefaultGenerator.
func NewGenerator(random io.Reader, adjectives, nouns []string) (*Generator, error) {
	if random == nil {
		return nil, fmt.Errorf("random source is required")
	}
	if len(adjectives) == 0 {
		return nil, fmt.Errorf("at least one adjective is required")
	}
	if len(nouns) == 0 {
		return nil, fmt.Errorf("at least one noun is required")
	}
	return &Generator{
		random:     random,
		adjectives: append([]string(nil), adjectives...),
		nouns:      append([]string(nil), nouns...),
	}, nil
}

// NewDefaultGenerator constructs a generator backed by crypto/rand and the
// repository's reviewed word lists.
func NewDefaultGenerator() *Generator {
	generator, err := NewGenerator(rand.Reader, wordlists.Adjectives(), wordlists.Nouns())
	if err != nil {
		panic("embedded word lists are invalid: " + err.Error())
	}
	return generator
}

// Generate returns one adjective-adjective-noun slug without separators.
func (g *Generator) Generate() (string, error) {
	first, err := g.selectWord(g.adjectives)
	if err != nil {
		return "", fmt.Errorf("select first adjective: %w", err)
	}
	second, err := g.selectWord(g.adjectives)
	if err != nil {
		return "", fmt.Errorf("select second adjective: %w", err)
	}
	noun, err := g.selectWord(g.nouns)
	if err != nil {
		return "", fmt.Errorf("select noun: %w", err)
	}
	return first + second + noun, nil
}

func (g *Generator) selectWord(words []string) (string, error) {
	index, err := rand.Int(g.random, big.NewInt(int64(len(words))))
	if err != nil {
		return "", err
	}
	return words[index.Int64()], nil
}
