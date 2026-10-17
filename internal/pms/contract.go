// Package pms mirrors the servr PMS webhook wire contract: delivery envelopes,
// action payloads, the reservation.record dictionary and the reservation.sync
// manifest. Field names follow https://api-dev.servr.ai/docs/guides/pms-webhook.html.
package pms

import "encoding/json"

// Action types servr delivers to POST {base}/actions.
const (
	ActionCheckIn       = "check_in"
	ActionEdit          = "edit_reservation"
	ActionCancel        = "cancel_reservation"
	ActionCheckout      = "checkout"
	ActionGetReservation = "get_reservation"
)

// ActionErrorCodes are the documented per-type error codes.
var ActionErrorCodes = []string{
	"reservation_not_found",
	"room_unavailable",
	"reservation_already_checked_in",
	"superseded_by_check_in",
	"cancelled_by_staff",
	"stale_cancelled",
}

// Reservation statuses on the wire (PmsStatusMap targets).
const (
	StatusReserved  = "Reserved"
	StatusCheckedIn = "CheckedIn"
	StatusCheckedOut = "CheckedOut"
	StatusCancelled = "Cancelled"
)

// Envelope is servr's delivery wrapper. request_id is the idempotency key;
// reservationNumber + lineNumber are the PMS wire address (booking_ref is
// servr's internal key).
type Envelope struct {
	RequestID         string          `json:"request_id"`
	ActionType        string          `json:"action_type"`
	BookingRef        string          `json:"booking_ref"`
	ReservationNumber string          `json:"reservationNumber"`
	LineNumber        *int64          `json:"lineNumber"`
	Payload           json.RawMessage `json:"payload"`
	CommentMessage    string          `json:"comment_message"`
}

// Line resolves the wire address: envelope first, payload Key fallback, 1 when
// neither carries a line.
func (e *Envelope) Line() int64 {
	if e.LineNumber != nil && *e.LineNumber >= 1 {
		return *e.LineNumber
	}
	var k struct {
		Key struct {
			LineNumber *int64 `json:"LineNumber"`
		} `json:"Key"`
	}
	if json.Unmarshal(e.Payload, &k) == nil && k.Key.LineNumber != nil && *k.Key.LineNumber >= 1 {
		return *k.Key.LineNumber
	}
	return 1
}

// ActionErrorBody is the documented failure body. It must ship with a NON-2xx
// status — servr treats any 2xx as synced regardless of the body.
type ActionErrorBody struct {
	Error           string `json:"error"`
	ActionErrorCode string `json:"action_error_code,omitempty"`
}

// KeyRef is the wire address inside payloads and records.
type KeyRef struct {
	ReservationNumber string `json:"ReservationNumber"`
	LineNumber        *int64 `json:"LineNumber"`
}

// CheckInPayload is the full-record check_in delivery (subset pms-dummy
// consumes; unknown fields are ignored by the decoder).
type CheckInPayload struct {
	Key           KeyRef   `json:"Key"`
	ArrivalDate   string   `json:"ArrivalDate"`
	DepartureDate string   `json:"DepartureDate"`
	RoomNumber    string   `json:"RoomNumber"`
	Status        string   `json:"Status"`
	Guest         struct {
		FirstName string `json:"FirstName"`
		Surname   string `json:"Surname"`
		NationCode string `json:"NationCode"`
		Contact   struct {
			Email string `json:"Email"`
			Phone string `json:"Phone"`
		} `json:"Contact"`
	} `json:"Guest"`
	Occupancy struct {
		AdultCount int64 `json:"AdultCount"`
		ChildCount int64 `json:"ChildCount"`
	} `json:"Occupancy"`
	RoomRate struct {
		Amount       float64 `json:"Amount"`
		CurrencyCode string  `json:"CurrencyCode"`
		RateCode     string  `json:"RateCode"`
	} `json:"RoomRate"`
	GroupCode   string `json:"GroupCode"`
	Remark      string `json:"Remark"`
	CheckedInAt string `json:"CheckedInAt"`
}

// EditChange is one {field, previous, new} triple.
type EditChange struct {
	Field    string          `json:"field"`
	Previous json.RawMessage `json:"previous"`
	New      json.RawMessage `json:"new"`
}

// EditPayload carries the accumulated diff.
type EditPayload struct {
	Changes []EditChange `json:"changes"`
}

// CheckoutPayload is the checkout delivery.
type CheckoutPayload struct {
	Key          KeyRef  `json:"Key"`
	RoomNumber   string  `json:"RoomNumber"`
	CheckedOutAt string  `json:"CheckedOutAt"`
	CheckedOutBy string  `json:"CheckedOutBy"`
	Source       string  `json:"Source"`
}

// CancelPayload is the cancellation delivery.
type CancelPayload struct {
	Key         KeyRef `json:"Key"`
	RoomNumber  string `json:"RoomNumber"`
	CancelledAt string `json:"CancelledAt"`
	CancelledBy string `json:"CancelledBy"`
	CancelReason struct {
		Code string `json:"Code"`
		Text string `json:"Text"`
	} `json:"CancelReason"`
}
