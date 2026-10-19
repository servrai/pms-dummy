package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// A v1 database (what pms-dummy-dev runs today) must upgrade in place and
// keep its rows.
func TestMigrateV1ToV2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(schemaSQL + `; PRAGMA user_version = 1;
		INSERT INTO reservations(reservation_number, created_at, updated_at) VALUES('OLD1', 'x', 'x');`); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := st.ReservationByBookingRef("OLD1")
	if err != nil || r == nil || r.RoomCategory != "" {
		t.Fatalf("migrated row = %+v, %v", r, err)
	}
	r.RoomCategory = "STD"
	if err := st.UpdateReservation(r); err != nil {
		t.Fatal(err)
	}
	var v int
	_ = st.db.QueryRow(`PRAGMA user_version`).Scan(&v)
	if v != 2 {
		t.Fatalf("user_version = %d", v)
	}
	// Re-open is a no-op.
	st.Close()
	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	again.Close()
}
