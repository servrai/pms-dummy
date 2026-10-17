package pms

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/servrai/pms-dummy/internal/store"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func mustReservation(t *testing.T, st *store.Store, number string, line int64) *store.Reservation {
	t.Helper()
	id, err := st.CreateReservation(&store.Reservation{
		ReservationNumber: number, LineNumber: line, Status: StatusReserved,
		GuestFirstName: "Ada", GuestSurname: "Lovelace",
		ArrivalDate: "2026-10-18", DepartureDate: "2026-10-20",
		Adults: 1, RoomQuantity: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	r, _ := st.ReservationByID(id)
	return r
}

func env(action, reqID, number string, line int64, payload any) *Envelope {
	raw, _ := json.Marshal(payload)
	l := line
	return &Envelope{
		RequestID: reqID, ActionType: action, BookingRef: number + "-1",
		ReservationNumber: number, LineNumber: &l, Payload: raw,
	}
}

func decodeErr(t *testing.T, o Outcome) (string, string) {
	t.Helper()
	var e ActionErrorBody
	if err := json.Unmarshal(o.Body, &e); err != nil {
		t.Fatalf("error body not JSON: %s", o.Body)
	}
	return e.Error, e.ActionErrorCode
}

func TestApplyLifecycle(t *testing.T) {
	st := testStore(t)
	mustReservation(t, st, "L1", 1)

	// Unknown wire address.
	o := Apply(st, env(ActionCheckIn, "q0", "NOPE", 1, nil))
	if o.Status == 200 {
		t.Fatal("unknown reservation must not be 2xx")
	}
	if _, code := decodeErr(t, o); code != "reservation_not_found" {
		t.Fatalf("code = %s", code)
	}

	// check_in applies the payload and flips the room.
	if _, err := st.CreateRoom("101", "vacant_clean_unchecked", "STD", 1); err != nil {
		t.Fatal(err)
	}
	o = Apply(st, env(ActionCheckIn, "q1", "L1", 1, map[string]any{
		"Key":         map[string]any{"ReservationNumber": "L1", "LineNumber": 1},
		"RoomNumber":  "101",
		"Guest":       map[string]any{"FirstName": "Ada", "Surname": "Lovelace"},
		"Occupancy":   map[string]any{"AdultCount": 2, "ChildCount": 1},
		"ArrivalDate": "2026-10-18", "DepartureDate": "2026-10-20",
	}))
	if o.Status != 200 || !o.Applied {
		t.Fatalf("check_in = %d %s", o.Status, o.Body)
	}
	after, _ := st.ReservationByRef("L1", 1)
	if after.Status != StatusCheckedIn || after.RoomNumber != "101" || after.Children != 1 {
		t.Fatalf("reservation after check_in: %+v", after)
	}
	room, _ := st.RoomByNumber("101")
	if room.Status != "occupied_cleaned" {
		t.Fatalf("room after check_in: %s", room.Status)
	}

	// Replay: same request_id answers the recorded outcome without reapplying.
	first := after.CheckedInAt
	o = Apply(st, env(ActionCheckIn, "q1", "L1", 1, nil))
	if o.Status != 200 {
		t.Fatalf("replay = %d %s", o.Status, o.Body)
	}
	after, _ = st.ReservationByRef("L1", 1)
	if after.CheckedInAt == nil || *after.CheckedInAt != *first {
		t.Fatal("replay re-applied the mutation")
	}

	// Second check-in with a FRESH request_id → guarded.
	o = Apply(st, env(ActionCheckIn, "q2", "L1", 1, nil))
	if _, code := decodeErr(t, o); code != "reservation_already_checked_in" {
		t.Fatalf("double check_in code = %s", code)
	}

	// edit_reservation applies changes.
	o = Apply(st, env(ActionEdit, "q3", "L1", 1, map[string]any{
		"changes": []map[string]any{
			{"field": "Guest.Surname", "previous": "Lovelace", "new": "Byron"},
			{"field": "RoomRate.Amount", "previous": "0", "new": "175.5"},
			{"field": "Mystery.Field", "previous": nil, "new": "x"},
		},
	}))
	if o.Status != 200 {
		t.Fatalf("edit = %d %s", o.Status, o.Body)
	}
	after, _ = st.ReservationByRef("L1", 1)
	if after.GuestSurname != "Byron" || after.RoomRate != 175.5 {
		t.Fatalf("edit not applied: %+v", after)
	}

	// checkout → room freed.
	o = Apply(st, env(ActionCheckout, "q4", "L1", 1, map[string]any{
		"CheckedOutBy": "guest", "RoomNumber": "101",
	}))
	if o.Status != 200 {
		t.Fatalf("checkout = %d %s", o.Status, o.Body)
	}
	room, _ = st.RoomByNumber("101")
	if room.Status != "vacant_clean_unchecked" {
		t.Fatalf("room after checkout: %s", room.Status)
	}

	// cancel after checkout → stale_cancelled.
	o = Apply(st, env(ActionCancel, "q5", "L1", 1, map[string]any{}))
	if _, code := decodeErr(t, o); code != "stale_cancelled" {
		t.Fatalf("cancel-after-checkout code = %s", code)
	}

	// get_reservation answers with the record dictionary.
	o = Apply(st, env(ActionGetReservation, "q6", "L1", 1, nil))
	if o.Status != 200 {
		t.Fatalf("get_reservation = %d", o.Status)
	}
	var rec Record
	if err := json.Unmarshal(o.Body, &rec); err != nil || rec.Key.ReservationNumber != "L1" {
		t.Fatalf("record = %s err=%v", o.Body, err)
	}
}

func TestApplyRoomUnavailable(t *testing.T) {
	st := testStore(t)
	if _, err := st.CreateRoom("201", "vacant_clean_unchecked", "STD", 2); err != nil {
		t.Fatal(err)
	}
	occupant := mustReservation(t, st, "R1", 1)
	occupant.Status = StatusCheckedIn
	occupant.RoomNumber = "201"
	_ = st.UpdateReservation(occupant)
	_ = st.SetRoomStatusByNumber("201", "occupied_cleaned")

	victim := mustReservation(t, st, "R2", 1)
	_ = victim
	o := Apply(st, env(ActionCheckIn, "q10", "R2", 1, map[string]any{"RoomNumber": "201"}))
	if _, code := decodeErr(t, o); code != "room_unavailable" {
		t.Fatalf("code = %s (body %s)", code, o.Body)
	}
}

func TestBuildRecordShape(t *testing.T) {
	r := mustReservation(t, testStore(t), "S1", 3)
	rec := BuildRecord(r)
	b, _ := json.Marshal(rec)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	for _, k := range []string{"Key", "ArrivalDate", "DepartureDate", "RoomNumber", "Status",
		"ReservationStatusSource", "Guest", "Occupancy", "RoomRate", "Remark", "GroupCode"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("record missing %s: %s", k, b)
		}
	}
	if rec.Key.LineNumber == nil || *rec.Key.LineNumber != 3 {
		t.Fatalf("line number = %v", rec.Key.LineNumber)
	}
}
