package sqlite

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/0atxl/0xbin/internal/live"
	sqliteDriver "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

func (s *Store) CreateRoom(ctx context.Context, snapshot live.RoomSnapshot) error {
	normalized, err := prepareRoomSnapshot(snapshot)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin live room creation: %w", err)
	}
	if err := insertRoom(ctx, tx, normalized); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := insertDocuments(ctx, tx, normalized); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit live room creation: %w", err)
	}
	return nil
}

func (s *Store) GetRoomSnapshot(ctx context.Context, slug string, now time.Time) (live.RoomSnapshot, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return live.RoomSnapshot{}, fmt.Errorf("begin live room read: %w", err)
	}
	defer tx.Rollback()
	snapshot, err := getRoomSnapshot(ctx, tx, slug, now)
	if err != nil {
		return live.RoomSnapshot{}, err
	}
	if err := tx.Commit(); err != nil {
		return live.RoomSnapshot{}, fmt.Errorf("commit live room read: %w", err)
	}
	return snapshot, nil
}

func (s *Store) GetRoomSnapshotWithClientOperations(ctx context.Context, slug, clientID string, limit int, now time.Time) (live.RoomSnapshot, []live.OperationRecord, error) {
	if clientID == "" || strings.TrimSpace(clientID) != clientID || len(clientID) > 128 || !utf8.ValidString(clientID) || limit < 1 {
		return live.RoomSnapshot{}, nil, live.ErrInvalidChange
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return live.RoomSnapshot{}, nil, fmt.Errorf("begin live reconciliation read: %w", err)
	}
	defer tx.Rollback()
	snapshot, err := getRoomSnapshot(ctx, tx, slug, now)
	if err != nil {
		return live.RoomSnapshot{}, nil, err
	}
	operations, err := queryOperations(ctx, tx, slug, clientID, limit, now)
	if err != nil {
		return live.RoomSnapshot{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return live.RoomSnapshot{}, nil, fmt.Errorf("commit live reconciliation read: %w", err)
	}
	return snapshot, operations, nil
}

func getRoomSnapshot(ctx context.Context, tx *sql.Tx, slug string, now time.Time) (live.RoomSnapshot, error) {
	var snapshot live.RoomSnapshot
	var passwordHash sql.NullString
	var expiresAt, createdAt int64
	if err := tx.QueryRowContext(ctx, `
		SELECT slug, password_hash, content_size, metadata_revision,
		       metadata_snapshot_revision, expires_at, created_at
		FROM live_rooms
		WHERE slug = ? AND expires_at > ?`, slug, unixSeconds(now)).Scan(
		&snapshot.Slug, &passwordHash, &snapshot.ContentSize,
		&snapshot.MetadataRevision, &snapshot.MetadataSnapshotRevision,
		&expiresAt, &createdAt,
	); errors.Is(err, sql.ErrNoRows) {
		return live.RoomSnapshot{}, live.ErrRoomNotFound
	} else if err != nil {
		return live.RoomSnapshot{}, fmt.Errorf("get live room: %w", err)
	}
	if passwordHash.Valid {
		snapshot.PasswordHash = passwordHash.String
	}
	snapshot.ExpiresAt = unixTime(expiresAt)
	snapshot.CreatedAt = unixTime(createdAt)

	rows, err := tx.QueryContext(ctx, `
		SELECT document_id, name, language, content, position,
		       current_revision, snapshot_revision, updated_at
		FROM live_documents
		WHERE room_slug = ?
		ORDER BY position`, slug)
	if err != nil {
		return live.RoomSnapshot{}, fmt.Errorf("list live room documents: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var document live.DocumentSnapshot
		var updatedAt int64
		if err := rows.Scan(&document.ID, &document.Name, &document.Language,
			&document.Content, &document.Position, &document.CurrentRevision,
			&document.SnapshotRevision, &updatedAt); err != nil {
			return live.RoomSnapshot{}, fmt.Errorf("scan live room document: %w", err)
		}
		document.UpdatedAt = unixTime(updatedAt)
		snapshot.Documents = append(snapshot.Documents, document)
	}
	if err := rows.Err(); err != nil {
		return live.RoomSnapshot{}, fmt.Errorf("read live room documents: %w", err)
	}
	if err := rows.Close(); err != nil {
		return live.RoomSnapshot{}, fmt.Errorf("close live room documents: %w", err)
	}
	return snapshot, nil
}

func (s *Store) SaveSnapshot(ctx context.Context, snapshot live.RoomSnapshot, now time.Time) error {
	normalized, err := prepareRoomSnapshot(snapshot)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin live room snapshot: %w", err)
	}
	var metadataRevision, metadataSnapshotRevision int
	var expiresAt int64
	if err := tx.QueryRowContext(ctx, `
		SELECT metadata_revision, metadata_snapshot_revision, expires_at
		FROM live_rooms
		WHERE slug = ? AND expires_at > ?`, normalized.Slug, unixSeconds(now)).Scan(
		&metadataRevision, &metadataSnapshotRevision, &expiresAt,
	); errors.Is(err, sql.ErrNoRows) {
		_ = tx.Rollback()
		return live.ErrRoomNotFound
	} else if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("read live room snapshot revision: %w", err)
	}
	if metadataRevision != normalized.MetadataRevision || metadataSnapshotRevision > normalized.MetadataSnapshotRevision {
		_ = tx.Rollback()
		return live.ErrRevisionConflict
	}
	if normalized.ExpiresAt.Unix() != expiresAt {
		_ = tx.Rollback()
		return live.ErrRevisionConflict
	}
	for _, document := range normalized.Documents {
		var currentRevision, snapshotRevision int
		if err := tx.QueryRowContext(ctx, `
			SELECT current_revision, snapshot_revision FROM live_documents
			WHERE room_slug = ? AND document_id = ?`, normalized.Slug, document.ID).Scan(&currentRevision, &snapshotRevision); errors.Is(err, sql.ErrNoRows) {
			// A metadata create can introduce a document in the same full
			// snapshot transaction. There is no prior row to compare.
			continue
		} else if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("read live document revision: %w", err)
		}
		if currentRevision != document.CurrentRevision || snapshotRevision > document.SnapshotRevision {
			_ = tx.Rollback()
			return live.ErrRevisionConflict
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE live_rooms
		SET content_size = ?, metadata_revision = ?, metadata_snapshot_revision = ?
		WHERE slug = ? AND expires_at > ?`, normalized.ContentSize,
		normalized.MetadataRevision, normalized.MetadataSnapshotRevision,
		normalized.Slug, unixSeconds(now)); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("update live room snapshot: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM live_documents WHERE room_slug = ?`, normalized.Slug); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("replace live room documents: %w", err)
	}
	if err := insertDocuments(ctx, tx, normalized); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit live room snapshot: %w", err)
	}
	return nil
}

// CommitChange is the live-edit hot path. It commits the change record and
// corresponding current snapshot together, while updating only one document
// for document edits. Structural metadata edits synchronize document rows but
// remain outside the per-keystroke path.
func (s *Store) CommitChange(ctx context.Context, commit live.ChangeCommit, now time.Time) error {
	snapshot, err := prepareRoomSnapshot(commit.Snapshot)
	if err != nil {
		return err
	}
	change, err := prepareChange(commit.Change, now)
	if err != nil || commit.CompactThrough < 0 || commit.CompactThrough > commit.Change.Revision || commit.RetainOperations < 1 {
		return live.ErrInvalidChange
	}
	operation, err := prepareOperation(commit.Operation, change, now)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin live change commit: %w", err)
	}
	rollback := func(err error) error {
		_ = tx.Rollback()
		return err
	}

	var metadataRevision int
	var expiresAt int64
	if err := tx.QueryRowContext(ctx, `
		SELECT metadata_revision, expires_at FROM live_rooms
		WHERE slug = ? AND expires_at > ?`, snapshot.Slug, unixSeconds(now)).Scan(&metadataRevision, &expiresAt); errors.Is(err, sql.ErrNoRows) {
		return rollback(live.ErrRoomNotFound)
	} else if err != nil {
		return rollback(fmt.Errorf("read live change commit state: %w", err))
	}
	if expiresAt != snapshot.ExpiresAt.Unix() {
		return rollback(live.ErrRevisionConflict)
	}
	if err := insertOperation(ctx, tx, snapshot.Slug, operation); err != nil {
		return rollback(err)
	}

	switch change.StreamKind {
	case live.StreamDocument:
		var document *live.DocumentSnapshot
		for i := range snapshot.Documents {
			if snapshot.Documents[i].ID == change.StreamID {
				document = &snapshot.Documents[i]
				break
			}
		}
		if document == nil || document.CurrentRevision != change.Revision || document.SnapshotRevision != change.Revision || metadataRevision != snapshot.MetadataRevision {
			return rollback(live.ErrRevisionConflict)
		}
		var currentRevision int
		if err := tx.QueryRowContext(ctx, `SELECT current_revision FROM live_documents WHERE room_slug = ? AND document_id = ?`, snapshot.Slug, document.ID).Scan(&currentRevision); errors.Is(err, sql.ErrNoRows) {
			return rollback(live.ErrRoomNotFound)
		} else if err != nil {
			return rollback(fmt.Errorf("read live document commit revision: %w", err))
		}
		if currentRevision+1 != change.Revision {
			return rollback(live.ErrRevisionConflict)
		}
		if err := insertChange(ctx, tx, snapshot.Slug, change); err != nil {
			return rollback(err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE live_rooms SET content_size = ? WHERE slug = ? AND expires_at > ?`, snapshot.ContentSize, snapshot.Slug, unixSeconds(now)); err != nil {
			return rollback(fmt.Errorf("update live room content size: %w", err))
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE live_documents
			SET name = ?, language = ?, content = ?, position = ?,
			    current_revision = ?, snapshot_revision = ?, updated_at = ?
			WHERE room_slug = ? AND document_id = ?`, document.Name, document.Language,
			document.Content, document.Position, document.CurrentRevision,
			document.SnapshotRevision, unixSeconds(document.UpdatedAt), snapshot.Slug,
			document.ID); err != nil {
			return rollback(fmt.Errorf("update live document snapshot: %w", err))
		}
	case live.StreamMetadata:
		if change.Revision != snapshot.MetadataRevision || snapshot.MetadataSnapshotRevision != change.Revision || metadataRevision+1 != change.Revision {
			return rollback(live.ErrRevisionConflict)
		}
		if err := insertChange(ctx, tx, snapshot.Slug, change); err != nil {
			return rollback(err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE live_rooms
			SET content_size = ?, metadata_revision = ?, metadata_snapshot_revision = ?
			WHERE slug = ? AND expires_at > ?`, snapshot.ContentSize,
			snapshot.MetadataRevision, snapshot.MetadataSnapshotRevision,
			snapshot.Slug, unixSeconds(now)); err != nil {
			return rollback(fmt.Errorf("update live metadata snapshot: %w", err))
		}
		if err := syncDocuments(ctx, tx, snapshot); err != nil {
			return rollback(err)
		}
	default:
		return rollback(live.ErrInvalidChange)
	}

	if commit.CompactThrough > 0 {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM live_changes
			WHERE room_slug = ? AND stream_kind = ? AND stream_id = ? AND revision <= ?`,
			snapshot.Slug, change.StreamKind, change.StreamID, commit.CompactThrough); err != nil {
			return rollback(fmt.Errorf("compact committed live changes: %w", err))
		}
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM live_operations
		WHERE room_slug = ? AND sequence NOT IN (
			SELECT sequence FROM live_operations
			WHERE room_slug = ?
			ORDER BY sequence DESC
			LIMIT ?
		)`, snapshot.Slug, snapshot.Slug, commit.RetainOperations); err != nil {
		return rollback(fmt.Errorf("prune live operation ledger: %w", err))
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit live change and snapshot: %w", err)
	}
	return nil
}

func insertOperation(ctx context.Context, tx *sql.Tx, slug string, operation live.OperationRecord) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO live_operations(
			room_slug, operation_id, client_id, fingerprint, stream_kind,
			stream_id, base_revision, revision, operation_kind,
			result_payload, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, slug,
		operation.OperationID, operation.ClientID, operation.Fingerprint,
		operation.StreamKind, operation.StreamID, operation.BaseRevision,
		operation.Revision, operation.OperationKind, operation.ResultPayload,
		unixSeconds(operation.CreatedAt)); err != nil {
		return fmt.Errorf("insert live operation: %w", err)
	}
	return nil
}

func insertChange(ctx context.Context, tx *sql.Tx, slug string, change live.ChangeRecord) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO live_changes(room_slug, stream_kind, stream_id, revision,
		                         change_kind, payload, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, slug, change.StreamKind, change.StreamID,
		change.Revision, change.Kind, change.Payload, unixSeconds(change.CreatedAt)); err != nil {
		if isSQLitePrimaryKey(err) {
			return live.ErrRevisionConflict
		}
		return fmt.Errorf("append live change: %w", err)
	}
	return nil
}

func syncDocuments(ctx context.Context, tx *sql.Tx, snapshot live.RoomSnapshot) error {
	rows, err := tx.QueryContext(ctx, `SELECT document_id FROM live_documents WHERE room_slug = ?`, snapshot.Slug)
	if err != nil {
		return fmt.Errorf("list live documents for synchronization: %w", err)
	}
	existing := make(map[string]struct{})
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan live document for synchronization: %w", err)
		}
		existing[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("read live document synchronization rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close live document synchronization rows: %w", err)
	}
	for _, document := range snapshot.Documents {
		if _, ok := existing[document.ID]; ok {
			if _, err := tx.ExecContext(ctx, `
				UPDATE live_documents
				SET name = ?, language = ?, content = ?, position = ?, current_revision = ?, snapshot_revision = ?, updated_at = ?
				WHERE room_slug = ? AND document_id = ?`, document.Name, document.Language,
				document.Content, document.Position, document.CurrentRevision,
				document.SnapshotRevision, unixSeconds(document.UpdatedAt), snapshot.Slug,
				document.ID); err != nil {
				return fmt.Errorf("update live document %q: %w", document.ID, err)
			}
			delete(existing, document.ID)
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO live_documents(room_slug, document_id, name, language, content,
			                           position, current_revision, snapshot_revision, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, snapshot.Slug, document.ID,
			document.Name, document.Language, document.Content, document.Position,
			document.CurrentRevision, document.SnapshotRevision, unixSeconds(document.UpdatedAt)); err != nil {
			return fmt.Errorf("insert synchronized live document %q: %w", document.ID, err)
		}
	}
	for id := range existing {
		if _, err := tx.ExecContext(ctx, `DELETE FROM live_documents WHERE room_slug = ? AND document_id = ?`, snapshot.Slug, id); err != nil {
			return fmt.Errorf("delete synchronized live document %q: %w", id, err)
		}
	}
	return nil
}

func (s *Store) AppendChanges(ctx context.Context, slug string, changes []live.ChangeRecord, now time.Time) error {
	if len(changes) == 0 {
		return live.ErrInvalidChange
	}
	normalized := make([]live.ChangeRecord, len(changes))
	for i, change := range changes {
		var err error
		normalized[i], err = prepareChange(change, now)
		if err != nil {
			return err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin live changes: %w", err)
	}
	var metadataRevision int
	if err := tx.QueryRowContext(ctx, `
		SELECT metadata_revision FROM live_rooms
		WHERE slug = ? AND expires_at > ?`, slug, unixSeconds(now)).Scan(&metadataRevision); errors.Is(err, sql.ErrNoRows) {
		_ = tx.Rollback()
		return live.ErrRoomNotFound
	} else if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("read live room revision: %w", err)
	}
	current := make(map[string]int)
	current[live.StreamMetadata+"\x00"+live.MetadataStreamID] = metadataRevision
	for _, change := range normalized {
		key := change.StreamKind + "\x00" + change.StreamID
		if _, ok := current[key]; ok {
			continue
		}
		if change.StreamKind != live.StreamDocument {
			_ = tx.Rollback()
			return live.ErrInvalidChange
		}
		var revision int
		if err := tx.QueryRowContext(ctx, `
			SELECT current_revision FROM live_documents
			WHERE room_slug = ? AND document_id = ?`, slug, change.StreamID).Scan(&revision); errors.Is(err, sql.ErrNoRows) {
			_ = tx.Rollback()
			return live.ErrRoomNotFound
		} else if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("read live document revision: %w", err)
		}
		current[key] = revision
	}
	for _, change := range normalized {
		key := change.StreamKind + "\x00" + change.StreamID
		want := current[key] + 1
		if change.Revision != want {
			_ = tx.Rollback()
			return live.ErrRevisionConflict
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO live_changes(room_slug, stream_kind, stream_id, revision,
			                         change_kind, payload, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, slug, change.StreamKind, change.StreamID,
			change.Revision, change.Kind, change.Payload, unixSeconds(change.CreatedAt)); err != nil {
			_ = tx.Rollback()
			if isSQLitePrimaryKey(err) {
				return live.ErrRevisionConflict
			}
			return fmt.Errorf("append live change: %w", err)
		}
		current[key] = change.Revision
	}
	metadataRevision = current[live.StreamMetadata+"\x00"+live.MetadataStreamID]
	if _, err := tx.ExecContext(ctx, `
		UPDATE live_rooms SET metadata_revision = ?
		WHERE slug = ? AND expires_at > ?`, metadataRevision, slug, unixSeconds(now)); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("update live metadata revision: %w", err)
	}
	updatedDocuments := make(map[string]int)
	for _, change := range normalized {
		if change.StreamKind == live.StreamDocument {
			updatedDocuments[change.StreamID] = current[change.StreamKind+"\x00"+change.StreamID]
		}
	}
	for documentID, revision := range updatedDocuments {
		if _, err := tx.ExecContext(ctx, `
			UPDATE live_documents SET current_revision = ?
			WHERE room_slug = ? AND document_id = ?`, revision, slug, documentID); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("update live document revision: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit live changes: %w", err)
	}
	return nil
}

func (s *Store) LoadChangesSince(ctx context.Context, slug, streamKind, streamID string, since int, now time.Time) ([]live.ChangeRecord, error) {
	if err := validateStream(streamKind, streamID); err != nil {
		return nil, err
	}
	if since < 0 {
		return nil, live.ErrInvalidChange
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin live history read: %w", err)
	}
	defer tx.Rollback()
	var currentRevision int
	if streamKind == live.StreamMetadata {
		if err := tx.QueryRowContext(ctx, `
			SELECT metadata_revision FROM live_rooms
			WHERE slug = ? AND expires_at > ?`, slug, unixSeconds(now)).Scan(&currentRevision); errors.Is(err, sql.ErrNoRows) {
			return nil, live.ErrRoomNotFound
		} else if err != nil {
			return nil, fmt.Errorf("read live metadata history: %w", err)
		}
	} else if err := tx.QueryRowContext(ctx, `
		SELECT d.current_revision
		FROM live_documents AS d
		JOIN live_rooms AS r ON r.slug = d.room_slug
		WHERE d.room_slug = ? AND d.document_id = ? AND r.expires_at > ?`, slug, streamID, unixSeconds(now)).Scan(&currentRevision); errors.Is(err, sql.ErrNoRows) {
		return nil, live.ErrRoomNotFound
	} else if err != nil {
		return nil, fmt.Errorf("read live document history: %w", err)
	}
	minimumRevision := currentRevision + 1
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MIN(revision), ?)
		FROM live_changes
		WHERE room_slug = ? AND stream_kind = ? AND stream_id = ?`,
		minimumRevision, slug, streamKind, streamID).Scan(&minimumRevision); err != nil {
		return nil, fmt.Errorf("read live history boundary: %w", err)
	}
	if since < minimumRevision-1 {
		return nil, live.ErrHistoryCompacted
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT stream_kind, stream_id, revision, change_kind, payload, created_at
		FROM live_changes
		WHERE room_slug = ? AND stream_kind = ? AND stream_id = ? AND revision > ?
		ORDER BY revision`, slug, streamKind, streamID, since)
	if err != nil {
		return nil, fmt.Errorf("load live changes: %w", err)
	}
	defer rows.Close()
	var result []live.ChangeRecord
	for rows.Next() {
		var change live.ChangeRecord
		var createdAt int64
		if err := rows.Scan(&change.StreamKind, &change.StreamID, &change.Revision,
			&change.Kind, &change.Payload, &createdAt); err != nil {
			return nil, fmt.Errorf("scan live change: %w", err)
		}
		change.CreatedAt = unixTime(createdAt)
		result = append(result, change)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read live changes: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close live changes: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit live history read: %w", err)
	}
	return result, nil
}

func (s *Store) LoadRecentOperations(ctx context.Context, slug string, limit int, now time.Time) ([]live.OperationRecord, error) {
	return s.loadOperations(ctx, slug, "", limit, now)
}

func (s *Store) LoadClientOperations(ctx context.Context, slug, clientID string, limit int, now time.Time) ([]live.OperationRecord, error) {
	if clientID == "" || strings.TrimSpace(clientID) != clientID || len(clientID) > 128 || !utf8.ValidString(clientID) {
		return nil, live.ErrInvalidChange
	}
	return s.loadOperations(ctx, slug, clientID, limit, now)
}

func (s *Store) loadOperations(ctx context.Context, slug, clientID string, limit int, now time.Time) ([]live.OperationRecord, error) {
	if slug == "" || strings.TrimSpace(slug) != slug || limit < 1 {
		return nil, live.ErrInvalidChange
	}
	operations, err := queryOperations(ctx, s.db, slug, clientID, limit, now)
	if err != nil {
		return nil, err
	}
	if len(operations) == 0 {
		var exists int
		if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM live_rooms WHERE slug = ? AND expires_at > ?`, slug, unixSeconds(now)).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
			return nil, live.ErrRoomNotFound
		} else if err != nil {
			return nil, fmt.Errorf("check live operation room: %w", err)
		}
	}
	return operations, nil
}

type operationQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func queryOperations(ctx context.Context, queryer operationQueryer, slug, clientID string, limit int, now time.Time) ([]live.OperationRecord, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if clientID == "" {
		rows, err = queryer.QueryContext(ctx, `
			SELECT o.operation_id, o.client_id, o.fingerprint, o.stream_kind,
			       o.stream_id, o.base_revision, o.revision, o.operation_kind,
			       o.result_payload, o.created_at
			FROM live_operations AS o
			JOIN live_rooms AS r ON r.slug = o.room_slug
			WHERE o.room_slug = ? AND r.expires_at > ?
			ORDER BY o.sequence DESC
			LIMIT ?`, slug, unixSeconds(now), limit)
	} else {
		rows, err = queryer.QueryContext(ctx, `
			SELECT o.operation_id, o.client_id, o.fingerprint, o.stream_kind,
			       o.stream_id, o.base_revision, o.revision, o.operation_kind,
			       o.result_payload, o.created_at
			FROM live_operations AS o
			JOIN live_rooms AS r ON r.slug = o.room_slug
			WHERE o.room_slug = ? AND o.client_id = ? AND r.expires_at > ?
			ORDER BY o.sequence DESC
			LIMIT ?`, slug, clientID, unixSeconds(now), limit)
	}
	if err != nil {
		return nil, fmt.Errorf("load live operations: %w", err)
	}
	defer rows.Close()
	operations := make([]live.OperationRecord, 0)
	for rows.Next() {
		var operation live.OperationRecord
		var createdAt int64
		if err := rows.Scan(&operation.OperationID, &operation.ClientID,
			&operation.Fingerprint, &operation.StreamKind, &operation.StreamID,
			&operation.BaseRevision, &operation.Revision, &operation.OperationKind,
			&operation.ResultPayload, &createdAt); err != nil {
			return nil, fmt.Errorf("scan live operation: %w", err)
		}
		operation.CreatedAt = unixTime(createdAt)
		operations = append(operations, operation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read live operations: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close live operations: %w", err)
	}
	for left, right := 0, len(operations)-1; left < right; left, right = left+1, right-1 {
		operations[left], operations[right] = operations[right], operations[left]
	}
	return operations, nil
}

func (s *Store) CompactChanges(ctx context.Context, slug, streamKind, streamID string, throughRevision int, now time.Time) error {
	if err := validateStream(streamKind, streamID); err != nil {
		return err
	}
	if throughRevision < 0 {
		return live.ErrInvalidChange
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin live change compaction: %w", err)
	}
	var currentRevision, snapshotRevision int
	if streamKind == live.StreamMetadata {
		if err := tx.QueryRowContext(ctx, `
			SELECT metadata_revision, metadata_snapshot_revision
			FROM live_rooms WHERE slug = ? AND expires_at > ?`, slug, unixSeconds(now)).Scan(&currentRevision, &snapshotRevision); errors.Is(err, sql.ErrNoRows) {
			_ = tx.Rollback()
			return live.ErrRoomNotFound
		} else if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("read live metadata compaction state: %w", err)
		}
	} else if err := tx.QueryRowContext(ctx, `
		SELECT d.current_revision, d.snapshot_revision
		FROM live_documents AS d
		JOIN live_rooms AS r ON r.slug = d.room_slug
		WHERE d.room_slug = ? AND d.document_id = ? AND r.expires_at > ?`, slug, streamID, unixSeconds(now)).Scan(&currentRevision, &snapshotRevision); errors.Is(err, sql.ErrNoRows) {
		_ = tx.Rollback()
		return live.ErrRoomNotFound
	} else if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("read live document compaction state: %w", err)
	}
	if throughRevision > currentRevision {
		_ = tx.Rollback()
		return live.ErrRevisionConflict
	}
	if throughRevision > snapshotRevision {
		if streamKind == live.StreamMetadata {
			_, err = tx.ExecContext(ctx, `
				UPDATE live_rooms SET metadata_snapshot_revision = ?
				WHERE slug = ? AND expires_at > ?`, throughRevision, slug, unixSeconds(now))
		} else {
			_, err = tx.ExecContext(ctx, `
				UPDATE live_documents SET snapshot_revision = ?
				WHERE room_slug = ? AND document_id = ?`, throughRevision, slug, streamID)
		}
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("update live snapshot revision: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM live_changes
		WHERE room_slug = ? AND stream_kind = ? AND stream_id = ? AND revision <= ?`,
		slug, streamKind, streamID, throughRevision); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("delete compacted live changes: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit live change compaction: %w", err)
	}
	return nil
}

func (s *Store) DeleteExpiredRooms(ctx context.Context, now time.Time, limit int) (int64, error) {
	if limit <= 0 {
		return 0, errors.New("live room cleanup limit must be positive")
	}
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM live_rooms
		WHERE slug IN (
			SELECT slug FROM live_rooms
			WHERE expires_at <= ?
			ORDER BY expires_at, slug
			LIMIT ?
		)`, unixSeconds(now), limit)
	if err != nil {
		return 0, fmt.Errorf("delete expired live rooms: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count deleted live rooms: %w", err)
	}
	return deleted, nil
}

func insertRoom(ctx context.Context, tx *sql.Tx, snapshot live.RoomSnapshot) error {
	var passwordHash any
	if snapshot.PasswordHash != "" {
		passwordHash = snapshot.PasswordHash
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO live_rooms(
			slug, password_hash, content_size, metadata_revision,
			metadata_snapshot_revision, expires_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)`, snapshot.Slug, passwordHash,
		snapshot.ContentSize, snapshot.MetadataRevision,
		snapshot.MetadataSnapshotRevision, unixSeconds(snapshot.ExpiresAt),
		unixSeconds(snapshot.CreatedAt))
	if err != nil {
		if isSQLitePrimaryKey(err) {
			return live.ErrRoomSlugCollision
		}
		return fmt.Errorf("insert live room: %w", err)
	}
	return nil
}

func insertDocuments(ctx context.Context, tx *sql.Tx, snapshot live.RoomSnapshot) error {
	for _, document := range snapshot.Documents {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO live_documents(
				room_slug, document_id, name, language, content, position,
				current_revision, snapshot_revision, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, snapshot.Slug, document.ID,
			document.Name, document.Language, document.Content, document.Position,
			document.CurrentRevision, document.SnapshotRevision,
			unixSeconds(document.UpdatedAt)); err != nil {
			return fmt.Errorf("insert live document %q: %w", document.ID, err)
		}
	}
	return nil
}

func prepareRoomSnapshot(snapshot live.RoomSnapshot) (live.RoomSnapshot, error) {
	if snapshot.Slug == "" || strings.TrimSpace(snapshot.Slug) != snapshot.Slug {
		return live.RoomSnapshot{}, live.ErrInvalidSnapshot
	}
	if snapshot.MetadataRevision < 0 || snapshot.MetadataSnapshotRevision < 0 || snapshot.MetadataSnapshotRevision > snapshot.MetadataRevision {
		return live.RoomSnapshot{}, live.ErrInvalidSnapshot
	}
	if snapshot.CreatedAt.IsZero() || snapshot.ExpiresAt.IsZero() || !snapshot.ExpiresAt.After(snapshot.CreatedAt) {
		return live.RoomSnapshot{}, live.ErrInvalidSnapshot
	}
	if len(snapshot.Documents) == 0 {
		return live.RoomSnapshot{}, live.ErrInvalidSnapshot
	}
	snapshot.CreatedAt = snapshot.CreatedAt.UTC()
	snapshot.ExpiresAt = snapshot.ExpiresAt.UTC()
	snapshot.Documents = append([]live.DocumentSnapshot(nil), snapshot.Documents...)
	seenIDs := make(map[string]struct{}, len(snapshot.Documents))
	var contentSize int64
	for i := range snapshot.Documents {
		document := &snapshot.Documents[i]
		if document.Position != i || document.CurrentRevision < 0 || document.SnapshotRevision < 0 || document.SnapshotRevision > document.CurrentRevision {
			return live.RoomSnapshot{}, live.ErrInvalidSnapshot
		}
		if live.ValidateDocumentID(document.ID) != nil || live.ValidateTabName(document.Name) != nil || live.ValidateLanguageID(document.Language) != nil || live.ValidateDocumentContent(document.Content, math.MaxInt64) != nil {
			return live.RoomSnapshot{}, live.ErrInvalidSnapshot
		}
		if _, exists := seenIDs[document.ID]; exists {
			return live.RoomSnapshot{}, live.ErrInvalidSnapshot
		}
		seenIDs[document.ID] = struct{}{}
		if document.UpdatedAt.IsZero() {
			document.UpdatedAt = snapshot.CreatedAt
		} else {
			document.UpdatedAt = document.UpdatedAt.UTC()
		}
		if int64(len(document.Content)) > math.MaxInt64-contentSize {
			return live.RoomSnapshot{}, live.ErrInvalidSnapshot
		}
		contentSize += int64(len(document.Content))
	}
	snapshot.ContentSize = contentSize
	return snapshot, nil
}

func prepareChange(change live.ChangeRecord, now time.Time) (live.ChangeRecord, error) {
	if err := validateStream(change.StreamKind, change.StreamID); err != nil {
		return live.ChangeRecord{}, err
	}
	if change.Revision <= 0 || strings.TrimSpace(change.Kind) != change.Kind || change.Kind == "" || live.ValidateRoomOperation(change.Kind) != nil || change.Payload == "" || !utf8.ValidString(change.Payload) {
		return live.ChangeRecord{}, live.ErrInvalidChange
	}
	if change.CreatedAt.IsZero() {
		change.CreatedAt = now.UTC()
	} else {
		change.CreatedAt = change.CreatedAt.UTC()
	}
	return change, nil
}

func prepareOperation(operation live.OperationRecord, change live.ChangeRecord, now time.Time) (live.OperationRecord, error) {
	if operation.OperationID == "" || strings.TrimSpace(operation.OperationID) != operation.OperationID || len(operation.OperationID) > 128 || !utf8.ValidString(operation.OperationID) ||
		operation.ClientID == "" || strings.TrimSpace(operation.ClientID) != operation.ClientID || len(operation.ClientID) > 128 || !utf8.ValidString(operation.ClientID) ||
		len(operation.Fingerprint) != 64 || operation.BaseRevision < 0 || operation.Revision != change.Revision || operation.StreamKind != change.StreamKind || operation.StreamID != change.StreamID || operation.OperationKind != change.Kind || operation.ResultPayload != change.Payload {
		return live.OperationRecord{}, live.ErrInvalidChange
	}
	if _, err := hex.DecodeString(operation.Fingerprint); err != nil {
		return live.OperationRecord{}, live.ErrInvalidChange
	}
	if operation.CreatedAt.IsZero() {
		operation.CreatedAt = now.UTC()
	} else {
		operation.CreatedAt = operation.CreatedAt.UTC()
	}
	return operation, nil
}

func validateStream(streamKind, streamID string) error {
	if streamKind == live.StreamMetadata {
		if streamID != live.MetadataStreamID {
			return live.ErrInvalidChange
		}
		return nil
	}
	if streamKind != live.StreamDocument || live.ValidateDocumentID(streamID) != nil {
		return live.ErrInvalidChange
	}
	return nil
}

func unixSeconds(value time.Time) int64 { return value.UTC().Unix() }

func isSQLitePrimaryKey(err error) bool {
	var sqliteErr *sqliteDriver.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY
}
