package live

import (
	cryptorand "crypto/rand"
	"fmt"
	"io"
	"math/big"
	"strings"

	"github.com/0atxl/0xbin/wordlists"
)

const nameGenerationAttempts = 8

// NameGenerator creates readable adjective+noun names for active participants.
// Uniqueness is checked against the caller's active-room key set.
type NameGenerator struct {
	random     io.Reader
	adjectives []string
	nouns      []string
}

// NewNameGenerator constructs a generator with injectable randomness for
// deterministic tests.
func NewNameGenerator(random io.Reader, adjectives, nouns []string) (*NameGenerator, error) {
	if random == nil {
		return nil, fmt.Errorf("random source is required")
	}
	if err := validateWordList(adjectives, "adjective"); err != nil {
		return nil, err
	}
	if err := validateWordList(nouns, "noun"); err != nil {
		return nil, err
	}
	return &NameGenerator{
		random:     random,
		adjectives: append([]string(nil), adjectives...),
		nouns:      append([]string(nil), nouns...),
	}, nil
}

// NewDefaultNameGenerator uses the reviewed repository wordlists and
// cryptographically secure randomness.
func NewDefaultNameGenerator() *NameGenerator {
	generator, err := NewNameGenerator(cryptorand.Reader, wordlists.Adjectives(), wordlists.Nouns())
	if err != nil {
		panic("embedded word lists are invalid: " + err.Error())
	}
	return generator
}

// Generate returns an unused title-cased adjective+noun name. The used map is
// keyed by NameKey and is not modified; the room registry reserves the name
// after this method succeeds.
func (generator *NameGenerator) Generate(used map[string]struct{}) (string, error) {
	for range nameGenerationAttempts {
		adjective, err := generator.selectWord(generator.adjectives)
		if err != nil {
			return "", fmt.Errorf("select adjective: %w", err)
		}
		noun, err := generator.selectWord(generator.nouns)
		if err != nil {
			return "", fmt.Errorf("select noun: %w", err)
		}
		name := titleWord(adjective) + " " + titleWord(noun)
		if err := ValidateNickname(name); err != nil {
			return "", err
		}
		if _, exists := used[NameKey(name)]; !exists {
			return name, nil
		}
	}
	return "", fmt.Errorf("could not generate a unique participant name after %d attempts", nameGenerationAttempts)
}

func (generator *NameGenerator) selectWord(words []string) (string, error) {
	index, err := cryptorand.Int(generator.random, big.NewInt(int64(len(words))))
	if err != nil {
		return "", err
	}
	return words[index.Int64()], nil
}

func validateWordList(words []string, kind string) error {
	if len(words) == 0 {
		return fmt.Errorf("at least one %s is required", kind)
	}
	for _, word := range words {
		if word == "" {
			return fmt.Errorf("%s list contains an empty word", kind)
		}
		for _, character := range word {
			if character < 'a' || character > 'z' {
				return fmt.Errorf("%s %q must contain lowercase ASCII letters only", kind, word)
			}
		}
	}
	return nil
}

func titleWord(word string) string {
	return strings.ToUpper(word[:1]) + word[1:]
}
