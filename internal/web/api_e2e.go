package web

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/servrai/pms-dummy/internal/pms"
	"github.com/servrai/pms-dummy/internal/sign"
	"github.com/servrai/pms-dummy/internal/store"
)

// e2eReservationsPath is the programmatic create endpoint mobile e2e CI uses
// to seed a reservation that then lands in servr through the normal outbound
// events.
const e2eReservationsPath = "/api/e2e/reservations"

type e2eGuest struct {
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	Email       string `json:"email"`
	Phone       string `json:"phone"`
	Nationality string `json:"nationality"`
}

type e2eReservationRequest struct {
	HotelCode     string    `json:"hotel_code"`
	BookingRef    string    `json:"booking_ref"`
	Guest         *e2eGuest `json:"guest"`
	ArrivalDate   string    `json:"arrival_date"`
	DepartureDate string    `json:"departure_date"`
	Adults        *int64    `json:"adults"`
	Children      *int64    `json:"children"`
	RoomType      string    `json:"room_type"`
	Status        string    `json:"status"`
}

type e2eDelivery struct {
	Sync   string `json:"reservation.sync"`
	Record string `json:"reservation.record"`
}

type e2eReservationResponse struct {
	BookingRef    string      `json:"booking_ref"`
	ReservationID int64       `json:"reservation_id"`
	Status        string      `json:"status"`
	Delivered     bool        `json:"delivered"`
	Events        e2eDelivery `json:"events"`
}

// bookingRefPattern keeps refs URL-safe for /pms/reservations/{booking_ref}.
var bookingRefPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// trailingLine matches a "-<digits>" suffix, which would make the ref
// ambiguous with the "<number>-<line>" addressing (servr strips it too).
var trailingLine = regexp.MustCompile(`-\d+$`)

// handleE2EReservation: POST /api/e2e/reservations.
//
// Order of checks: body readable + hotel_code present (422, the key cannot
// be chosen without it) → hotel wired here (404) → signature over the raw
// body with that hotel's signing secret (401) → field validation (422) →
// create through the UI's service path → synchronous reservation.sync then
// reservation.record to servr → 201.
func (s *Server) handleE2EReservation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, []byte(`{"error":"POST only"}`))
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, []byte(`{"error":"unreadable body"}`))
		return
	}
	var req e2eReservationRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, mustJSON(map[string]any{"error": "body must be a JSON object: " + err.Error()}))
		return
	}
	hotel := strings.TrimSpace(req.HotelCode)
	if hotel == "" {
		writeJSON(w, http.StatusUnprocessableEntity, mustJSON(map[string]any{
			"error": "validation failed", "fields": map[string]string{"hotel_code": "required"},
		}))
		return
	}

	st, err := s.Store.Settings()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, []byte(`{"error":"settings unavailable"}`))
		return
	}
	// One pms-dummy instance is wired to exactly one servr hotel: the
	// configured hotel_code with a non-empty signing secret.
	if st.Secret == "" || hotel != st.HotelCode {
		writeJSON(w, http.StatusNotFound, []byte(`{"error":"unknown or unwired hotel"}`))
		return
	}
	if err := sign.Verify(st.Secret, r.Header.Get("x-servr-timestamp"), body,
		r.Header.Get("x-servr-signature"), time.Now()); err != nil {
		writeJSON(w, http.StatusUnauthorized, []byte(`{"error":"invalid signature"}`))
		return
	}

	resv, fields := e2eToReservation(&req)
	if len(fields) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, mustJSON(map[string]any{"error": "validation failed", "fields": fields}))
		return
	}
	existing, err := s.Store.ReservationByBookingRef(resv.ReservationNumber)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, []byte(`{"error":"lookup failed"}`))
		return
	}
	if existing != nil {
		writeJSON(w, http.StatusConflict, mustJSON(map[string]any{
			"error": "booking_ref already exists", "booking_ref": resv.ReservationNumber,
			"reservation_id": existing.ID,
		}))
		return
	}

	id, err := s.createReservation(resv)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, mustJSON(map[string]any{"error": err.Error()}))
		return
	}
	created, err := s.Store.ReservationByID(id)
	if err != nil || created == nil {
		writeJSON(w, http.StatusInternalServerError, []byte(`{"error":"reload failed"}`))
		return
	}

	// servr only creates bookings from a reservation.sync manifest; the
	// record then upserts the full guest fields. A record for a booking servr
	// has never seen is a 422 there, so it is skipped when the sync failed.
	ev := e2eDelivery{Sync: "failed", Record: "skipped"}
	syncOK, err := s.pushManifest()
	if err == nil && syncOK {
		ev.Sync = "delivered"
		ev.Record = "failed"
		if s.pushRecord(created) {
			ev.Record = "delivered"
		}
	} else {
		_ = s.Store.SetReservationSync(created.ID, "error")
	}

	writeJSON(w, http.StatusCreated, mustJSON(e2eReservationResponse{
		BookingRef:    created.ReservationNumber,
		ReservationID: created.ID,
		Status:        created.Status,
		Delivered:     ev.Sync == "delivered" && ev.Record == "delivered",
		Events:        ev,
	}))
}

// e2eToReservation maps and validates the request; fields maps each invalid
// JSON field to the reason.
func e2eToReservation(req *e2eReservationRequest) (*store.Reservation, map[string]string) {
	fields := map[string]string{}
	resv := &store.Reservation{LineNumber: 1, RoomQuantity: 1, Adults: 1}

	resv.ReservationNumber = strings.TrimSpace(req.BookingRef)
	if resv.ReservationNumber == "" {
		resv.ReservationNumber = newE2EBookingRef()
	} else if !bookingRefPattern.MatchString(resv.ReservationNumber) || trailingLine.MatchString(resv.ReservationNumber) {
		fields["booking_ref"] = "1-64 chars of [A-Za-z0-9_-], starting alphanumeric, not ending in -<digits>"
	}

	switch strings.ToLower(strings.TrimSpace(req.Status)) {
	case "", "reserved", "pending", "confirmed":
		resv.Status = pms.StatusReserved // the forms UI's new-reservation status
	default:
		fields["status"] = "only Reserved (aliases: pending, confirmed) can be created"
	}

	if req.Guest == nil {
		fields["guest"] = "required"
	} else {
		g := req.Guest
		resv.GuestFirstName = strings.TrimSpace(g.FirstName)
		resv.GuestSurname = strings.TrimSpace(g.LastName)
		resv.GuestEmail = strings.TrimSpace(g.Email)
		resv.GuestPhone = strings.TrimSpace(g.Phone)
		resv.NationCode = strings.ToUpper(strings.TrimSpace(g.Nationality))
		if resv.GuestFirstName == "" {
			fields["guest.first_name"] = "required"
		}
		if resv.GuestSurname == "" {
			fields["guest.last_name"] = "required"
		}
		if at := strings.Index(resv.GuestEmail, "@"); at < 1 || at == len(resv.GuestEmail)-1 {
			fields["guest.email"] = "required, must be an email address"
		}
		if resv.GuestPhone == "" {
			fields["guest.phone"] = "required"
		}
		if n := len(resv.NationCode); n != 0 && (n < 2 || n > 3) {
			fields["guest.nationality"] = "ISO country code (2-3 letters)"
		}
	}

	arr, errA := time.Parse("2006-01-02", req.ArrivalDate)
	if errA != nil {
		fields["arrival_date"] = "required, YYYY-MM-DD"
	}
	dep, errD := time.Parse("2006-01-02", req.DepartureDate)
	if errD != nil {
		fields["departure_date"] = "required, YYYY-MM-DD"
	}
	if errA == nil && errD == nil && dep.Before(arr) {
		fields["departure_date"] = "must be on or after arrival_date"
	}
	resv.ArrivalDate, resv.DepartureDate = req.ArrivalDate, req.DepartureDate

	if req.Adults != nil {
		if *req.Adults < 1 || *req.Adults > 99 {
			fields["adults"] = "1-99"
		}
		resv.Adults = *req.Adults
	}
	if req.Children != nil {
		if *req.Children < 0 || *req.Children > 99 {
			fields["children"] = "0-99"
		}
		resv.Children = *req.Children
	}
	resv.RoomCategory = strings.TrimSpace(req.RoomType)
	if len(resv.RoomCategory) > 64 {
		fields["room_type"] = "at most 64 chars"
	}
	return resv, fields
}

// newE2EBookingRef mints a unique, sortable ref like E2E1KX9Z3Q4A7F2.
func newE2EBookingRef() string {
	buf := make([]byte, 3)
	_, _ = rand.Read(buf)
	return "E2E" + strings.ToUpper(strconv.FormatInt(time.Now().UnixMilli(), 36)+hex.EncodeToString(buf))
}
