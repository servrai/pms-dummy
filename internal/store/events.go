package store

import "database/sql"

// OutboundEvent is one signed event we sent to servr (or to the echo
// self-loop), with the response we got back.
type OutboundEvent struct {
	ID             int64
	Event          string
	URL            string
	PayloadJSON    string
	ResponseStatus *int64
	ResponseBody   string
	Error          string
	DurationMS     int64
	CreatedAt      string
}

// InsertOutboundEvent logs a send attempt (success or failure).
func (s *Store) InsertOutboundEvent(e *OutboundEvent) error {
	_, err := s.db.Exec(`INSERT INTO outbound_events(
		event, url, payload_json, response_status, response_body, error, duration_ms, created_at)
		VALUES(?,?,?,?,?,?,?,?)`,
		e.Event, e.URL, e.PayloadJSON, e.ResponseStatus, e.ResponseBody, e.Error, e.DurationMS, Now())
	return err
}

// ListOutboundEvents returns the newest sends for the log page.
func (s *Store) ListOutboundEvents(limit int) ([]OutboundEvent, error) {
	rows, err := s.db.Query(`SELECT id, event, url, payload_json, response_status, response_body,
		error, duration_ms, created_at FROM outbound_events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OutboundEvent
	for rows.Next() {
		var e OutboundEvent
		var hs sql.NullInt64
		if err := rows.Scan(&e.ID, &e.Event, &e.URL, &e.PayloadJSON, &hs, &e.ResponseBody,
			&e.Error, &e.DurationMS, &e.CreatedAt); err != nil {
			return nil, err
		}
		if hs.Valid {
			v := hs.Int64
			e.ResponseStatus = &v
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// OutboundStats feeds the dashboard: count, last send at + its HTTP status.
func (s *Store) OutboundStats() (total int, lastAt string, lastStatus int, err error) {
	var hs sql.NullInt64
	err = s.db.QueryRow(`SELECT COUNT(*), COALESCE(MAX(created_at),''), 0 FROM outbound_events`).Scan(&total, &lastAt, &hs)
	if err != nil || total == 0 {
		return
	}
	var status sql.NullInt64
	err = s.db.QueryRow(`SELECT response_status FROM outbound_events ORDER BY id DESC LIMIT 1`).Scan(&status)
	if err == sql.ErrNoRows {
		err = nil
		return
	}
	if err == nil && status.Valid {
		lastStatus = int(status.Int64)
	}
	return
}
