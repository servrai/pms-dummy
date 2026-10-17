package pms

import (
	"github.com/servrai/pms-dummy/internal/store"
)

// Record is the reservation.record dictionary — the answer to servr's GET
// read and the body of the reservation.record event. Only fields pms-dummy
// stores are populated; "omitted never wipes" on servr's side, so a thinner
// record is always safe.
type Record struct {
	Key                    KeyRef   `json:"Key"`
	ArrivalDate            string   `json:"ArrivalDate"`
	DepartureDate          string   `json:"DepartureDate"`
	RoomNumber             *string  `json:"RoomNumber"`
	Status                 string   `json:"Status"`
	ReservationStatusSource string  `json:"ReservationStatusSource"`
	Guest                  RecordGuest `json:"Guest"`
	Occupancy              RecordOccupancy `json:"Occupancy"`
	RoomRate               RecordRoomRate  `json:"RoomRate"`
	Remark                 string   `json:"Remark"`
	GroupCode              string   `json:"GroupCode"`
	CheckedInAt            *string  `json:"CheckedInAt,omitempty"`
	CheckedOutAt           *string  `json:"CheckedOutAt,omitempty"`
	CancelledAt            *string  `json:"CancelledAt,omitempty"`
	CancelReason           *RecordCancelReason `json:"CancelReason,omitempty"`
}

// RecordGuest is the guest block of the record dictionary.
type RecordGuest struct {
	FirstName  string `json:"FirstName"`
	Surname    string `json:"Surname"`
	RenderedName string `json:"RenderedName"`
	NationCode string `json:"NationCode"`
	Contact    RecordContact `json:"Contact"`
}

// RecordContact is the contact block.
type RecordContact struct {
	Email string `json:"Email"`
	Phone string `json:"Phone"`
}

// RecordOccupancy is the occupancy block.
type RecordOccupancy struct {
	AdultCount  int64 `json:"AdultCount"`
	ChildCount  int64 `json:"ChildCount"`
	RoomQuantity int64 `json:"RoomQuantity"`
}

// RecordRoomRate is the rate block.
type RecordRoomRate struct {
	Amount       float64 `json:"Amount"`
	CurrencyCode string  `json:"CurrencyCode"`
	RateCode     string  `json:"RateCode"`
}

// RecordCancelReason echoes the stored cancellation reason.
type RecordCancelReason struct {
	Code string `json:"Code"`
	Text string `json:"Text"`
}

// BuildRecord renders a stored reservation as the wire record dictionary.
func BuildRecord(r *store.Reservation) Record {
	line := r.LineNumber
	var room *string
	if r.RoomNumber != "" {
		room = &r.RoomNumber
	}
	rec := Record{
		Key:                     KeyRef{ReservationNumber: r.ReservationNumber, LineNumber: &line},
		ArrivalDate:             r.ArrivalDate,
		DepartureDate:           r.DepartureDate,
		RoomNumber:              room,
		Status:                  r.Status,
		ReservationStatusSource: "pms",
		Guest: RecordGuest{
			FirstName:    r.GuestFirstName,
			Surname:      r.GuestSurname,
			RenderedName: r.GuestName(),
			NationCode:   r.NationCode,
			Contact:      RecordContact{Email: r.GuestEmail, Phone: r.GuestPhone},
		},
		Occupancy: RecordOccupancy{AdultCount: r.Adults, ChildCount: r.Children, RoomQuantity: r.RoomQuantity},
		RoomRate:  RecordRoomRate{Amount: r.RoomRate, RateCode: r.RateCode},
		Remark:    r.Remark,
		GroupCode: r.GroupCode,
	}
	if r.CheckedInAt != nil {
		rec.CheckedInAt = r.CheckedInAt
	}
	if r.CheckedOutAt != nil {
		rec.CheckedOutAt = r.CheckedOutAt
	}
	if r.CancelledAt != nil {
		rec.CancelledAt = r.CancelledAt
		rec.CancelReason = &RecordCancelReason{}
		if r.CancelReasonCode != nil {
			rec.CancelReason.Code = *r.CancelReasonCode
		}
		if r.CancelReasonText != nil {
			rec.CancelReason.Text = *r.CancelReasonText
		}
	}
	return rec
}

// ManifestLine is one reservation in a reservation.sync push.
type ManifestLine struct {
	ReservationNumber string  `json:"ReservationNumber"`
	GuestName         string  `json:"GuestName"`
	ArrivalDate       string  `json:"ArrivalDate"`
	DepartureDate     string  `json:"DepartureDate"`
	RoomNumber        string  `json:"RoomNumber,omitempty"`
	RoomQuantity      int64   `json:"RoomQuantity,omitempty"`
	AdultCount        int64   `json:"AdultCount,omitempty"`
	ChildCount        int64   `json:"ChildCount,omitempty"`
	RoomRateAmount    float64 `json:"RoomRateAmount,omitempty"`
	RateCode          string  `json:"RateCode,omitempty"`
	Remark            string  `json:"Remark,omitempty"`
	GroupCode         string  `json:"GroupCode,omitempty"`
}

// RoomLine is one inventory row in a reservation.sync push.
type RoomLine struct {
	RoomNumber   string `json:"RoomNumber"`
	RoomStatus   string `json:"RoomStatus"`
	RoomCategory string `json:"RoomCategory,omitempty"`
	FloorNumber  int64  `json:"FloorNumber,omitempty"`
}

// SyncManifest is the reservation.sync data block.
type SyncManifest struct {
	Reservations []ManifestLine `json:"reservations"`
	Rooms        []RoomLine     `json:"rooms"`
}

// BuildManifest renders every reservation + room as a full sync manifest.
func BuildManifest(res []store.Reservation, rooms []store.Room) SyncManifest {
	m := SyncManifest{Reservations: []ManifestLine{}, Rooms: []RoomLine{}}
	for i := range res {
		r := &res[i]
		m.Reservations = append(m.Reservations, ManifestLine{
			ReservationNumber: r.ReservationNumber,
			GuestName:         r.GuestName(),
			ArrivalDate:       r.ArrivalDate,
			DepartureDate:     r.DepartureDate,
			RoomNumber:        r.RoomNumber,
			RoomQuantity:      r.RoomQuantity,
			AdultCount:        r.Adults,
			ChildCount:        r.Children,
			RoomRateAmount:    r.RoomRate,
			RateCode:          r.RateCode,
			Remark:            r.Remark,
			GroupCode:         r.GroupCode,
		})
	}
	for _, rm := range rooms {
		m.Rooms = append(m.Rooms, RoomLine{
			RoomNumber:   rm.Number,
			RoomStatus:   rm.Status,
			RoomCategory: rm.Category,
			FloorNumber:  rm.Floor,
		})
	}
	return m
}

// DeletedData is the reservation.deleted data block.
type DeletedData struct {
	BookingRef string `json:"booking_ref"`
}

// ConfirmData is the action.confirm data block.
type ConfirmData struct {
	RequestID string  `json:"request_id"`
	Error     *string `json:"error"`
}
