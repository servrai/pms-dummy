package store

import (
	"database/sql"
	"encoding/json"
)

// Action is one inbound delivery (or replay) logged for the UI's activity view.
type Action struct {
	ID                int64
	RequestID         string
	ActionType        string
	BookingRef        string
	ReservationNumber string
	LineNumber        *int64
	PayloadJSON       string // pretty-printed envelope
	Status            string // created | synced | error | duplicate
	HTTPStatus        *int64
	ErrorCode         string
	ErrorMessage      string
	ReceivedAt        string
	RespondedAt       string
}

// InsertAction logs a delivery.
func (s *Store) InsertAction(a *Action) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO actions(
		request_id, action_type, booking_ref, reservation_number, line_number,
		payload_json, status, http_status, error_code, error_message, received_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		a.RequestID, a.ActionType, a.BookingRef, a.ReservationNumber, a.LineNumber,
		a.PayloadJSON, a.Status, a.HTTPStatus, a.ErrorCode, a.ErrorMessage, Now())
	if err != nil {
		return 0, err
	}
	a.ID, err = res.LastInsertId()
	return a.ID, err
}

// UpdateActionOutcome records how we responded.
func (s *Store) UpdateActionOutcome(id int64, status string, httpStatus int, errorCode, errorMessage string) error {
	hs := int64(httpStatus)
	_, err := s.db.Exec(`UPDATE actions SET status=?, http_status=?, error_code=?, error_message=?, responded_at=? WHERE id=?`,
		status, hs, errorCode, errorMessage, Now(), id)
	return err
}

// LastActionByRequestID returns the newest row for the idempotency lookup.
func (s *Store) LastActionByRequestID(requestID string) (*Action, error) {
	var a Action
	var hs sql.NullInt64
	err := s.db.QueryRow(`SELECT id, request_id, action_type, booking_ref, reservation_number,
		line_number, payload_json, status, http_status, error_code, error_message, received_at
		FROM actions WHERE request_id=? ORDER BY id DESC LIMIT 1`, requestID).
		Scan(&a.ID, &a.RequestID, &a.ActionType, &a.BookingRef, &a.ReservationNumber,
			&a.LineNumber, &a.PayloadJSON, &a.Status, &hs, &a.ErrorCode, &a.ErrorMessage, &a.ReceivedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if hs.Valid {
		v := hs.Int64
		a.HTTPStatus = &v
	}
	return &a, nil
}

// ListActions returns the newest deliveries for the log page.
func (s *Store) ListActions(limit int) ([]Action, error) {
	rows, err := s.db.Query(`SELECT id, request_id, action_type, booking_ref, reservation_number,
		line_number, payload_json, status, http_status, error_code, error_message, received_at
		FROM actions ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Action
	for rows.Next() {
		var a Action
		var hs sql.NullInt64
		if err := rows.Scan(&a.ID, &a.RequestID, &a.ActionType, &a.BookingRef, &a.ReservationNumber,
			&a.LineNumber, &a.PayloadJSON, &a.Status, &hs, &a.ErrorCode, &a.ErrorMessage, &a.ReceivedAt); err != nil {
			return nil, err
		}
		if hs.Valid {
			v := hs.Int64
			a.HTTPStatus = &v
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ActionStats feeds the dashboard: totals by outcome + last inbound at.
func (s *Store) ActionStats() (total, synced, errored int, lastAt string, err error) {
	err = s.db.QueryRow(`SELECT COUNT(*),
		COALESCE(SUM(status IN ('synced','duplicate')), 0),
		COALESCE(SUM(status='error'), 0),
		COALESCE(MAX(received_at), '')
		FROM actions`).Scan(&total, &synced, &errored, &lastAt)
	return
}

// PrettyJSON indents v for the log tables.
func PrettyJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return ""
	}
	return string(b)
}
