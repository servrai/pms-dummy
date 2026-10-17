package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"time"
)

// CreateSession mints a session token valid for 7 days.
func (s *Store) CreateSession() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)
	_, err := s.db.Exec(`INSERT INTO sessions(token, created_at, expires_at) VALUES(?,?,?)`,
		token, Now(), time.Now().UTC().Add(7*24*time.Hour).Format(time.RFC3339))
	if err != nil {
		return "", err
	}
	return token, nil
}

// ValidSession reports whether the token belongs to an unexpired session and
// lazily drops expired rows.
func (s *Store) ValidSession(token string) (bool, error) {
	if token == "" {
		return false, nil
	}
	if _, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, Now()); err != nil {
		return false, err
	}
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM sessions WHERE token=?`, token).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

// DeleteSession logs out.
func (s *Store) DeleteSession(token string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token=?`, token)
	return err
}
