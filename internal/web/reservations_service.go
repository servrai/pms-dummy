package web

import (
	"fmt"

	"github.com/servrai/pms-dummy/internal/pms"
	"github.com/servrai/pms-dummy/internal/store"
)

// The reservation service: the one code path every reservation creation and
// every outbound reservation push goes through, whichever surface (forms UI,
// /outbound, /api/e2e/reservations) triggered it.

// validateReservation enforces the create/edit invariants.
func validateReservation(resv *store.Reservation) error {
	if resv.ReservationNumber == "" {
		return errStr("reservation number is required")
	}
	if resv.ArrivalDate == "" || resv.DepartureDate == "" || resv.DepartureDate < resv.ArrivalDate {
		return errStr("arrival/departure dates invalid (departure must be ≥ arrival)")
	}
	return nil
}

// createReservation validates and inserts a new reservation, returning its
// local id. Outbound pushes are the caller's choice (the form honours the
// sync_after_mutation toggle; the e2e API pushes synchronously).
func (s *Server) createReservation(resv *store.Reservation) (int64, error) {
	if err := validateReservation(resv); err != nil {
		return 0, err
	}
	id, err := s.Store.CreateReservation(resv)
	if err != nil {
		return 0, fmt.Errorf("create failed: %w", err)
	}
	return id, nil
}

// pushManifest sends a full reservation.sync manifest (every reservation +
// room). It is what creates new bookings on servr's side: servr's
// reservation.record handler only upserts bookings it already knows.
func (s *Server) pushManifest() (bool, error) {
	resvs, err := s.Store.ListReservations()
	if err != nil {
		return false, err
	}
	rooms, _ := s.Store.ListRooms()
	return s.Sender.Send("reservation.sync", pms.BuildManifest(resvs, rooms)), nil
}

// pushRecord sends one reservation.record and stamps the local sync status
// with the outcome.
func (s *Server) pushRecord(resv *store.Reservation) bool {
	ok := s.Sender.Send("reservation.record", pms.BuildRecord(resv))
	if ok {
		_ = s.Store.SetReservationSync(resv.ID, "synced")
	} else {
		_ = s.Store.SetReservationSync(resv.ID, "error")
	}
	return ok
}
