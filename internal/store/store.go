// Package store is the router's SQLite persistence layer: accounts keyed by
// SSH public-key fingerprint and per-game alpha-warning acknowledgements.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"

	_ "modernc.org/sqlite" // pure-Go, cgo-free driver (static builds)
)

var (
	gameIDPattern = regexp.MustCompile(`^[a-z0-9-]{1,24}$`)
	// ErrInvalidKey is returned when a fingerprint or game id fails validation.
	ErrInvalidKey = errors.New("store: invalid fingerprint or game id")
)

// Store is the open database handle.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the database at path, applies pending
// migrations, and returns the store.
func Open(ctx context.Context, path string) (*Store, error) {
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("store: create db dir: %w", err)
		}
	}

	dsn := "file:" + url.PathEscape(path) +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	db.SetMaxOpenConns(1)

	st := &Store{db: db}
	if err := st.verifyWAL(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := st.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	return st, nil
}

// Close closes the underlying database.
func (st *Store) Close() error { return st.db.Close() }

func (st *Store) verifyWAL(ctx context.Context) error {
	var mode string
	if err := st.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return fmt.Errorf("store: read journal mode: %w", err)
	}
	if mode != "wal" {
		return fmt.Errorf("store: WAL mode required, database reports %q", mode)
	}
	return nil
}

// TouchAccount records that fingerprint connected at now, creating the
// account row on first sight.
func (st *Store) TouchAccount(ctx context.Context, fingerprint, publicKey string, now int64) error {
	if fingerprint == "" {
		return ErrInvalidKey
	}
	_, err := st.db.ExecContext(ctx, `
		INSERT INTO accounts (fingerprint, public_key, first_seen, last_seen)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (fingerprint) DO UPDATE SET last_seen = excluded.last_seen`,
		fingerprint, publicKey, now, now)
	if err != nil {
		return fmt.Errorf("store: touch account: %w", err)
	}
	return nil
}

// HasAlphaAck reports whether fingerprint dismissed the alpha warning for gameID.
func (st *Store) HasAlphaAck(ctx context.Context, fingerprint, gameID string) (bool, error) {
	if err := validateKeys(fingerprint, gameID); err != nil {
		return false, err
	}
	var n int
	err := st.db.QueryRowContext(ctx, `
		SELECT COUNT(1) FROM alpha_warning_acks
		WHERE fingerprint = ? AND game_id = ?`,
		fingerprint, gameID).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("store: has alpha ack: %w", err)
	}
	return n > 0, nil
}

// AckAlphaWarning records that fingerprint chose not to see the alpha warning
// for gameID again.
func (st *Store) AckAlphaWarning(ctx context.Context, fingerprint, gameID string, now int64) error {
	if err := validateKeys(fingerprint, gameID); err != nil {
		return err
	}
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: ack alpha warning: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO accounts (fingerprint, public_key, first_seen, last_seen)
		VALUES (?, '', ?, ?)
		ON CONFLICT (fingerprint) DO UPDATE SET last_seen = excluded.last_seen`,
		fingerprint, now, now); err != nil {
		return fmt.Errorf("store: ack alpha warning: ensure account: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO alpha_warning_acks (fingerprint, game_id, acked_at)
		VALUES (?, ?, ?)
		ON CONFLICT (fingerprint, game_id) DO UPDATE SET acked_at = excluded.acked_at`,
		fingerprint, gameID, now); err != nil {
		return fmt.Errorf("store: ack alpha warning: %w", err)
	}
	return tx.Commit()
}

func validateKeys(fingerprint, gameID string) error {
	if fingerprint == "" || !gameIDPattern.MatchString(gameID) {
		return ErrInvalidKey
	}
	return nil
}
