package web

import (
	"net/http"

	"github.com/servrai/pms-dummy/internal/store"
)

const logLimit = 200

func (s *Server) handleActionsLog(w http.ResponseWriter, r *http.Request) {
	actions, err := s.Store.ListActions(logLimit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if actions == nil {
		actions = []store.Action{}
	}
	s.render(w, "actions_log", struct{ Actions []store.Action }{Actions: actions})
}

func (s *Server) handleEventsLog(w http.ResponseWriter, r *http.Request) {
	events, err := s.Store.ListOutboundEvents(logLimit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if events == nil {
		events = []store.OutboundEvent{}
	}
	s.render(w, "events_log", struct{ Events []store.OutboundEvent }{Events: events})
}
