package pms

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/servrai/pms-dummy/internal/store"
)

// Outcome is pms-dummy's answer to one delivery.
type Outcome struct {
	Status        int
	Body          []byte
	Applied       bool // state mutated → caller may fire action.confirm
	ReservationID int64
}

// Apply runs the full inbound pipeline: idempotency dedupe on request_id,
// wire-address resolution, per-type guards with documented action_error_codes,
// the local state mutation, and action-log bookkeeping. It returns the exact
// HTTP response to send back to servr.
func Apply(st *store.Store, env *Envelope) Outcome {
	envJSON := store.PrettyJSON(env)

	// 1. Idempotency: replays answer with the previously recorded outcome.
	if prior, _ := st.LastActionByRequestID(env.RequestID); prior != nil && env.RequestID != "" {
		if prior.Status == "synced" || prior.Status == "error" {
			st.InsertAction(&store.Action{
				RequestID: env.RequestID, ActionType: env.ActionType, BookingRef: env.BookingRef,
				ReservationNumber: env.ReservationNumber, PayloadJSON: envJSON,
				Status: "duplicate", HTTPStatus: prior.HTTPStatus,
				ErrorCode: prior.ErrorCode, ErrorMessage: prior.ErrorMessage,
			})
			return replayOutcome(st, prior, env)
		}
	}

	rowID, _ := st.InsertAction(&store.Action{
		RequestID: env.RequestID, ActionType: env.ActionType, BookingRef: env.BookingRef,
		ReservationNumber: env.ReservationNumber, PayloadJSON: envJSON, Status: "created",
	})

	finish := func(o Outcome, errCode, errMsg string) Outcome {
		httpStatus := int64(o.Status)
		_ = st.UpdateActionOutcome(rowID, statusFor(o), int(httpStatus), errCode, errMsg)
		return o
	}
	finishOK := func(o Outcome) Outcome { return finish(o, "", "") }

	// 2. Resolve the PMS wire address (envelope first, payload Key fallback).
	number := env.ReservationNumber
	if number == "" {
		var k struct {
			Key KeyRef `json:"Key"`
		}
		if json.Unmarshal(env.Payload, &k) == nil {
			number = k.Key.ReservationNumber
		}
	}
	line := env.Line()
	r, err := st.ReservationByRef(number, line)
	if err != nil || r == nil {
		return finish(errOutcome("reservation_not_found",
			fmt.Sprintf("no reservation %s-%d in pms-dummy", number, line)), "reservation_not_found", "reservation not found")
	}

	switch env.ActionType {
	case ActionCheckIn:
		return finishOK(applyCheckIn(st, r, env))
	case ActionEdit:
		return finishOK(applyEdit(st, r, env))
	case ActionCheckout:
		return finishOK(applyCheckout(st, r, env))
	case ActionCancel:
		return finishOK(applyCancel(st, r, env))
	case ActionGetReservation:
		body, _ := json.Marshal(BuildRecord(r))
		return finishOK(Outcome{Status: 200, Body: body})
	default:
		return finish(errOutcomeBody(422, ActionErrorBody{Error: fmt.Sprintf("unknown action_type %q", env.ActionType)}),
			"", "unknown action_type")
	}
}

func statusFor(o Outcome) string {
	if o.Status >= 200 && o.Status < 300 {
		return "synced"
	}
	return "error"
}

func replayOutcome(st *store.Store, prior *store.Action, env *Envelope) Outcome {
	status := 200
	if prior.HTTPStatus != nil {
		status = int(*prior.HTTPStatus)
	}
	if prior.Status == "error" {
		return Outcome{Status: status, Body: errBody(ActionErrorBody{Error: orDefault(prior.ErrorMessage, "error"), ActionErrorCode: prior.ErrorCode})}
	}
	// A synced get_reservation replays the current record; other types synced
	// to an empty 2xx, matching what we answered the first time.
	if prior.ActionType == ActionGetReservation {
		if r, _ := st.ReservationByRef(prior.ReservationNumber, lineOf(prior)); r != nil {
			body, _ := json.Marshal(BuildRecord(r))
			return Outcome{Status: 200, Body: body}
		}
	}
	return Outcome{Status: 200, Body: []byte(`{}`)}
}

func lineOf(a *store.Action) int64 {
	if a.LineNumber != nil {
		return *a.LineNumber
	}
	return 1
}

func errOutcome(code, msg string) Outcome {
	return errOutcomeBody(422, ActionErrorBody{Error: msg, ActionErrorCode: code})
}

func errOutcomeBody(status int, b ActionErrorBody) Outcome {
	return Outcome{Status: status, Body: errBody(b)}
}

func errBody(b ActionErrorBody) []byte {
	out, _ := json.Marshal(b)
	return out
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// ── per-type application ──

func applyCheckIn(st *store.Store, r *store.Reservation, env *Envelope) Outcome {
	var p CheckInPayload
	if len(env.Payload) > 0 {
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return failResv(st, r, errOutcome("reservation_not_found", "unparseable check_in payload"))
		}
	}
	switch r.Status {
	case StatusCheckedIn:
		return failResv(st, r, errOutcome("reservation_already_checked_in",
			fmt.Sprintf("reservation %s already checked in", r.BookingRef())))
	case StatusCancelled:
		return failResv(st, r, errOutcome("superseded_by_check_in",
			fmt.Sprintf("reservation %s is cancelled", r.BookingRef())))
	case StatusCheckedOut:
		return failResv(st, r, errOutcome("stale_cancelled",
			fmt.Sprintf("reservation %s already checked out", r.BookingRef())))
	}
	// Room guard: moving into a room occupied by another checked-in stay.
	if p.RoomNumber != "" && p.RoomNumber != r.RoomNumber {
		if other, _ := st.ActiveReservationInRoom(p.RoomNumber, r.ID); other != 0 {
			return failResv(st, r, errOutcome("room_unavailable",
				fmt.Sprintf("room %s occupied by reservation #%d", p.RoomNumber, other)))
		}
	}
	oldRoom := r.RoomNumber
	if p.ArrivalDate != "" {
		r.ArrivalDate = p.ArrivalDate
	}
	if p.DepartureDate != "" {
		r.DepartureDate = p.DepartureDate
	}
	if p.RoomNumber != "" {
		r.RoomNumber = p.RoomNumber
	}
	r.GuestFirstName = orDefault(p.Guest.FirstName, r.GuestFirstName)
	r.GuestSurname = orDefault(p.Guest.Surname, r.GuestSurname)
	r.NationCode = orDefault(p.Guest.NationCode, r.NationCode)
	r.GuestEmail = orDefault(p.Guest.Contact.Email, r.GuestEmail)
	r.GuestPhone = orDefault(p.Guest.Contact.Phone, r.GuestPhone)
	if p.Occupancy.AdultCount > 0 {
		r.Adults = p.Occupancy.AdultCount
	}
	r.Children = p.Occupancy.ChildCount
	if p.RoomRate.Amount > 0 {
		r.RoomRate = p.RoomRate.Amount
	}
	r.RateCode = orDefault(p.RoomRate.RateCode, r.RateCode)
	r.GroupCode = orDefault(p.GroupCode, r.GroupCode)
	r.Remark = orDefault(p.Remark, r.Remark)
	now := store.Now()
	r.CheckedInAt = &now
	r.Status = StatusCheckedIn
	if err := st.UpdateReservation(r); err != nil {
		return errOutcomeBody(500, ActionErrorBody{Error: err.Error()})
	}
	_ = st.SetReservationSync(r.ID, "synced")
	if oldRoom != "" && oldRoom != r.RoomNumber {
		_ = st.SetRoomStatusByNumber(oldRoom, "vacant_clean_unchecked")
	}
	_ = st.SetRoomStatusByNumber(r.RoomNumber, "occupied_cleaned")
	return Outcome{Status: 200, Body: []byte(`{}`), Applied: true, ReservationID: r.ID}
}

func applyEdit(st *store.Store, r *store.Reservation, env *Envelope) Outcome {
	var p EditPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return failResv(st, r, errOutcome("reservation_not_found", "unparseable edit payload"))
	}
	if r.Status == StatusCancelled {
		return failResv(st, r, errOutcome("cancelled_by_staff",
			fmt.Sprintf("reservation %s is cancelled", r.BookingRef())))
	}
	oldRoom := r.RoomNumber
	for _, c := range p.Changes {
		var val string
		_ = json.Unmarshal(c.New, &val)
		applyChange(r, c.Field, val, c.New)
	}
	if err := st.UpdateReservation(r); err != nil {
		return errOutcomeBody(500, ActionErrorBody{Error: err.Error()})
	}
	_ = st.SetReservationSync(r.ID, "synced")
	if oldRoom != r.RoomNumber {
		if oldRoom != "" {
			_ = st.SetRoomStatusByNumber(oldRoom, "vacant_clean_unchecked")
		}
		if r.Status == StatusCheckedIn && r.RoomNumber != "" {
			_ = st.SetRoomStatusByNumber(r.RoomNumber, "occupied_cleaned")
		}
	}
	return Outcome{Status: 200, Body: []byte(`{}`), Applied: true, ReservationID: r.ID}
}

// applyChange maps one {field, new} triple onto the row. Returns false for
// fields pms-dummy does not carry (logged + still 2xx, per the contract).
func applyChange(r *store.Reservation, field, val string, raw json.RawMessage) bool {
	switch field {
	case "ArrivalDate":
		r.ArrivalDate = val
	case "DepartureDate":
		r.DepartureDate = val
	case "RoomNumber":
		r.RoomNumber = val
	case "Guest.FirstName":
		r.GuestFirstName = val
	case "Guest.Surname":
		r.GuestSurname = val
	case "Guest.Contact.Email":
		r.GuestEmail = val
	case "Guest.Contact.Phone":
		r.GuestPhone = val
	case "Guest.NationCode", "NationCode":
		r.NationCode = val
	case "Remark":
		r.Remark = val
	case "GroupCode":
		r.GroupCode = val
	case "Occupancy.AdultCount", "AdultCount":
		if n, err := strconv.ParseInt(val, 10, 64); err == nil {
			r.Adults = n
		}
	case "Occupancy.ChildCount", "ChildCount":
		if n, err := strconv.ParseInt(val, 10, 64); err == nil {
			r.Children = n
		}
	case "RoomRate.Amount":
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			r.RoomRate = f
		}
	case "RoomRate.RateCode", "RateCode":
		r.RateCode = val
	default:
		return false
	}
	return true
}

func applyCheckout(st *store.Store, r *store.Reservation, env *Envelope) Outcome {
	var p CheckoutPayload
	_ = json.Unmarshal(env.Payload, &p)
	switch r.Status {
	case StatusCancelled:
		return failResv(st, r, errOutcome("cancelled_by_staff",
			fmt.Sprintf("reservation %s is cancelled", r.BookingRef())))
	case StatusCheckedOut:
		return failResv(st, r, errOutcome("stale_cancelled",
			fmt.Sprintf("reservation %s already checked out", r.BookingRef())))
	case StatusReserved:
		return failResv(st, r, errOutcome("stale_cancelled",
			fmt.Sprintf("reservation %s is not checked in", r.BookingRef())))
	}
	now := store.Now()
	r.CheckedOutAt = &now
	by := orDefault(p.CheckedOutBy, "servr")
	r.CheckedOutBy = &by
	if p.RoomNumber != "" {
		r.RoomNumber = p.RoomNumber
	}
	r.Status = StatusCheckedOut
	if err := st.UpdateReservation(r); err != nil {
		return errOutcomeBody(500, ActionErrorBody{Error: err.Error()})
	}
	_ = st.SetReservationSync(r.ID, "synced")
	_ = st.SetRoomStatusByNumber(r.RoomNumber, "vacant_clean_unchecked")
	return Outcome{Status: 200, Body: []byte(`{}`), Applied: true, ReservationID: r.ID}
}

func applyCancel(st *store.Store, r *store.Reservation, env *Envelope) Outcome {
	var p CancelPayload
	_ = json.Unmarshal(env.Payload, &p)
	switch r.Status {
	case StatusCheckedOut:
		return failResv(st, r, errOutcome("stale_cancelled",
			fmt.Sprintf("reservation %s already checked out", r.BookingRef())))
	case StatusCancelled:
		return failResv(st, r, errOutcome("cancelled_by_staff",
			fmt.Sprintf("reservation %s already cancelled", r.BookingRef())))
	}
	now := store.Now()
	r.CancelledAt = &now
	code := p.CancelReason.Code
	r.CancelReasonCode = &code
	text := p.CancelReason.Text
	r.CancelReasonText = &text
	r.Status = StatusCancelled
	if err := st.UpdateReservation(r); err != nil {
		return errOutcomeBody(500, ActionErrorBody{Error: err.Error()})
	}
	_ = st.SetReservationSync(r.ID, "synced")
	_ = st.SetRoomStatusByNumber(r.RoomNumber, "vacant_clean_unchecked")
	return Outcome{Status: 200, Body: []byte(`{}`), Applied: true, ReservationID: r.ID}
}

// failResv marks the reservation's sync_status error alongside the action row.
func failResv(st *store.Store, r *store.Reservation, o Outcome) Outcome {
	_ = st.SetReservationSync(r.ID, "error")
	return o
}
