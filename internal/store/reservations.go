package store

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

// Reservation is one stay line. (reservation_number, line_number) is the PMS
// wire address servr addresses us by; booking_ref is "<number>-<line>".
type Reservation struct {
	ID                int64
	ReservationNumber string
	LineNumber        int64
	Status            string // Reserved | CheckedIn | CheckedOut | Cancelled
	GuestFirstName    string
	GuestSurname      string
	GuestEmail        string
	GuestPhone        string
	NationCode        string
	ArrivalDate       string
	DepartureDate     string
	RoomNumber        string
	Adults            int64
	Children          int64
	RoomQuantity      int64
	RoomRate          float64
	RateCode          string
	Remark            string
	GroupCode         string
	CheckedInAt       *string
	CheckedOutAt      *string
	CheckedOutBy      *string
	CancelledAt       *string
	CancelReasonCode  *string
	CancelReasonText  *string
	SyncStatus        string // local | synced | error
	LastSyncedAt      *string
	CreatedAt         string
	UpdatedAt         string
}

// BookingRef renders servr's internal key for this line.
func (r *Reservation) BookingRef() string {
	return fmt.Sprintf("%s-%d", r.ReservationNumber, r.LineNumber)
}

// GuestName renders "First Last" for manifest lines.
func (r *Reservation) GuestName() string {
	return strings.TrimSpace(r.GuestFirstName + " " + r.GuestSurname)
}

const resvCols = `id, reservation_number, line_number, status,
	guest_first_name, guest_surname, guest_email, guest_phone, nation_code,
	arrival_date, departure_date, room_number, adults, children, room_quantity,
	room_rate, rate_code, remark, group_code,
	checked_in_at, checked_out_at, checked_out_by, cancelled_at,
	cancel_reason_code, cancel_reason_text, sync_status, last_synced_at,
	created_at, updated_at`

func scanReservation(row interface{ Scan(...any) error }) (*Reservation, error) {
	var r Reservation
	err := row.Scan(&r.ID, &r.ReservationNumber, &r.LineNumber, &r.Status,
		&r.GuestFirstName, &r.GuestSurname, &r.GuestEmail, &r.GuestPhone, &r.NationCode,
		&r.ArrivalDate, &r.DepartureDate, &r.RoomNumber, &r.Adults, &r.Children, &r.RoomQuantity,
		&r.RoomRate, &r.RateCode, &r.Remark, &r.GroupCode,
		&r.CheckedInAt, &r.CheckedOutAt, &r.CheckedOutBy, &r.CancelledAt,
		&r.CancelReasonCode, &r.CancelReasonText, &r.SyncStatus, &r.LastSyncedAt,
		&r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// ListReservations returns all reservations, newest first.
func (s *Store) ListReservations() ([]Reservation, error) {
	rows, err := s.db.Query(`SELECT ` + resvCols + ` FROM reservations ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Reservation
	for rows.Next() {
		r, err := scanReservation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// ReservationByID fetches one reservation; nil when absent.
func (s *Store) ReservationByID(id int64) (*Reservation, error) {
	r, err := scanReservation(s.db.QueryRow(`SELECT `+resvCols+` FROM reservations WHERE id=?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return r, err
}

// ReservationByRef resolves the PMS wire address; nil when absent.
func (s *Store) ReservationByRef(number string, line int64) (*Reservation, error) {
	r, err := scanReservation(s.db.QueryRow(
		`SELECT `+resvCols+` FROM reservations WHERE reservation_number=? AND line_number=?`, number, line))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return r, err
}

// ReservationByBookingRef parses "<number>-<line>" (or bare number → line 1).
func (s *Store) ReservationByBookingRef(ref string) (*Reservation, error) {
	number, line, err := ParseBookingRef(ref)
	if err != nil {
		return nil, nil
	}
	return s.ReservationByRef(number, line)
}

// ParseBookingRef splits servr's booking_ref into the PMS wire address.
func ParseBookingRef(ref string) (string, int64, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", 0, fmt.Errorf("empty booking ref")
	}
	if i := strings.LastIndex(ref, "-"); i > 0 {
		if line, err := strconv.ParseInt(ref[i+1:], 10, 64); err == nil && line >= 1 {
			return ref[:i], line, nil
		}
	}
	return ref, 1, nil
}

// CreateReservation inserts a row built from the form/payload fields.
func (s *Store) CreateReservation(r *Reservation) (int64, error) {
	now := Now()
	res, err := s.db.Exec(`INSERT INTO reservations(
		reservation_number, line_number, status,
		guest_first_name, guest_surname, guest_email, guest_phone, nation_code,
		arrival_date, departure_date, room_number, adults, children, room_quantity,
		room_rate, rate_code, remark, group_code,
		checked_in_at, checked_out_at, checked_out_by, cancelled_at,
		cancel_reason_code, cancel_reason_text, sync_status, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ReservationNumber, r.LineNumber, r.Status,
		r.GuestFirstName, r.GuestSurname, r.GuestEmail, r.GuestPhone, r.NationCode,
		r.ArrivalDate, r.DepartureDate, r.RoomNumber, r.Adults, r.Children, r.RoomQuantity,
		r.RoomRate, r.RateCode, r.Remark, r.GroupCode,
		r.CheckedInAt, r.CheckedOutAt, r.CheckedOutBy, r.CancelledAt,
		r.CancelReasonCode, r.CancelReasonText, "local", now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateReservation writes every mutable column (id and audit timestamps
// aside) and resets sync_status to 'local' — a UI edit desynchronizes us
// from what servr last saw.
func (s *Store) UpdateReservation(r *Reservation) error {
	_, err := s.db.Exec(`UPDATE reservations SET
		reservation_number=?, line_number=?, status=?,
		guest_first_name=?, guest_surname=?, guest_email=?, guest_phone=?, nation_code=?,
		arrival_date=?, departure_date=?, room_number=?, adults=?, children=?, room_quantity=?,
		room_rate=?, rate_code=?, remark=?, group_code=?,
		checked_in_at=?, checked_out_at=?, checked_out_by=?, cancelled_at=?,
		cancel_reason_code=?, cancel_reason_text=?, sync_status=?, updated_at=?
		WHERE id=?`,
		r.ReservationNumber, r.LineNumber, r.Status,
		r.GuestFirstName, r.GuestSurname, r.GuestEmail, r.GuestPhone, r.NationCode,
		r.ArrivalDate, r.DepartureDate, r.RoomNumber, r.Adults, r.Children, r.RoomQuantity,
		r.RoomRate, r.RateCode, r.Remark, r.GroupCode,
		r.CheckedInAt, r.CheckedOutAt, r.CheckedOutBy, r.CancelledAt,
		r.CancelReasonCode, r.CancelReasonText, "local", Now(), r.ID)
	return err
}

// DeleteReservation removes a row outright (the polygon equivalent of "the
// reservation is gone from the PMS" — servr's GET then 404s).
func (s *Store) DeleteReservation(id int64) error {
	_, err := s.db.Exec(`DELETE FROM reservations WHERE id=?`, id)
	return err
}

// SetReservationSync updates the sync flag after inbound/outcome events.
func (s *Store) SetReservationSync(id int64, status string) error {
	var last *string
	if status == "synced" {
		now := Now()
		last = &now
	}
	_, err := s.db.Exec(`UPDATE reservations SET sync_status=?, last_synced_at=?, updated_at=? WHERE id=?`,
		status, last, Now(), id)
	return err
}

// ReservationSyncStats counts reservations by sync_status for the dashboard.
func (s *Store) ReservationSyncStats() (local, synced, errored int, err error) {
	err = s.db.QueryRow(`SELECT
		COALESCE(SUM(sync_status='local'), 0),
		COALESCE(SUM(sync_status='synced'), 0),
		COALESCE(SUM(sync_status='error'), 0)
		FROM reservations`).Scan(&local, &synced, &errored)
	return
}

// ActiveReservationInRoom returns the id of a CheckedIn reservation occupying
// the room other than excludeID (0 = no exclusion). Used for the
// room_unavailable guard.
func (s *Store) ActiveReservationInRoom(room string, excludeID int64) (int64, error) {
	if room == "" {
		return 0, nil
	}
	var id int64
	err := s.db.QueryRow(`SELECT id FROM reservations WHERE room_number=? AND status='CheckedIn' AND id<>? LIMIT 1`,
		room, excludeID).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return id, err
}
