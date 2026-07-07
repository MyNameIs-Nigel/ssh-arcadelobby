package store

import (
	"context"
	"fmt"
)

// migrations are applied in order on startup.
var migrations = []string{
	`CREATE TABLE accounts (
		fingerprint TEXT PRIMARY KEY,
		public_key  TEXT NOT NULL,
		first_seen  INTEGER NOT NULL,
		last_seen   INTEGER NOT NULL
	);
	CREATE TABLE alpha_warning_acks (
		fingerprint TEXT NOT NULL REFERENCES accounts(fingerprint),
		game_id     TEXT NOT NULL,
		acked_at    INTEGER NOT NULL,
		PRIMARY KEY (fingerprint, game_id)
	) WITHOUT ROWID;`,
}

func (st *Store) migrate(ctx context.Context) error {
	var current int
	if err := st.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("store: read schema version: %w", err)
	}
	if current > len(migrations) {
		return fmt.Errorf("store: database schema version %d is newer than supported %d", current, len(migrations))
	}

	for v := current; v < len(migrations); v++ {
		tx, err := st.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("store: begin migration %d: %w", v+1, err)
		}
		if _, err := tx.ExecContext(ctx, migrations[v]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store: apply migration %d: %w", v+1, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", v+1)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store: bump schema version to %d: %w", v+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("store: commit migration %d: %w", v+1, err)
		}
	}
	return nil
}

// SchemaVersion returns the database's current schema version.
func (st *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	err := st.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v)
	return v, err
}
