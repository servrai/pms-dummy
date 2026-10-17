// Package web is pms-dummy's HTTP layer: the signature-gated PMS API, the
// session-gated forms-only UI, and the glue between them.
package web

import (
	"net/http"
	"strings"

	"github.com/servrai/pms-dummy/internal/store"
	"github.com/servrai/pms-dummy/internal/webhook"
)

const sessionCookie = "pms_session"

// Server routes between the PMS API (configurable paths, signature-gated)
// and the fixed UI tree (session-gated).
type Server struct {
	Store   *store.Store
	Sender  *webhook.Sender
	Gate    *webhook.Gate
	ownBase string
	ui      *http.ServeMux
	tpl     map[string]*templateT
}

// New assembles the server.
func New(st *store.Store, ownBase string) *Server {
	s := &Server{
		Store:   st,
		Sender:  webhook.NewSender(st),
		Gate:    &webhook.Gate{Store: st},
		ownBase: ownBase,
		tpl:     parseTemplates(),
	}
	s.ui = s.buildUI()
	return s
}

// ServeHTTP dispatches: health first, then the configurable PMS API paths,
// then the fixed UI tree.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		s.handleHealth(w, r)
		return
	}
	st, err := s.Store.Settings()
	if err != nil {
		http.Error(w, "settings unavailable", http.StatusInternalServerError)
		return
	}
	if r.URL.Path == st.PathActions && r.Method == http.MethodPost {
		s.Gate.Wrap(s.handleActions)(w, r)
		return
	}
	if ref, ok := matchGetPath(st.PathGetReservation, r.URL.Path); ok && r.Method == http.MethodGet {
		s.Gate.Wrap(s.handleGetRecord(ref))(w, r)
		return
	}
	if r.URL.Path == "/dev/echo" && r.Method == http.MethodPost {
		s.Gate.Wrap(s.handleEcho)(w, r)
		return
	}
	s.ui.ServeHTTP(w, r)
}

// matchGetPath matches a stored "/prefix/{booking_ref}/suffix" pattern.
func matchGetPath(pattern, path string) (string, bool) {
	if !strings.Contains(pattern, "{booking_ref}") {
		return "", false
	}
	i := strings.Index(pattern, "{booking_ref}")
	prefix, suffix := pattern[:i], pattern[i+len("{booking_ref}"):]
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}
	ref := path[len(prefix) : len(path)-len(suffix)]
	if ref == "" || strings.Contains(ref, "/") {
		return "", false
	}
	return ref, true
}

// buildUI wires the fixed UI tree; everything except /login and /static
// sits behind the session middleware.
func (s *Server) buildUI() *http.ServeMux {
	mux := http.NewServeMux()
	auth := func(next http.HandlerFunc) http.HandlerFunc { return s.requireSession(next) }

	mux.HandleFunc("/login", s.handleLogin)
	mux.HandleFunc("/static/", s.serveStatic)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
	})
	mux.HandleFunc("/logout", auth(s.handleLogout))
	mux.HandleFunc("/dashboard", auth(s.handleDashboard))
	mux.HandleFunc("/rooms", auth(s.handleRooms))
	mux.HandleFunc("/rooms/save", auth(s.handleRoomSave))
	mux.HandleFunc("/rooms/delete", auth(s.handleRoomDelete))
	mux.HandleFunc("/reservations", auth(s.handleReservations))
	mux.HandleFunc("/reservations/save", auth(s.handleReservationSave))
	mux.HandleFunc("/reservations/delete", auth(s.handleReservationDelete))
	mux.HandleFunc("/reservations/check-in", auth(s.handleReservationCheckIn))
	mux.HandleFunc("/reservations/checkout", auth(s.handleReservationCheckout))
	mux.HandleFunc("/reservations/cancel", auth(s.handleReservationCancel))
	mux.HandleFunc("/settings", auth(s.handleSettings))
	mux.HandleFunc("/settings/secret/regenerate", auth(s.handleSecretRegenerate))
	mux.HandleFunc("/settings/secret", auth(s.handleSecretShow))
	mux.HandleFunc("/logs/actions", auth(s.handleActionsLog))
	mux.HandleFunc("/logs/events", auth(s.handleEventsLog))
	mux.HandleFunc("/outbound", auth(s.handleOutbound))
	mux.HandleFunc("/outbound/send", auth(s.handleOutboundSend))
	return mux
}

func (s *Server) requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		ok, err := s.Store.ValidSession(c.Value)
		if err != nil || !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}
