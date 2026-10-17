package store

import (
	"database/sql"
	"fmt"
)

// Room is a PMS room inventory row. Status uses the servr RoomStatus enum.
type Room struct {
	ID        int64
	Number    string
	Status    string
	Category  string
	Floor     int64
	CreatedAt string
	UpdatedAt string
}

// RoomStatuses is the servr RoomStatus enum (sync + inventory lines).
var RoomStatuses = []string{
	"vacant_clean_checked",
	"vacant_clean_unchecked",
	"vacant_dirty",
	"occupied_cleaned",
	"occupied_dirty",
	"do_not_disturb",
	"expected_departure",
	"checked_out",
	"out_of_order",
	"off_market",
}

// ValidRoomStatus reports whether s is in the enum.
func ValidRoomStatus(s string) bool {
	for _, v := range RoomStatuses {
		if v == s {
			return true
		}
	}
	return false
}

func scanRoom(row interface{ Scan(...any) error }) (*Room, error) {
	var r Room
	if err := row.Scan(&r.ID, &r.Number, &r.Status, &r.Category, &r.Floor, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, err
	}
	return &r, nil
}

const roomCols = `id, number, status, category, floor, created_at, updated_at`

// ListRooms returns all rooms ordered by number.
func (s *Store) ListRooms() ([]Room, error) {
	rows, err := s.db.Query(`SELECT ` + roomCols + ` FROM rooms ORDER BY number`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Room
	for rows.Next() {
		r, err := scanRoom(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// RoomByID fetches one room.
func (s *Store) RoomByID(id int64) (*Room, error) {
	return scanRoom(s.db.QueryRow(`SELECT `+roomCols+` FROM rooms WHERE id=?`, id))
}

// RoomByNumber fetches one room by its number; nil when absent.
func (s *Store) RoomByNumber(number string) (*Room, error) {
	r, err := scanRoom(s.db.QueryRow(`SELECT `+roomCols+` FROM rooms WHERE number=?`, number))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return r, err
}

// CreateRoom inserts a room.
func (s *Store) CreateRoom(number, status, category string, floor int64) (int64, error) {
	now := Now()
	res, err := s.db.Exec(`INSERT INTO rooms(number, status, category, floor, created_at, updated_at)
	                       VALUES(?,?,?,?,?,?)`, number, status, category, floor, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateRoom writes every mutable column.
func (s *Store) UpdateRoom(id int64, number, status, category string, floor int64) error {
	_, err := s.db.Exec(`UPDATE rooms SET number=?, status=?, category=?, floor=?, updated_at=? WHERE id=?`,
		number, status, category, floor, Now(), id)
	return err
}

// DeleteRoom removes a room.
func (s *Store) DeleteRoom(id int64) error {
	_, err := s.db.Exec(`DELETE FROM rooms WHERE id=?`, id)
	return err
}

// SetRoomStatusByNumber flips a room's status (room lifecycle side effects of
// check-in / checkout / cancel).
func (s *Store) SetRoomStatusByNumber(number, status string) error {
	if number == "" {
		return nil
	}
	res, err := s.db.Exec(`UPDATE rooms SET status=?, updated_at=? WHERE number=?`, status, Now(), number)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("room %q not found", number)
	}
	return nil
}
