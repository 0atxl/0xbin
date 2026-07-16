// Package sqlite provides 0xbin's SQLite foundation.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/0atxl/0xbin/db/migrations"
	"github.com/0atxl/0xbin/internal/paste"
	sqliteDriver "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const busyTimeout = 5 * time.Second

// Store owns the SQLite connection and its schema migrations.
type Store struct{ db *sql.DB }

// Open creates the database directory, opens the database, configures SQLite,
// and applies the migrations embedded in this binary.
func Open(ctx context.Context, dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "0xbin.db"))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.configure(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if err := store.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) configure(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		return fmt.Errorf("enable foreign keys: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout = %d", busyTimeout.Milliseconds())); err != nil {
		return fmt.Errorf("set busy timeout: %w", err)
	}
	var mode string
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode = WAL").Scan(&mode); err != nil {
		return fmt.Errorf("enable WAL: %w", err)
	}
	if !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("WAL mode was not enabled (got %q)", mode)
	}
	return nil
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL) STRICT"); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}
	entries, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil {
		return err
	}
	sort.Strings(entries)
	var current int
	if err := s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&current); err != nil {
		return err
	}
	for _, name := range entries {
		var version int
		if _, err := fmt.Sscanf(filepath.Base(name), "%03d_", &version); err != nil {
			return fmt.Errorf("invalid migration name %q", name)
		}
		if version <= current {
			continue
		}
		body, err := migrations.Files.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, string(body)); err == nil {
			_, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", version, time.Now().UTC().Unix())
		}
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %d: %w", version, err)
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	var disk int
	if err := s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&disk); err != nil {
		return err
	}
	if disk > len(entries) {
		return fmt.Errorf("database schema version %d is newer than this binary", disk)
	}
	return nil
}

// Ping reports whether the initialized database is reachable.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// Create inserts a plaintext paste. The slug primary key is the authoritative
// uniqueness check; only that exact constraint maps to ErrSlugCollision.
func (s *Store) Create(ctx context.Context, newPaste paste.NewPaste) (paste.Paste, error) {
	payload, err := json.Marshal(newPaste.Payload)
	if err != nil {
		return paste.Paste{}, fmt.Errorf("encode plaintext payload: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO pastes (
			slug, payload, is_encrypted, crypto_version, burn_after_read,
			content_size, expires_at, created_at
		) VALUES (?, ?, 0, NULL, ?, ?, ?, ?)`,
		newPaste.Slug,
		string(payload),
		boolToInt(newPaste.BurnAfterRead),
		newPaste.ContentSize,
		newPaste.ExpiresAt.UTC().Unix(),
		newPaste.CreatedAt.UTC().Unix(),
	)
	if err != nil {
		var sqliteErr *sqliteDriver.Error
		if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY {
			return paste.Paste{}, paste.ErrSlugCollision
		}
		return paste.Paste{}, fmt.Errorf("insert paste: %w", err)
	}
	return paste.Paste{
		Slug:          newPaste.Slug,
		Payload:       newPaste.Payload,
		BurnAfterRead: newPaste.BurnAfterRead,
		ContentSize:   newPaste.ContentSize,
		ExpiresAt:     unixTime(newPaste.ExpiresAt.UTC().Unix()),
		CreatedAt:     unixTime(newPaste.CreatedAt.UTC().Unix()),
	}, nil
}

// CreateEncrypted inserts an opaque encrypted envelope. The server stores it
// without attempting to decrypt, authenticate, or otherwise inspect content.
func (s *Store) CreateEncrypted(ctx context.Context, newPaste paste.NewEncryptedPaste) (paste.Paste, error) {
	payload, err := json.Marshal(newPaste.Envelope)
	if err != nil {
		return paste.Paste{}, fmt.Errorf("encode encrypted envelope: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO pastes (
			slug, payload, is_encrypted, crypto_version, burn_after_read,
			content_size, expires_at, created_at
		) VALUES (?, ?, 1, ?, ?, ?, ?, ?)`,
		newPaste.Slug,
		string(payload),
		newPaste.Envelope.Version,
		boolToInt(newPaste.BurnAfterRead),
		newPaste.ContentSize,
		newPaste.ExpiresAt.UTC().Unix(),
		newPaste.CreatedAt.UTC().Unix(),
	)
	if err != nil {
		var sqliteErr *sqliteDriver.Error
		if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY {
			return paste.Paste{}, paste.ErrSlugCollision
		}
		return paste.Paste{}, fmt.Errorf("insert encrypted paste: %w", err)
	}
	return paste.Paste{
		Slug:          newPaste.Slug,
		Envelope:      &newPaste.Envelope,
		IsEncrypted:   true,
		CryptoVersion: newPaste.Envelope.Version,
		BurnAfterRead: newPaste.BurnAfterRead,
		ContentSize:   newPaste.ContentSize,
		ExpiresAt:     unixTime(newPaste.ExpiresAt.UTC().Unix()),
		CreatedAt:     unixTime(newPaste.CreatedAt.UTC().Unix()),
	}, nil
}

// GetActive returns a plaintext paste only when its expiry is later than now.
// Missing and expired records deliberately share ErrNotFound.
func (s *Store) GetActive(ctx context.Context, slug string, now time.Time) (paste.Paste, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT slug, payload, is_encrypted, crypto_version, burn_after_read,
		       content_size, expires_at, created_at
		FROM pastes
		WHERE slug = ? AND expires_at > ?`, slug, now.UTC().Unix())
	result, err := scanPaste(row)
	if errors.Is(err, sql.ErrNoRows) {
		return paste.Paste{}, paste.ErrNotFound
	}
	if err != nil {
		return paste.Paste{}, fmt.Errorf("get active paste: %w", err)
	}
	return result, nil
}

// ConsumeActive atomically deletes and returns one active burn-after-read
// paste. A missing, expired, normal, or already consumed paste is not found.
func (s *Store) ConsumeActive(ctx context.Context, slug string, now time.Time) (paste.Paste, error) {
	row := s.db.QueryRowContext(ctx, `
		DELETE FROM pastes
		WHERE slug = ? AND burn_after_read = 1 AND expires_at > ?
		RETURNING slug, payload, is_encrypted, crypto_version, burn_after_read,
		          content_size, expires_at, created_at`, slug, now.UTC().Unix())
	result, err := scanPaste(row)
	if errors.Is(err, sql.ErrNoRows) {
		return paste.Paste{}, paste.ErrNotFound
	}
	if err != nil {
		return paste.Paste{}, fmt.Errorf("consume active paste: %w", err)
	}
	return result, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanPaste(row rowScanner) (paste.Paste, error) {
	var (
		payloadJSON   string
		isEncrypted   int
		cryptoVersion sql.NullInt64
		burnAfterRead int
		result        paste.Paste
		expiresAt     int64
		createdAt     int64
	)
	err := row.Scan(
		&result.Slug,
		&payloadJSON,
		&isEncrypted,
		&cryptoVersion,
		&burnAfterRead,
		&result.ContentSize,
		&expiresAt,
		&createdAt,
	)
	if err != nil {
		return paste.Paste{}, err
	}
	if isEncrypted == 1 && cryptoVersion.Valid {
		var envelope paste.CiphertextEnvelope
		if err := json.Unmarshal([]byte(payloadJSON), &envelope); err != nil {
			return paste.Paste{}, fmt.Errorf("decode encrypted envelope: %w", err)
		}
		result.Envelope = &envelope
		result.IsEncrypted = true
		result.CryptoVersion = int(cryptoVersion.Int64)
	} else if isEncrypted == 0 && !cryptoVersion.Valid {
		if err := json.Unmarshal([]byte(payloadJSON), &result.Payload); err != nil {
			return paste.Paste{}, fmt.Errorf("decode plaintext payload: %w", err)
		}
	} else {
		return paste.Paste{}, fmt.Errorf("invalid encryption state")
	}
	result.BurnAfterRead = burnAfterRead != 0
	result.ExpiresAt = unixTime(expiresAt)
	result.CreatedAt = unixTime(createdAt)
	return result, nil
}

// DeleteExpiredBatch physically removes at most limit rows whose expiry has
// passed. Read-time expiry remains the access-control boundary.
func (s *Store) DeleteExpiredBatch(ctx context.Context, now time.Time, limit int) (int64, error) {
	if limit < 1 {
		return 0, fmt.Errorf("delete limit must be positive")
	}
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM pastes
		WHERE rowid IN (
			SELECT rowid
			FROM pastes
			WHERE expires_at <= ?
			ORDER BY expires_at
			LIMIT ?
		)`, now.UTC().Unix(), limit)
	if err != nil {
		return 0, fmt.Errorf("delete expired pastes: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count deleted expired pastes: %w", err)
	}
	return count, nil
}

// Close releases the database connection.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the connection only for storage integration tests during foundation work.
func (s *Store) DB() *sql.DB { return s.db }

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func unixTime(seconds int64) time.Time { return time.Unix(seconds, 0).UTC() }
