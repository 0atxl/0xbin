package slug

import (
	"bytes"
	"io"
	"regexp"
	"testing"

	"github.com/0atxl/0xbin/wordlists"
)

func TestGeneratorUsesInjectedRandomSource(t *testing.T) {
	generator, err := NewGenerator(bytes.NewReader([]byte{0, 1, 2}), []string{"calm", "swift"}, []string{"otter", "wren", "fox"})
	if err != nil {
		t.Fatal(err)
	}

	got, err := generator.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if got != "calmswiftfox" {
		t.Fatalf("Generate() = %q, want %q", got, "calmswiftfox")
	}
}

func TestDefaultGeneratorProducesThreeWordSlugs(t *testing.T) {
	generator := NewDefaultGenerator()
	adjectives := membership(wordlists.Adjectives())
	nouns := membership(wordlists.Nouns())
	validSyntax := regexp.MustCompile(`^[a-z]+$`)

	for range 100 {
		generated, err := generator.Generate()
		if err != nil {
			t.Fatal(err)
		}
		if !validSyntax.MatchString(generated) {
			t.Fatalf("Generate() = %q, want lowercase ASCII letters", generated)
		}
		if !hasConstruction(generated, adjectives, nouns) {
			t.Fatalf("Generate() = %q, want adjective-adjective-noun construction", generated)
		}
	}
}

func TestGeneratorReportsRandomSourceFailure(t *testing.T) {
	generator, err := NewGenerator(failingReader{}, []string{"calm", "swift"}, []string{"otter", "wren"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := generator.Generate(); err == nil {
		t.Fatal("Generate() error = nil")
	}
}

func TestNewGeneratorValidatesDependencies(t *testing.T) {
	tests := []struct {
		name       string
		random     io.Reader
		adjectives []string
		nouns      []string
	}{
		{name: "nil random source", nouns: []string{"otter"}, adjectives: []string{"calm"}},
		{name: "empty adjectives", random: bytes.NewReader(nil), nouns: []string{"otter"}},
		{name: "empty nouns", random: bytes.NewReader(nil), adjectives: []string{"calm"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewGenerator(test.random, test.adjectives, test.nouns); err == nil {
				t.Fatal("NewGenerator() error = nil")
			}
		})
	}
}

func membership(words []string) map[string]struct{} {
	result := make(map[string]struct{}, len(words))
	for _, word := range words {
		result[word] = struct{}{}
	}
	return result
}

func hasConstruction(generated string, adjectives, nouns map[string]struct{}) bool {
	for first := range adjectives {
		if len(generated) <= len(first) || generated[:len(first)] != first {
			continue
		}
		remainder := generated[len(first):]
		for second := range adjectives {
			if len(remainder) <= len(second) || remainder[:len(second)] != second {
				continue
			}
			if _, ok := nouns[remainder[len(second):]]; ok {
				return true
			}
		}
	}
	return false
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
