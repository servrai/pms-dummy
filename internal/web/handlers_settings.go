package web

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"

	"github.com/servrai/pms-dummy/internal/pms"
	"github.com/servrai/pms-dummy/internal/store"
)

type settingsData struct {
	Settings store.Settings
	OwnBase  string
	Msg      string
	Err      string
	ModeList []string
	Codes    []string
}

// reservedUIPaths are fixed by the app; the actions path must not shadow them.
var reservedUIPaths = map[string]bool{
	"/": true, "/login": true, "/logout": true, "/dashboard": true,
	"/rooms": true, "/rooms/save": true, "/rooms/delete": true,
	"/reservations": true, "/reservations/save": true, "/reservations/delete": true,
	"/reservations/check-in": true, "/reservations/checkout": true, "/reservations/cancel": true,
	"/settings": true, "/settings/secret": true, "/settings/secret/regenerate": true,
	"/logs/actions": true, "/logs/events": true,
	"/outbound": true, "/outbound/send": true,
	"/dev/echo": true, "/healthz": true, "/static": true,
}

var faultModes = []string{"normal", "force_500", "timeout", "delay", "respond_error_code"}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		st, err := s.Store.Settings()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.render(w, "settings", settingsData{
			Settings: st, OwnBase: s.ownBase, Msg: r.URL.Query().Get("msg"),
			ModeList: faultModes, Codes: pms.ActionErrorCodes,
		})
		return
	}

	_ = r.ParseForm()
	current, err := s.Store.Settings()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	st := current
	st.ServrBaseURL = strings.TrimSpace(r.PostFormValue("servr_base_url"))
	st.HotelCode = strings.TrimSpace(r.PostFormValue("hotel_code"))
	st.PathActions = strings.TrimSpace(r.PostFormValue("path_actions"))
	st.PathGetReservation = strings.TrimSpace(r.PostFormValue("path_get_reservation"))
	st.InboundActive = r.PostFormValue("inbound_active") == "on"
	st.ConfirmAfterApply = r.PostFormValue("confirm_after_apply") == "on"
	st.SyncAfterMutation = r.PostFormValue("sync_after_mutation") == "on"
	// The page posts two forms (integration + fault injection); an absent
	// fault field means "not this form", so keep the current values.
	if v := r.PostFormValue("fault_mode"); v != "" {
		st.FaultMode = v
	}
	if v := r.PostFormValue("fault_error_code"); v != "" {
		st.FaultErrorCode = v
	}
	if v := r.PostFormValue("fault_delay_ms"); v != "" {
		st.FaultDelayMS = atoiDefault(v, st.FaultDelayMS)
	}

	if err := validateSettings(st); err != nil {
		s.renderSettingsError(w, r, st, err.Error())
		return
	}
	if err := s.Store.SaveSettings(st); err != nil {
		s.renderSettingsError(w, r, st, "save failed: "+err.Error())
		return
	}
	// Optional known-secret paste (handy when wiring servr with a chosen
	// secret instead of a generated one).
	if secret := strings.TrimSpace(r.PostFormValue("signing_secret")); secret != "" {
		_ = s.Store.SetSecret(secret)
	}
	seeOther(w, r, "/settings", "settings saved")
}

func (s *Server) renderSettingsError(w http.ResponseWriter, r *http.Request, st store.Settings, msg string) {
	w.WriteHeader(http.StatusUnprocessableEntity)
	s.render(w, "settings", settingsData{
		Settings: st, OwnBase: s.ownBase, Err: msg,
		ModeList: faultModes, Codes: pms.ActionErrorCodes,
	})
}

func validateSettings(st store.Settings) error {
	if st.ServrBaseURL == "" || !strings.HasPrefix(st.ServrBaseURL, "http") {
		return errStr("servr base URL must be an http(s) URL")
	}
	if st.HotelCode == "" || strings.Contains(st.HotelCode, "/") {
		return errStr("hotel code is required (no slashes)")
	}
	if !strings.HasPrefix(st.PathActions, "/") || len(st.PathActions) < 2 || reservedUIPaths[st.PathActions] {
		return errStr("deliveries path must start with / and not collide with UI routes")
	}
	if strings.Contains(st.PathActions, "{") {
		return errStr("deliveries path must not contain placeholders")
	}
	if strings.Count(st.PathGetReservation, "{booking_ref}") != 1 || !strings.HasPrefix(st.PathGetReservation, "/") {
		return errStr("reservations path must start with / and contain exactly one {booking_ref}")
	}
	if strings.ContainsAny(strings.ReplaceAll(st.PathGetReservation, "{booking_ref}", ""), "{}") {
		return errStr("reservations path may only use the {booking_ref} placeholder")
	}
	switch st.FaultMode {
	case "normal", "force_500", "timeout", "delay", "respond_error_code":
	default:
		return errStr("invalid fault mode")
	}
	validCode := false
	for _, c := range pms.ActionErrorCodes {
		if st.FaultErrorCode == c {
			validCode = true
		}
	}
	if !validCode {
		return errStr("invalid fault error code")
	}
	return nil
}

func atoiDefault(s string, fallback int) int {
	if n, err := strconv.Atoi(s); err == nil && n >= 0 {
		return n
	}
	return fallback
}

type errStr string

func (e errStr) Error() string { return string(e) }

func (s *Server) handleSecretRegenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		http.Error(w, "entropy failure", http.StatusInternalServerError)
		return
	}
	secret := hex.EncodeToString(buf)
	_ = s.Store.SetSecret(secret)
	_ = s.Store.SetFlashSecret(secret)
	http.Redirect(w, r, "/settings/secret", http.StatusSeeOther)
}

func (s *Server) handleSecretShow(w http.ResponseWriter, r *http.Request) {
	secret, err := s.Store.ConsumeFlashSecret()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, "secret", struct {
		Secret string
		Has    bool
	}{Secret: secret, Has: secret != ""})
}
