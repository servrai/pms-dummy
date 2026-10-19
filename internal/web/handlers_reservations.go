package web

import (
	"net/http"
	"strconv"

	"github.com/servrai/pms-dummy/internal/pms"
	"github.com/servrai/pms-dummy/internal/store"
)

type reservationsData struct {
	Reservations []store.Reservation
	View         *store.Reservation
	Edit         *store.Reservation
	FormNew      bool
	Rooms        []store.Room
	Msg          string
}

func (s *Server) handleReservations(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListReservations()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rooms, _ := s.Store.ListRooms()
	data := reservationsData{Reservations: list, Rooms: rooms, Msg: r.URL.Query().Get("msg")}
	if r.URL.Query().Get("form") == "new" {
		data.FormNew = true
	}
	if id, err := strconv.ParseInt(r.URL.Query().Get("edit"), 10, 64); err == nil && id > 0 {
		if resv, err := s.Store.ReservationByID(id); err == nil && resv != nil {
			data.Edit = resv
		}
	}
	if id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64); err == nil && id > 0 {
		if resv, err := s.Store.ReservationByID(id); err == nil && resv != nil {
			data.View = resv
			s.render(w, "reservation_view", data)
			return
		}
	}
	s.render(w, "reservations", data)
}

// reservationFromForm fills the mutable columns from the create/edit form.
func reservationFromForm(r *http.Request, base *store.Reservation) *store.Reservation {
	v := r.PostFormValue
	resv := *base
	resv.ReservationNumber = v("reservation_number")
	resv.LineNumber, _ = strconv.ParseInt(v("line_number"), 10, 64)
	if resv.LineNumber < 1 {
		resv.LineNumber = 1
	}
	resv.GuestFirstName = v("guest_first_name")
	resv.GuestSurname = v("guest_surname")
	resv.GuestEmail = v("guest_email")
	resv.GuestPhone = v("guest_phone")
	resv.NationCode = v("nation_code")
	resv.ArrivalDate = v("arrival_date")
	resv.DepartureDate = v("departure_date")
	resv.RoomNumber = v("room_number")
	resv.RoomCategory = v("room_category")
	resv.Adults, _ = strconv.ParseInt(v("adults"), 10, 64)
	if resv.Adults < 1 {
		resv.Adults = 1
	}
	resv.Children, _ = strconv.ParseInt(v("children"), 10, 64)
	if resv.Children < 0 {
		resv.Children = 0
	}
	resv.RoomQuantity, _ = strconv.ParseInt(v("room_quantity"), 10, 64)
	if resv.RoomQuantity < 1 {
		resv.RoomQuantity = 1
	}
	resv.RoomRate, _ = strconv.ParseFloat(v("room_rate"), 64)
	resv.RateCode = v("rate_code")
	resv.Remark = v("remark")
	resv.GroupCode = v("group_code")
	return &resv
}

func (s *Server) handleReservationSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	_ = r.ParseForm()
	var base store.Reservation
	id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	creating := id == 0
	if creating {
		base.Status = pms.StatusReserved
	} else {
		existing, err := s.Store.ReservationByID(id)
		if err != nil || existing == nil {
			seeOther(w, r, "/reservations", "reservation not found")
			return
		}
		base = *existing
	}
	resv := reservationFromForm(r, &base)
	if creating {
		newID, err := s.createReservation(resv)
		if err != nil {
			seeOther(w, r, "/reservations", err.Error())
			return
		}
		id = newID
	} else {
		if err := validateReservation(resv); err != nil {
			seeOther(w, r, "/reservations", err.Error())
			return
		}
		if err := s.Store.UpdateReservation(resv); err != nil {
			seeOther(w, r, "/reservations", "update failed: "+err.Error())
			return
		}
	}
	s.maybeSyncAfterMutation()
	seeOther(w, r, "/reservations?id="+strconv.FormatInt(id, 10), "reservation saved")
}

func (s *Server) handleReservationDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	_ = r.ParseForm()
	id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	if id > 0 {
		_ = s.Store.DeleteReservation(id)
	}
	s.maybeSyncAfterMutation()
	seeOther(w, r, "/reservations", "reservation deleted")
}

// localCheckIn / localCheckout / localCancel are the UI-side state buttons —
// the polygon's own staff actions, mirroring what servr asks us to do via
// /actions deliveries.
func (s *Server) handleReservationCheckIn(w http.ResponseWriter, r *http.Request) {
	resv := s.loadReservation(w, r)
	if resv == nil {
		return
	}
	if resv.Status != pms.StatusReserved {
		seeOther(w, r, viewPath(resv.ID), "only Reserved reservations can be checked in")
		return
	}
	now := store.Now()
	resv.CheckedInAt = &now
	resv.Status = pms.StatusCheckedIn
	if err := s.Store.UpdateReservation(resv); err == nil {
		_ = s.Store.SetRoomStatusByNumber(resv.RoomNumber, "occupied_cleaned")
	}
	s.maybeSyncAfterMutation()
	seeOther(w, r, viewPath(resv.ID), "checked in locally")
}

func (s *Server) handleReservationCheckout(w http.ResponseWriter, r *http.Request) {
	resv := s.loadReservation(w, r)
	if resv == nil {
		return
	}
	if resv.Status != pms.StatusCheckedIn {
		seeOther(w, r, viewPath(resv.ID), "only CheckedIn reservations can be checked out")
		return
	}
	now := store.Now()
	resv.CheckedOutAt = &now
	by := "pms-dummy staff"
	resv.CheckedOutBy = &by
	resv.Status = pms.StatusCheckedOut
	if err := s.Store.UpdateReservation(resv); err == nil {
		_ = s.Store.SetRoomStatusByNumber(resv.RoomNumber, "vacant_clean_unchecked")
	}
	s.maybeSyncAfterMutation()
	seeOther(w, r, viewPath(resv.ID), "checked out locally")
}

func (s *Server) handleReservationCancel(w http.ResponseWriter, r *http.Request) {
	resv := s.loadReservation(w, r)
	if resv == nil {
		return
	}
	if resv.Status == pms.StatusCancelled || resv.Status == pms.StatusCheckedOut {
		seeOther(w, r, viewPath(resv.ID), "reservation already "+resv.Status)
		return
	}
	_ = r.ParseForm()
	now := store.Now()
	resv.CancelledAt = &now
	code := r.PostFormValue("cancel_reason_code")
	resv.CancelReasonCode = &code
	text := r.PostFormValue("cancel_reason_text")
	resv.CancelReasonText = &text
	resv.Status = pms.StatusCancelled
	if err := s.Store.UpdateReservation(resv); err == nil {
		_ = s.Store.SetRoomStatusByNumber(resv.RoomNumber, "vacant_clean_unchecked")
	}
	s.maybeSyncAfterMutation()
	seeOther(w, r, viewPath(resv.ID), "cancelled locally")
}

func (s *Server) loadReservation(w http.ResponseWriter, r *http.Request) *store.Reservation {
	_ = r.ParseForm()
	id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	if id <= 0 {
		http.Error(w, "missing id", http.StatusBadRequest)
		return nil
	}
	resv, err := s.Store.ReservationByID(id)
	if err != nil || resv == nil {
		http.Error(w, "reservation not found", http.StatusNotFound)
		return nil
	}
	return resv
}

func viewPath(id int64) string { return "/reservations?id=" + strconv.FormatInt(id, 10) }
