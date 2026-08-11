// Package livecollab contains the Step 1 collaboration compatibility spike.
// It is intentionally independent from HTTP, SQLite, and the application
// room model. The package mirrors the small ChangeSet/ChangeDesc surface used
// by @codemirror/state and @codemirror/collab.
package livecollab

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf16"
)

var (
	ErrInvalidChangeSet   = errors.New("invalid CodeMirror changeset")
	ErrRevisionConflict   = errors.New("invalid or stale collaboration revision")
	ErrDuplicateOperation = errors.New("duplicate collaboration operation")
)

// Section is the internal representation of one CodeMirror ChangeSet section.
// Unchanged sections use NewLen == -1. Changed sections have NewLen >= 0 and
// carry the inserted UTF-8 string, including insertions with OldLen == 0.
type Section struct {
	OldLen int
	NewLen int
	Insert string
}

func (s Section) changed() bool { return s.NewLen >= 0 }

// ChangeSet is a JSON-compatible CodeMirror change set. Positions and lengths
// are UTF-16 code-unit offsets, matching JavaScript strings and CodeMirror.
type ChangeSet struct {
	Sections []Section
}

func (set ChangeSet) OldLen() int {
	length := 0
	for _, section := range set.Sections {
		length += section.OldLen
	}
	return length
}

func (set ChangeSet) NewLen() int {
	length := 0
	for _, section := range set.Sections {
		if section.changed() {
			length += section.NewLen
		} else {
			length += section.OldLen
		}
	}
	return length
}

func (set ChangeSet) Empty() bool {
	if len(set.Sections) == 0 {
		return true
	}
	for _, section := range set.Sections {
		if section.changed() {
			return false
		}
	}
	return true
}

func (set ChangeSet) InsertedBytes() int {
	bytes := 0
	for _, section := range set.Sections {
		if section.changed() {
			bytes += len(section.Insert)
		}
	}
	return bytes
}

// ValidateInsertedBytes is the small size guard used by the prototype. The
// room-specific limit and configuration wiring belong to the later live-room
// implementation, but the authority must have a byte-counted boundary rather
// than relying on UTF-16 lengths alone.
func (set ChangeSet) ValidateInsertedBytes(maxBytes int) error {
	if maxBytes < 0 {
		return fmt.Errorf("%w: negative inserted-byte limit", ErrInvalidChangeSet)
	}
	if set.InsertedBytes() > maxBytes {
		return fmt.Errorf("%w: inserted content is %d bytes, limit is %d", ErrInvalidChangeSet, set.InsertedBytes(), maxBytes)
	}
	return nil
}

// ParseChangeSetJSON parses ChangeSet.toJSON() output. A number represents an
// unchanged range. [n] deletes n UTF-16 code units. [n, ...lines] replaces n
// code units with the newline-joined inserted text.
func ParseChangeSetJSON(data []byte) (ChangeSet, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return ChangeSet{}, fmt.Errorf("%w: root must be an array", ErrInvalidChangeSet)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var parts []json.RawMessage
	if err := decoder.Decode(&parts); err != nil {
		return ChangeSet{}, fmt.Errorf("%w: %v", ErrInvalidChangeSet, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return ChangeSet{}, fmt.Errorf("%w: trailing JSON", ErrInvalidChangeSet)
	}

	set := ChangeSet{}
	for index, raw := range parts {
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 {
			return ChangeSet{}, fmt.Errorf("%w: empty section %d", ErrInvalidChangeSet, index)
		}
		if raw[0] != '[' {
			length, err := parseNonNegativeInt(raw)
			if err != nil {
				return ChangeSet{}, fmt.Errorf("%w: section %d: %v", ErrInvalidChangeSet, index, err)
			}
			if length > 0 {
				set.Sections = append(set.Sections, Section{OldLen: length, NewLen: -1})
			}
			continue
		}

		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil || len(values) == 0 {
			return ChangeSet{}, fmt.Errorf("%w: section %d is not an array", ErrInvalidChangeSet, index)
		}
		oldLen, err := parseNonNegativeInt(values[0])
		if err != nil {
			return ChangeSet{}, fmt.Errorf("%w: section %d old length: %v", ErrInvalidChangeSet, index, err)
		}
		lines := make([]string, 0, len(values)-1)
		for lineIndex, value := range values[1:] {
			var line string
			if err := json.Unmarshal(value, &line); err != nil {
				return ChangeSet{}, fmt.Errorf("%w: section %d line %d is not a string", ErrInvalidChangeSet, index, lineIndex)
			}
			lines = append(lines, line)
		}
		insert := strings.Join(lines, "\n")
		set.Sections = append(set.Sections, Section{
			OldLen: oldLen,
			NewLen: UTF16Len(insert),
			Insert: insert,
		})
	}
	return set, nil
}

func parseNonNegativeInt(raw []byte) (int, error) {
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return 0, errors.New("expected a JSON number")
	}
	value, err := number.Int64()
	if err != nil || value < 0 || value > int64(^uint(0)>>1) {
		return 0, errors.New("expected a non-negative integer")
	}
	return int(value), nil
}

func (set ChangeSet) ValidateForDocument(document string) error {
	if set.OldLen() != UTF16Len(document) {
		return fmt.Errorf("%w: changeset starts at %d, document has %d UTF-16 units", ErrInvalidChangeSet, set.OldLen(), UTF16Len(document))
	}
	for index, section := range set.Sections {
		if section.OldLen < 0 || section.NewLen < -1 {
			return fmt.Errorf("%w: negative length in section %d", ErrInvalidChangeSet, index)
		}
		if !section.changed() && section.Insert != "" {
			return fmt.Errorf("%w: unchanged section %d has inserted text", ErrInvalidChangeSet, index)
		}
		if section.changed() && section.NewLen != UTF16Len(section.Insert) {
			return fmt.Errorf("%w: inserted length mismatch in section %d", ErrInvalidChangeSet, index)
		}
	}
	return nil
}

// Apply applies a change set using UTF-16 positions and converts the result
// back to UTF-8. CodeMirror's document model uses JavaScript string positions,
// so byte slicing would corrupt emoji and other non-BMP characters.
func (set ChangeSet) Apply(document string) (string, error) {
	if set.OldLen() != UTF16Len(document) {
		return "", fmt.Errorf("%w: changeset starts at %d, document has %d UTF-16 units", ErrInvalidChangeSet, set.OldLen(), UTF16Len(document))
	}
	input := utf16.Encode([]rune(document))
	output := make([]uint16, 0, set.NewLen())
	position := 0
	for index, section := range set.Sections {
		end := position + section.OldLen
		if end > len(input) {
			return "", fmt.Errorf("%w: section %d exceeds document", ErrInvalidChangeSet, index)
		}
		if section.changed() {
			output = append(output, utf16.Encode([]rune(section.Insert))...)
		} else {
			output = append(output, input[position:end]...)
		}
		position = end
	}
	if position != len(input) {
		return "", fmt.Errorf("%w: sections do not cover document", ErrInvalidChangeSet)
	}
	return string(utf16.Decode(output)), nil
}

// Map returns this change set mapped over other, matching ChangeSet.map(other,
// before). Both sets must start in the same document. The result applies after
// other has been applied.
func (set ChangeSet) Map(other ChangeSet, before bool) (ChangeSet, error) {
	if set.OldLen() != other.OldLen() {
		return ChangeSet{}, fmt.Errorf("%w: cannot map lengths %d and %d", ErrInvalidChangeSet, set.OldLen(), other.OldLen())
	}
	a, b := newSectionIter(set), newSectionIter(other)
	result := ChangeSet{}
	inserted := -1

	for {
		if (a.done() && b.oldLength() > 0) || (b.done() && a.oldLength() > 0) {
			return ChangeSet{}, fmt.Errorf("%w: mismatched change lengths", ErrInvalidChangeSet)
		}
		if a.ins() == -1 && b.ins() == -1 {
			length := min(a.oldLength(), b.oldLength())
			addSection(&result, length, -1, "", false)
			a.forward(length)
			b.forward(length)
		} else if b.ins() >= 0 && (a.ins() < 0 || inserted == a.index || a.offset == 0 && (b.oldLength() < a.oldLength() || b.oldLength() == a.oldLength() && !before)) {
			length := b.oldLength()
			addSection(&result, b.newLength(), -1, "", false)
			for length > 0 {
				piece := min(a.oldLength(), length)
				if a.ins() >= 0 && inserted < a.index && a.oldLength() <= piece {
					addSection(&result, 0, a.newLength(), a.text(), false)
					inserted = a.index
				}
				a.forward(piece)
				length -= piece
			}
			b.next()
		} else if a.ins() >= 0 {
			length, left := 0, a.oldLength()
			for left > 0 {
				switch {
				case b.ins() == -1:
					piece := min(left, b.oldLength())
					length += piece
					left -= piece
					b.forward(piece)
				case b.ins() == 0 && b.oldLength() < left:
					left -= b.oldLength()
					b.next()
				default:
					goto processA
				}
			}
		processA:
			if inserted < a.index {
				addSection(&result, length, a.newLength(), a.text(), false)
			} else {
				addSection(&result, length, 0, "", false)
			}
			inserted = a.index
			a.forward(a.oldLength() - left)
		} else if a.done() && b.done() {
			return result, nil
		} else {
			return ChangeSet{}, fmt.Errorf("%w: mismatched change sets", ErrInvalidChangeSet)
		}
	}
}

func addSection(set *ChangeSet, oldLength, newLength int, insert string, forceJoin bool) {
	if oldLength == 0 && newLength <= 0 {
		return
	}
	last := len(set.Sections) - 1
	if last >= 0 && newLength <= 0 && newLength == set.Sections[last].NewLen {
		set.Sections[last].OldLen += oldLength
		return
	}
	if last >= 0 && oldLength == 0 && set.Sections[last].OldLen == 0 {
		set.Sections[last].NewLen += newLength
		set.Sections[last].Insert += insert
		return
	}
	if forceJoin && last >= 0 {
		set.Sections[last].OldLen += oldLength
		set.Sections[last].NewLen += newLength
		set.Sections[last].Insert += insert
		return
	}
	set.Sections = append(set.Sections, Section{OldLen: oldLength, NewLen: newLength, Insert: insert})
}

type sectionIter struct {
	set    ChangeSet
	index  int
	old    int
	new    int
	offset int
}

func newSectionIter(set ChangeSet) *sectionIter {
	iterator := &sectionIter{set: set}
	iterator.next()
	return iterator
}

func (iterator *sectionIter) next() {
	if iterator.index >= len(iterator.set.Sections) {
		iterator.old, iterator.new, iterator.offset = 0, -2, 0
		return
	}
	section := iterator.set.Sections[iterator.index]
	iterator.index++
	iterator.old, iterator.new, iterator.offset = section.OldLen, section.NewLen, 0
}

func (iterator *sectionIter) done() bool { return iterator.new == -2 }
func (iterator *sectionIter) ins() int   { return iterator.new }

func (iterator *sectionIter) oldLength() int { return iterator.old }
func (iterator *sectionIter) newLength() int {
	if iterator.new < 0 {
		return iterator.old
	}
	return iterator.new
}

func (iterator *sectionIter) text() string {
	if iterator.done() || iterator.new < 0 {
		return ""
	}
	return iterator.set.Sections[iterator.index-1].Insert
}

func (iterator *sectionIter) forward(length int) {
	if length == iterator.old {
		iterator.next()
		return
	}
	iterator.old -= length
	iterator.offset += length
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}

// MapPos mirrors ChangeDesc.mapPos for the normal Simple mapping mode.
func (set ChangeSet) MapPos(position, assoc int) (int, error) {
	if position < 0 || position > set.OldLen() {
		return 0, fmt.Errorf("position %d is out of range for document length %d", position, set.OldLen())
	}
	positionA, positionB := 0, 0
	for _, section := range set.Sections {
		endA := positionA + section.OldLen
		if !section.changed() {
			if endA > position {
				return positionB + position - positionA, nil
			}
			positionB += section.OldLen
		} else {
			if endA > position || endA == position && assoc < 0 && section.OldLen == 0 {
				if position == positionA || assoc < 0 {
					return positionB, nil
				}
				return positionB + section.NewLen, nil
			}
			positionB += section.NewLen
		}
		positionA = endA
	}
	return positionB, nil
}

type SelectionRange struct {
	Anchor int `json:"anchor"`
	Head   int `json:"head"`
}

func (selection SelectionRange) Map(changes ChangeSet) (SelectionRange, error) {
	if selection.Anchor == selection.Head {
		position, err := changes.MapPos(selection.Anchor, -1)
		if err != nil {
			return SelectionRange{}, err
		}
		return SelectionRange{Anchor: position, Head: position}, nil
	}
	from, err := changes.MapPos(min(selection.Anchor, selection.Head), 1)
	if err != nil {
		return SelectionRange{}, err
	}
	to, err := changes.MapPos(max(selection.Anchor, selection.Head), -1)
	if err != nil {
		return SelectionRange{}, err
	}
	if selection.Anchor <= selection.Head {
		return SelectionRange{Anchor: from, Head: to}, nil
	}
	return SelectionRange{Anchor: to, Head: from}, nil
}

func max(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func UTF16Len(value string) int { return len(utf16.Encode([]rune(value))) }
