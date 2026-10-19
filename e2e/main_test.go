package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

var baseURL = envOr("BASE_URL", "http://127.0.0.1:8080")

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// state carries the ordered subtests' shared fixtures. The suite runs against
// a persistent DB (the live dev instance), so everything is uniquified.
type state struct {
	t        *testing.T
	client   *http.Client
	noFollow *http.Client
	secret   string
	prefix   string
	room     string
	resvID   int64
	reqID    string
}

func (s *state) get(path string) (int, string) {
	s.t.Helper()
	resp, err := s.client.Get(baseURL + path)
	if err != nil {
		s.t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// postForm submits a form and follows the PRG redirect, returning the final
// status and body.
func (s *state) postForm(path string, vals url.Values) (int, string) {
	s.t.Helper()
	resp, err := s.client.PostForm(baseURL+path, vals)
	if err != nil {
		s.t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// postFormNoRedirect returns the immediate (303) response without following.
func (s *state) postFormNoRedirect(path string, vals url.Values) *http.Response {
	s.t.Helper()
	resp, err := s.noFollow.PostForm(baseURL+path, vals)
	if err != nil {
		s.t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	return resp
}

// do runs a prepared request through the plain (non-redirect-following)
// client and returns status + body.
func (s *state) do(req *http.Request) (int, string) {
	s.t.Helper()
	resp, err := s.noFollow.Do(req)
	if err != nil {
		s.t.Fatalf("%s %s: %v", req.Method, req.URL, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (s *state) signedJSON(method, path string, body any) (int, string) {
	s.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		s.t.Fatalf("marshal: %v", err)
	}
	req, err := signedRequest(method, baseURL+path, s.secret, raw)
	if err != nil {
		s.t.Fatalf("request: %v", err)
	}
	return s.do(req)
}

// settingsForm returns the complete integration-settings form with defaults,
// overridden by kv.
func (s *state) settingsForm(kv map[string]string) url.Values {
	v := url.Values{
		"servr_base_url":       {"https://api-dev.servr.ai/api/integration/webhook"},
		"hotel_code":           {"DUMMY"},
		"path_actions":         {"/actions"},
		"path_get_reservation": {"/pms/reservations/{booking_ref}"},
		"inbound_active":       {"on"},
		"confirm_after_apply":  {"on"},
		"signing_secret":       {""},
	}
	for k, val := range kv {
		v.Set(k, val)
	}
	return v
}

// setFault posts the fault-injection form (which carries the integration
// fields as hidden inputs). The outbound target stays on the run's own
// /dev/echo self-loop — resetting it to servr here would send the later
// subtests' events to the real dev API.
func (s *state) setFault(mode, code string) {
	s.t.Helper()
	v := url.Values{
		"servr_base_url":       {baseURL + "/dev"},
		"hotel_code":           {"echo"},
		"path_actions":         {"/actions"},
		"path_get_reservation": {"/pms/reservations/{booking_ref}"},
		"inbound_active":       {"on"},
		"fault_mode":           {mode},
		"fault_error_code":     {code},
	}
	if _, body := s.postForm("/settings", v); strings.Contains(body, "flash err") {
		s.t.Fatalf("setFault(%s) rejected", mode)
	}
}

func mustContain(t *testing.T, haystack, needle, what string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("%s: needle %q not found in response (%d bytes)", what, needle, len(haystack))
	}
}

func TestPolygon(t *testing.T) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	s := &state{
		t:        t,
		client:   &http.Client{Timeout: 120 * time.Second, Jar: jar},
		noFollow: &http.Client{Timeout: 120 * time.Second, Jar: jar, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }},
		prefix:   fmt.Sprintf("E2E%d", time.Now().UnixNano()%1e9),
	}
	s.room = s.prefix + "01"

	t.Run("health", func(t *testing.T) {
		status, body := s.get("/healthz")
		if status != 200 || body != "ok" {
			t.Fatalf("healthz = %d %q", status, body)
		}
	})

	t.Run("login page shape", func(t *testing.T) {
		status, body := s.get("/login")
		if status != 200 {
			t.Fatalf("login page = %d", status)
		}
		mustContain(t, body, "Enter as admin", "login page")
		mustContain(t, body, `disabled`, "login page (disabled inputs)")
	})

	t.Run("unauthenticated redirect", func(t *testing.T) {
		resp, err := s.noFollow.Get(baseURL + "/dashboard")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 303 || resp.Header.Get("Location") != "/login" {
			t.Fatalf("unauth dashboard = %d %s", resp.StatusCode, resp.Header.Get("Location"))
		}
	})

	t.Run("enter as admin", func(t *testing.T) {
		resp := s.postFormNoRedirect("/login", url.Values{"mode": {"admin"}})
		if resp.StatusCode != 303 || resp.Header.Get("Location") != "/dashboard" {
			t.Fatalf("login POST = %d %s", resp.StatusCode, resp.Header.Get("Location"))
		}
		// Without the session cookie the dashboard would 303 to /login (which
		// the following client would render as 200) — so assert no redirect.
		noSess := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
		bare, err := noSess.Get(baseURL + "/dashboard")
		if err != nil {
			t.Fatal(err)
		}
		bare.Body.Close()
		if bare.StatusCode != 303 {
			t.Fatalf("pre-login dashboard = %d, want 303 to /login", bare.StatusCode)
		}
		status, body := s.get("/dashboard")
		if status != 200 || strings.Contains(body, "Enter as admin") {
			t.Fatalf("dashboard = %d (login page leaked?)", status)
		}
		mustContain(t, body, "pms-webhook.html", "dashboard docs link (webhook)")
		mustContain(t, body, "pms-rpa.html", "dashboard docs link (RPA)")
	})

	t.Run("install known signing secret", func(t *testing.T) {
		s.secret = "e2e-" + s.prefix
		// Point the outbound endpoint at our own /dev/echo for the WHOLE run —
		// action.confirm callbacks fired by applied deliveries must not hit
		// the real servr dev API from a test.
		code, _ := s.postForm("/settings", s.settingsForm(map[string]string{
			"signing_secret": s.secret,
			"servr_base_url": baseURL + "/dev",
			"hotel_code":     "echo",
		}))
		if code != 200 {
			t.Fatalf("settings save = %d", code)
		}
		// A signed delivery with the new secret must now verify.
		status, _ := s.signedJSON("POST", "/actions", map[string]any{
			"request_id": s.prefix + "-probe", "action_type": "get_reservation",
			"booking_ref": "none-1", "reservationNumber": "none", "lineNumber": 1, "payload": nil,
		})
		if status != 422 {
			t.Fatalf("probe with new secret = %d (want 422 reservation_not_found, proving signature verified)", status)
		}
	})

	t.Run("create room", func(t *testing.T) {
		code, _ := s.postForm("/rooms/save", url.Values{
			"number": {s.room}, "status": {"vacant_clean_unchecked"}, "category": {"STD"}, "floor": {"1"},
		})
		if code != 200 {
			t.Fatalf("room save = %d", code)
		}
		_, body := s.get("/rooms")
		mustContain(t, body, s.room, "rooms list")
	})

	t.Run("create reservation", func(t *testing.T) {
		today := time.Now().UTC().Format("2006-01-02")
		tomorrow := time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")
		resp := s.postFormNoRedirect("/reservations/save", url.Values{
			"reservation_number": {s.prefix}, "line_number": {"1"},
			"guest_first_name": {"Test"}, "guest_surname": {"Guest"},
			"guest_email": {"test@example.com"}, "guest_phone": {"+1234567"}, "nation_code": {"ID"},
			"arrival_date": {today}, "departure_date": {tomorrow},
			"room_number": {s.room}, "adults": {"2"}, "children": {"0"}, "room_quantity": {"1"},
			"room_rate": {"150"}, "rate_code": {"BAR"}, "remark": {""}, "group_code": {""},
		})
		loc := resp.Header.Get("Location")
		if resp.StatusCode != 303 || !strings.Contains(loc, "id=") {
			t.Fatalf("reservation save = %d %s", resp.StatusCode, loc)
		}
		id, _ := strconv.ParseInt(loc[strings.Index(loc, "id=")+3:], 10, 64)
		s.resvID = id
		_, body := s.get("/reservations?id=" + strconv.FormatInt(id, 10))
		mustContain(t, body, "Reserved", "reservation view status")
	})

	t.Run("signed GET record", func(t *testing.T) {
		req, err := signedRequest("GET", baseURL+"/pms/reservations/"+s.prefix+"-1", s.secret, nil)
		if err != nil {
			t.Fatal(err)
		}
		status, body := s.do(req)
		if status != 200 {
			t.Fatalf("GET record = %d %s", status, body)
		}
		var rec struct {
			Key       struct{ ReservationNumber string } `json:"Key"`
			Status    string
			Occupancy struct{ AdultCount int } `json:"Occupancy"`
			Guest     struct{ Surname string }  `json:"Guest"`
		}
		if err := json.Unmarshal([]byte(body), &rec); err != nil {
			t.Fatalf("record not JSON: %v\n%s", err, body)
		}
		if rec.Key.ReservationNumber != s.prefix || rec.Status != "Reserved" ||
			rec.Occupancy.AdultCount != 2 || rec.Guest.Surname != "Guest" {
			t.Fatalf("record shape wrong: %s", body)
		}
	})

	s.reqID = s.prefix + "-checkin-1"

	t.Run("check_in delivery applies", func(t *testing.T) {
		status, body := s.signedJSON("POST", "/actions", map[string]any{
			"request_id": s.reqID, "action_type": "check_in", "booking_ref": s.prefix + "-1",
			"reservationNumber": s.prefix, "lineNumber": 1,
			"payload": map[string]any{
				"Key":         map[string]any{"ReservationNumber": s.prefix, "LineNumber": 1},
				"ArrivalDate": time.Now().UTC().Format("2006-01-02"),
				"DepartureDate": time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02"),
				"RoomNumber":   s.room,
				"Status":       "CheckedIn",
				"Guest":        map[string]any{"FirstName": "Test", "Surname": "Guest"},
				"Occupancy":    map[string]any{"AdultCount": 2, "ChildCount": 0},
				"RoomRate":     map[string]any{"Amount": 150, "CurrencyCode": "USD", "RateCode": "BAR"},
			},
			"comment_message": "Checked in in guestX (guest lane)",
		})
		if status != 200 {
			t.Fatalf("check_in = %d %s", status, body)
		}
		_, view := s.get("/reservations?id=" + strconv.FormatInt(s.resvID, 10))
		mustContain(t, view, "CheckedIn", "reservation checked in")
		_, rooms := s.get("/rooms")
		mustContain(t, rooms, "occupied_cleaned", "room flipped to occupied_cleaned")
		_, logBody := s.get("/logs/actions")
		mustContain(t, logBody, s.reqID, "actions log shows delivery")
	})

	t.Run("request_id replay is a duplicate", func(t *testing.T) {
		env := map[string]any{
			"request_id": s.reqID, "action_type": "check_in", "booking_ref": s.prefix + "-1",
			"reservationNumber": s.prefix, "lineNumber": 1, "payload": nil,
		}
		status, body := s.signedJSON("POST", "/actions", env)
		if status != 200 {
			t.Fatalf("replay = %d %s", status, body)
		}
		_, logBody := s.get("/logs/actions")
		mustContain(t, logBody, "duplicate", "replay logged as duplicate")
	})

	t.Run("signature gate", func(t *testing.T) {
		raw := []byte(`{"request_id":"` + s.prefix + `-bad","action_type":"get_reservation"}`)
		// flipped signature char
		req, _ := signedRequest("POST", baseURL+"/actions", s.secret, raw)
		sig := req.Header.Get("x-servr-signature")
		flipped := []rune(sig)
		if flipped[len(flipped)-1] == 'a' {
			flipped[len(flipped)-1] = 'b'
		} else {
			flipped[len(flipped)-1] = 'a'
		}
		req.Header.Set("x-servr-signature", string(flipped))
		if status, _ := s.do(req); status != 401 {
			t.Fatalf("bad signature = %d, want 401", status)
		}
		// missing headers
		req2, _ := http.NewRequest("POST", baseURL+"/actions", strings.NewReader(string(raw)))
		if status, _ := s.do(req2); status != 401 {
			t.Fatalf("unsigned = %d, want 401", status)
		}
		// stale timestamp with a CORRECT signature for that timestamp
		req3, _ := signedRequestAt("POST", baseURL+"/actions", s.secret, raw, time.Now().Add(-time.Hour).Unix())
		if status, _ := s.do(req3); status != 401 {
			t.Fatalf("stale = %d, want 401", status)
		}
	})

	t.Run("unknown reservation is a non-2xx error body", func(t *testing.T) {
		status, body := s.signedJSON("POST", "/actions", map[string]any{
			"request_id": s.prefix + "-nf", "action_type": "check_in",
			"reservationNumber": s.prefix + "X", "lineNumber": 1, "payload": nil,
		})
		if status == 200 {
			t.Fatalf("unknown reservation answered 2xx — servr would treat it as synced")
		}
		var e struct {
			Error           string `json:"error"`
			ActionErrorCode string `json:"action_error_code"`
		}
		if err := json.Unmarshal([]byte(body), &e); err != nil || e.ActionErrorCode != "reservation_not_found" {
			t.Fatalf("error body wrong: %s", body)
		}
	})

	t.Run("checkout frees the room", func(t *testing.T) {
		status, body := s.signedJSON("POST", "/actions", map[string]any{
			"request_id": s.prefix + "-co", "action_type": "checkout",
			"reservationNumber": s.prefix, "lineNumber": 1,
			"payload": map[string]any{
				"Key":          map[string]any{"ReservationNumber": s.prefix, "LineNumber": 1},
				"RoomNumber":   s.room, "CheckedOutAt": time.Now().UTC().Format(time.RFC3339),
				"CheckedOutBy": "guest", "Source": "quick_checkout",
			},
		})
		if status != 200 {
			t.Fatalf("checkout = %d %s", status, body)
		}
		_, view := s.get("/reservations?id=" + strconv.FormatInt(s.resvID, 10))
		mustContain(t, view, "CheckedOut", "reservation checked out")
		_, rooms := s.get("/rooms")
		mustContain(t, rooms, "vacant_clean_unchecked", "room freed")
	})

	t.Run("fault injection force_500", func(t *testing.T) {
		s.setFault("force_500", "")
		status, _ := s.signedJSON("POST", "/actions", map[string]any{
			"request_id": s.prefix + "-f1", "action_type": "get_reservation",
			"reservationNumber": s.prefix, "lineNumber": 1, "payload": nil,
		})
		if status != 500 {
			t.Fatalf("force_500 = %d, want 500", status)
		}
		s.setFault("normal", "")
		status, body := s.signedJSON("POST", "/actions", map[string]any{
			"request_id": s.prefix + "-f2", "action_type": "get_reservation",
			"reservationNumber": s.prefix, "lineNumber": 1, "payload": nil,
		})
		if status != 200 || !strings.Contains(body, `"ReservationNumber":"`+s.prefix+`"`) {
			t.Fatalf("recovered get_reservation = %d %s", status, body)
		}
	})

	t.Run("fault injection respond_error_code", func(t *testing.T) {
		s.setFault("respond_error_code", "room_unavailable")
		status, body := s.signedJSON("POST", "/actions", map[string]any{
			"request_id": s.prefix + "-f3", "action_type": "check_in",
			"reservationNumber": s.prefix, "lineNumber": 1, "payload": nil,
		})
		if status != 422 || !strings.Contains(body, "room_unavailable") {
			t.Fatalf("respond_error_code = %d %s", status, body)
		}
		s.setFault("normal", "")
	})

	t.Run("cancel error semantics", func(t *testing.T) {
		// Cancelling an already checked-out reservation → stale_cancelled.
		status, body := s.signedJSON("POST", "/actions", map[string]any{
			"request_id": s.prefix + "-c1", "action_type": "cancel_reservation",
			"reservationNumber": s.prefix, "lineNumber": 1,
			"payload": map[string]any{
				"Key":          map[string]any{"ReservationNumber": s.prefix, "LineNumber": 1},
				"CancelReason": map[string]any{"Code": "TEST", "Text": "e2e"},
			},
		})
		if status != 422 || !strings.Contains(body, "stale_cancelled") {
			t.Fatalf("cancel on checked-out = %d %s", status, body)
		}
		// A fresh line cancels cleanly.
		resp := s.postFormNoRedirect("/reservations/save", url.Values{
			"reservation_number": {s.prefix}, "line_number": {"2"},
			"guest_first_name": {"Second"}, "guest_surname": {"Line"},
			"arrival_date": {time.Now().UTC().Format("2006-01-02")},
			"departure_date": {time.Now().UTC().AddDate(0, 0, 2).Format("2006-01-02")},
			"room_number": {""}, "adults": {"1"}, "children": {"0"}, "room_quantity": {"1"},
			"room_rate": {"0"}, "rate_code": {""}, "remark": {""}, "group_code": {""},
		})
		loc := resp.Header.Get("Location")
		if resp.StatusCode != 303 {
			t.Fatalf("second reservation = %d", resp.StatusCode)
		}
		id := loc[strings.Index(loc, "id=")+3:]
		status, body = s.signedJSON("POST", "/actions", map[string]any{
			"request_id": s.prefix + "-c2", "action_type": "cancel_reservation",
			"reservationNumber": s.prefix, "lineNumber": 2,
			"payload": map[string]any{
				"Key":          map[string]any{"ReservationNumber": s.prefix, "LineNumber": 2},
				"CancelReason": map[string]any{"Code": "GUEST", "Text": "plans changed"},
			},
		})
		if status != 200 {
			t.Fatalf("clean cancel = %d %s", status, body)
		}
		_, view := s.get("/reservations?id=" + id)
		mustContain(t, view, "Cancelled", "second line cancelled")
		_ = strings.Index(view, "GUEST")
	})

	t.Run("outbound self-loop through the public URL", func(t *testing.T) {
		// The sender has been pointed at our own /dev/echo since the secret
		// install step; hotel_code "echo" completes the URL → {BASE}/dev/echo.
		if code, _ := s.postForm("/outbound/send", url.Values{"event": {"reservation.sync"}}); code != 200 {
			t.Fatalf("sync send = %d", code)
		}
		_, events := s.get("/logs/events")
		// The response body rides inside <pre>, so html/template escapes quotes.
		mustContain(t, events, "&#34;status&#34;:&#34;ok&#34;", "echo answered ok")
		mustContain(t, events, "reservation.sync", "events log shows sync")
		// Unsigned echo is rejected — proving the echo endpoint really gates.
		req, _ := http.NewRequest("POST", baseURL+"/dev/echo", strings.NewReader(`{}`))
		if status, _ := s.do(req); status != 401 {
			t.Fatalf("unsigned echo = %d, want 401", status)
		}
	})

	var apiResvID int64
	t.Run("e2e reservations API", func(t *testing.T) {
		// hotel_code is "echo" for the whole run (see the secret install step),
		// so the outbound sync + record land on our own signature-gated
		// /dev/echo instead of servr.
		body := map[string]any{
			"hotel_code":  "echo",
			"booking_ref": s.prefix + "API",
			"guest": map[string]any{
				"first_name": "Api", "last_name": "Guest",
				"email": "api@example.com", "phone": "+1555", "nationality": "ID",
			},
			"arrival_date":   time.Now().UTC().Format("2006-01-02"),
			"departure_date": time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02"),
			"adults":         2, "children": 0, "room_type": "STD",
		}
		status, resp := s.signedJSON("POST", "/api/e2e/reservations", body)
		if status != 201 {
			t.Fatalf("create = %d %s", status, resp)
		}
		var out struct {
			BookingRef    string `json:"booking_ref"`
			ReservationID int64  `json:"reservation_id"`
			Status        string `json:"status"`
			Delivered     bool   `json:"delivered"`
		}
		if err := json.Unmarshal([]byte(resp), &out); err != nil {
			t.Fatalf("response not JSON: %s", resp)
		}
		apiResvID = out.ReservationID
		if out.BookingRef != s.prefix+"API" || out.Status != "Reserved" || !out.Delivered {
			t.Fatalf("response = %s", resp)
		}
		_, view := s.get("/reservations?id=" + strconv.FormatInt(out.ReservationID, 10))
		mustContain(t, view, "STD", "API reservation room type")
		_, events := s.get("/logs/events")
		mustContain(t, events, "reservation.record", "events log shows record")

		if status, _ := s.signedJSON("POST", "/api/e2e/reservations", body); status != 409 {
			t.Fatalf("duplicate = %d, want 409", status)
		}
		body["hotel_code"] = "NOPE"
		if status, _ := s.signedJSON("POST", "/api/e2e/reservations", body); status != 404 {
			t.Fatalf("unknown hotel = %d, want 404", status)
		}
		body["hotel_code"] = "echo"
		body["arrival_date"] = "tomorrow"
		if status, _ := s.signedJSON("POST", "/api/e2e/reservations", body); status != 422 {
			t.Fatalf("invalid date = %d, want 422", status)
		}
		req, _ := http.NewRequest("POST", baseURL+"/api/e2e/reservations", strings.NewReader(`{"hotel_code":"echo"}`))
		if status, _ := s.do(req); status != 401 {
			t.Fatalf("unsigned = %d, want 401", status)
		}
	})

	t.Run("cleanup", func(t *testing.T) {
		s.postForm("/reservations/delete", url.Values{"id": {strconv.FormatInt(s.resvID, 10)}})
		if apiResvID > 0 {
			s.postForm("/reservations/delete", url.Values{"id": {strconv.FormatInt(apiResvID, 10)}})
		}
		s.postForm("/rooms/delete", url.Values{"id": {s.roomIDFromList()}})
		s.postForm("/settings", s.settingsForm(map[string]string{
			"servr_base_url": "https://api-dev.servr.ai/api/integration/webhook",
			"hotel_code":     "DUMMY",
		}))
	})
}

// roomIDFromList scrapes the rooms page for the row-id of s.room. The number
// cell precedes the actions cell, so the row's edit link follows the number.
func (s *state) roomIDFromList() string {
	s.t.Helper()
	_, body := s.get("/rooms")
	idx := strings.Index(body, ">"+s.room+"<")
	if idx < 0 {
		s.t.Fatalf("room %s not found for cleanup", s.room)
	}
	rest := body[idx:]
	rowStart := strings.Index(rest, `/rooms?edit=`)
	if rowStart < 0 {
		s.t.Fatalf("no edit link for room %s", s.room)
	}
	seg := rest[rowStart+len(`/rooms?edit=`):]
	end := strings.IndexAny(seg, `"`)
	if end < 0 {
		s.t.Fatalf("cannot parse room id for %s", s.room)
	}
	return seg[:end]
}
