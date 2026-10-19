package web

import (
	"net/http"

	"github.com/servrai/pms-dummy/internal/pms"
	"github.com/servrai/pms-dummy/internal/store"
)

type outboundData struct {
	Reservations []store.Reservation
	TargetURL    string
	Msg          string
}

func (s *Server) handleOutbound(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListReservations()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	st, _ := s.Store.Settings()
	s.render(w, "outbound", outboundData{
		Reservations: list,
		TargetURL:    s.Sender.TargetURL(st),
		Msg:          r.URL.Query().Get("msg"),
	})
}

// handleOutboundSend fires one manual signed event — the polygon's side of
// "integrate with servr".
func (s *Server) handleOutboundSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	_ = r.ParseForm()
	event := r.PostFormValue("event")
	ref := r.PostFormValue("ref")
	requestID := r.PostFormValue("request_id")

	switch event {
	case "reservation.sync":
		if _, err := s.pushManifest(); err != nil {
			seeOther(w, r, "/outbound", "load failed: "+err.Error())
			return
		}
	case "reservation.record":
		resv, err := s.Store.ReservationByBookingRef(ref)
		if err != nil || resv == nil {
			seeOther(w, r, "/outbound", "no reservation for booking ref "+ref)
			return
		}
		s.pushRecord(resv)
	case "reservation.deleted":
		if ref == "" {
			seeOther(w, r, "/outbound", "booking ref is required")
			return
		}
		if resv, _ := s.Store.ReservationByBookingRef(ref); resv != nil {
			if err := s.Store.DeleteReservation(resv.ID); err != nil {
				seeOther(w, r, "/outbound", "local delete failed: "+err.Error())
				return
			}
		}
		s.Sender.Send("reservation.deleted", pms.DeletedData{BookingRef: ref})
	case "action.confirm":
		if requestID == "" {
			seeOther(w, r, "/outbound", "request_id is required")
			return
		}
		s.Sender.Send("action.confirm", pms.ConfirmData{RequestID: requestID})
	default:
		seeOther(w, r, "/outbound", "unknown event "+event)
		return
	}
	seeOther(w, r, "/logs/events", "event sent — see the log below")
}

// maybeSyncAfterMutation implements the optional sync_after_mutation toggle:
// after any UI change to rooms/reservations, push a full manifest.
func (s *Server) maybeSyncAfterMutation() {
	st, err := s.Store.Settings()
	if err != nil || !st.SyncAfterMutation {
		return
	}
	go func() { _, _ = s.pushManifest() }()
}
