// Package store is pms-dummy's SQLite layer: rooms, reservations, settings,
// inbound action log, outbound event log and sessions. One file-backed DB,
// one process, MaxOpenConns(1) so writers never race.
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Store wraps the SQLite handle.
type Store struct{ db *sql.DB }

// Open opens (creating if needed) the database at path, applies pragmas and
// migrations, and seeds default settings.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;`); err != nil {
		return nil, fmt.Errorf("pragmas: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	if err := s.seed(); err != nil {
		return nil, err
	}
	return s, nil
}

// Close closes the underlying handle.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	if version < 1 {
		if _, err := s.db.Exec(schemaSQL); err != nil {
			return fmt.Errorf("apply schema v1: %w", err)
		}
		if _, err := s.db.Exec(`PRAGMA user_version = 1`); err != nil {
			return fmt.Errorf("set user_version: %w", err)
		}
		version = 1
	}
	if version < 2 {
		// v2: per-reservation room type (servr RoomCategoryCode).
		if _, err := s.db.Exec(`ALTER TABLE reservations ADD COLUMN room_category TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("apply schema v2: %w", err)
		}
		if _, err := s.db.Exec(`PRAGMA user_version = 2`); err != nil {
			return fmt.Errorf("set user_version: %w", err)
		}
	}
	return nil
}

func (s *Store) seed() error {
	def := DefaultSettings()
	if def.Secret == "" {
		return nil
	}
	// Only the secret is truly minted; everything else has fixed defaults.
	if _, err := s.db.Exec(`SELECT value FROM settings WHERE key='secret'`); err == sql.ErrNoRows {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			return fmt.Errorf("mint secret: %w", err)
		}
		def.Secret = hex.EncodeToString(buf)
	} else if err != nil {
		return err
	} else {
		return nil // already seeded
	}
	now := Now()
	for k, v := range map[string]string{
		"secret":               def.Secret,
		"servr_base_url":       def.ServrBaseURL,
		"hotel_code":           def.HotelCode,
		"inbound_active":       "1",
		"path_actions":         def.PathActions,
		"path_get_reservation": def.PathGetReservation,
		"confirm_after_apply":  "1",
		"sync_after_mutation":  "0",
		"fault_mode":           def.FaultMode,
		"fault_delay_ms":       "0",
		"fault_error_code":     def.FaultErrorCode,
	} {
		if _, err := s.db.Exec(`INSERT INTO settings(key, value, updated_at) VALUES(?,?,?)`, k, v, now); err != nil {
			return fmt.Errorf("seed %s: %w", k, err)
		}
	}
	return nil
}

// Now returns the canonical UTC timestamp format used across the store.
func Now() string { return time.Now().UTC().Format(time.RFC3339) }
