package web

import (
	"net/http"
)

type dashboardData struct {
	OwnBase             string
	InboundTotal        int
	InboundSynced       int
	InboundErrored      int
	LastInbound         string
	OutboundTotal       int
	LastOutbound        string
	LastOutboundStatus  int
	ResvLocal           int
	ResvSynced          int
	ResvError           int
	RoomTotal           int
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	inTotal, inSynced, inErrored, lastInbound, err := s.Store.ActionStats()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	outTotal, lastOutbound, lastOutboundStatus, err := s.Store.OutboundStats()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	local, synced, errored, err := s.Store.ReservationSyncStats()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rooms, _ := s.Store.ListRooms()
	s.render(w, "dashboard", dashboardData{
		OwnBase:            s.ownBase,
		InboundTotal:       inTotal,
		InboundSynced:      inSynced,
		InboundErrored:     inErrored,
		LastInbound:        lastInbound,
		OutboundTotal:      outTotal,
		LastOutbound:       lastOutbound,
		LastOutboundStatus: lastOutboundStatus,
		ResvLocal:          local,
		ResvSynced:         synced,
		ResvError:          errored,
		RoomTotal:          len(rooms),
	})
}
