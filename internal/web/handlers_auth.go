package web

import (
	"net/http"
	"time"
)

// handleLogin implements the owner-specified auth page: decorative disabled
// login/password inputs and a disabled Login button, plus the one working
// control — "Enter as admin".
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.render(w, "login", nil)
		return
	}
	_ = r.ParseForm()
	if r.PostFormValue("mode") != "admin" {
		s.render(w, "login", nil)
		return
	}
	token, err := s.Store.CreateSession()
	if err != nil {
		http.Error(w, "session create failed", http.StatusInternalServerError)
		return
	}
	// Secure only when the client actually reached us over TLS (directly, or
	// through the Cloudflare tunnel via X-Forwarded-Proto) — keeps local
	// http://127.0.0.1 testing working.
	secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   secure,
		MaxAge:   int((7 * 24 * time.Hour).Seconds()),
	})
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = s.Store.DeleteSession(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
