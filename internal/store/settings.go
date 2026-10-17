package store

import (
	"database/sql"
	"fmt"
	"strconv"
)

// Settings is the whole integration configuration, stored as a kv table.
type Settings struct {
	Secret             string
	ServrBaseURL       string // e.g. https://api-dev.servr.ai/api/integration/webhook
	HotelCode          string
	InboundActive      bool
	PathActions        string // default /actions
	PathGetReservation string // default /pms/reservations/{booking_ref}
	ConfirmAfterApply  bool   // fire action.confirm after a synced inbound action
	SyncAfterMutation  bool   // fire reservation.sync after UI mutations (off by default)
	FaultMode          string // normal | force_500 | timeout | delay | respond_error_code
	FaultDelayMS       int
	FaultErrorCode     string
}

// DefaultSettings returns the seeded configuration.
func DefaultSettings() Settings {
	return Settings{
		ServrBaseURL:       "https://api-dev.servr.ai/api/integration/webhook",
		HotelCode:          "DUMMY",
		InboundActive:      true,
		PathActions:        "/actions",
		PathGetReservation: "/pms/reservations/{booking_ref}",
		ConfirmAfterApply:  true,
		SyncAfterMutation:  false,
		FaultMode:          "normal",
		FaultDelayMS:       0,
		FaultErrorCode:     "reservation_not_found",
	}
}

// Settings loads the current configuration.
func (s *Store) Settings() (Settings, error) {
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return Settings{}, err
	}
	defer rows.Close()
	kv := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return Settings{}, err
		}
		kv[k] = v
	}
	if err := rows.Err(); err != nil {
		return Settings{}, err
	}
	st := DefaultSettings()
	st.Secret = kv["secret"]
	if v, ok := kv["servr_base_url"]; ok {
		st.ServrBaseURL = v
	}
	if v, ok := kv["hotel_code"]; ok {
		st.HotelCode = v
	}
	if v, ok := kv["path_actions"]; ok && v != "" {
		st.PathActions = v
	}
	if v, ok := kv["path_get_reservation"]; ok && v != "" {
		st.PathGetReservation = v
	}
	st.InboundActive = kv["inbound_active"] != "0"
	st.ConfirmAfterApply = kv["confirm_after_apply"] != "0"
	st.SyncAfterMutation = kv["sync_after_mutation"] == "1"
	if v, ok := kv["fault_mode"]; ok && v != "" {
		st.FaultMode = v
	}
	if v, err := strconv.Atoi(kv["fault_delay_ms"]); err == nil && v >= 0 {
		st.FaultDelayMS = v
	}
	if v, ok := kv["fault_error_code"]; ok && v != "" {
		st.FaultErrorCode = v
	}
	return st, nil
}

// SaveSettings persists the whole configuration in one transaction.
func (s *Store) SaveSettings(st Settings) error {
	kv := map[string]string{
		"secret":               st.Secret,
		"servr_base_url":       st.ServrBaseURL,
		"hotel_code":           st.HotelCode,
		"inbound_active":       boolStr(st.InboundActive),
		"path_actions":         st.PathActions,
		"path_get_reservation": st.PathGetReservation,
		"confirm_after_apply":  boolStr(st.ConfirmAfterApply),
		"sync_after_mutation":  boolStr(st.SyncAfterMutation),
		"fault_mode":           st.FaultMode,
		"fault_delay_ms":       strconv.Itoa(st.FaultDelayMS),
		"fault_error_code":     st.FaultErrorCode,
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for k, v := range kv {
		if _, err := tx.Exec(`INSERT INTO settings(key, value, updated_at) VALUES(?,?,?)
		                      ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
			k, v, Now()); err != nil {
			return fmt.Errorf("save setting %s: %w", k, err)
		}
	}
	return tx.Commit()
}

// SetSecret overwrites just the signing secret (used by the settings form's
// paste-a-known-secret field and by regenerate).
func (s *Store) SetSecret(secret string) error {
	_, err := s.db.Exec(`INSERT INTO settings(key, value, updated_at) VALUES('secret',?,?)
	                     ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		secret, Now())
	return err
}

// ConsumeFlashSecret reads and clears the one-time secret reveal left by a
// regenerate. Returns "" when nothing is pending or it was already shown.
func (s *Store) ConsumeFlashSecret() (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key='secret_flash'`).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if _, err := s.db.Exec(`DELETE FROM settings WHERE key='secret_flash'`); err != nil {
		return "", err
	}
	return v, nil
}

// SetFlashSecret stages a one-time secret reveal.
func (s *Store) SetFlashSecret(secret string) error {
	_, err := s.db.Exec(`INSERT INTO settings(key, value, updated_at) VALUES('secret_flash',?,?)
	                     ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		secret, Now())
	return err
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
