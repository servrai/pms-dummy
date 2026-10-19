package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/servrai/pms-dummy/internal/sign"
	"github.com/servrai/pms-dummy/internal/store"
)

const (
	testHotel  = "POL0001"
	testSecret = "e2e-unit-secret"
)

// fakeServr stands in for servr's inbound webhook: it verifies the outbound
// signature and records each event envelope it accepted.
type fakeServr struct {
	mu     sync.Mutex
	paths  []string
	events []map[string]any
}

func (f *fakeServr) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := sign.Verify(testSecret, r.Header.Get("x-servr-timestamp"), body,
			r.Header.Get("x-servr-signature"), time.Now()); err != nil {
			t.Errorf("outbound event not signed with the hotel secret: %v", err)
			w.WriteHeader(401)
			return
		}
		var env map[string]any
		_ = json.Unmarshal(body, &env)
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.Path)
		f.events = append(f.events, env)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"status":"ok"}`)
	}
}

func newE2EServer(t *testing.T) (*Server, *store.Store, *fakeServr) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	fs := &fakeServr{}
	servr := httptest.NewServer(fs.handler(t))
	t.Cleanup(servr.Close)
	cfg := store.DefaultSettings()
	cfg.ServrBaseURL = servr.URL + "/api/integration/webhook"
	cfg.HotelCode = testHotel
	if err := st.SaveSettings(cfg); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSecret(testSecret); err != nil {
		t.Fatal(err)
	}
	return New(st, ""), st, fs
}

func validBody(ref string) map[string]any {
	return map[string]any{
		"hotel_code":  testHotel,
		"booking_ref": ref,
		"guest": map[string]any{
			"first_name": "Ada", "last_name": "Lovelace",
			"email": "ada@example.com", "phone": "+15550100", "nationality": "gb",
		},
		"arrival_date": "2026-10-20", "departure_date": "2026-10-22",
		"adults": 2, "children": 1, "room_type": "DLX",
	}
}

func signedPost(t *testing.T, srv http.Handler, secret string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, e2eReservationsPath, bytes.NewReader(raw))
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req.Header.Set("x-servr-timestamp", ts)
	req.Header.Set("x-servr-signature", sign.Signature(secret, ts, raw))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestE2EReservationCreatesAndEmits(t *testing.T) {
	srv, st, fs := newE2EServer(t)

	rec := signedPost(t, srv, testSecret, validBody("MOB1"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d %s", rec.Code, rec.Body)
	}
	var resp e2eReservationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.BookingRef != "MOB1" || resp.Status != "Reserved" || resp.ReservationID == 0 ||
		!resp.Delivered || resp.Events.Sync != "delivered" || resp.Events.Record != "delivered" {
		t.Fatalf("response = %+v", resp)
	}

	got, _ := st.ReservationByID(resp.ReservationID)
	if got == nil || got.GuestSurname != "Lovelace" || got.NationCode != "GB" || got.Adults != 2 ||
		got.Children != 1 || got.RoomCategory != "DLX" || got.SyncStatus != "synced" {
		t.Fatalf("stored reservation = %+v", got)
	}

	// sync (creates the booking in servr) then record (full fields), both to
	// {servr_base}/{hotel_code}.
	if len(fs.events) != 2 || fs.events[0]["event"] != "reservation.sync" || fs.events[1]["event"] != "reservation.record" {
		t.Fatalf("events = %v", fs.events)
	}
	for _, p := range fs.paths {
		if p != "/api/integration/webhook/"+testHotel {
			t.Fatalf("event posted to %s", p)
		}
	}
	data := fs.events[1]["data"].(map[string]any)
	key := data["Key"].(map[string]any)
	guest := data["Guest"].(map[string]any)
	if key["ReservationNumber"] != "MOB1" || guest["Surname"] != "Lovelace" || data["RoomCategoryCode"] != "DLX" {
		t.Fatalf("record = %v", data)
	}

	// Duplicate booking_ref is a conflict, not a second row.
	if rec := signedPost(t, srv, testSecret, validBody("MOB1")); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate = %d %s", rec.Code, rec.Body)
	}
}

func TestE2EReservationGeneratesBookingRef(t *testing.T) {
	srv, _, _ := newE2EServer(t)
	body := validBody("")
	delete(body, "booking_ref")
	delete(body, "room_type")
	refs := map[string]bool{}
	for i := 0; i < 2; i++ {
		rec := signedPost(t, srv, testSecret, body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d %s", rec.Code, rec.Body)
		}
		var resp e2eReservationResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		if !strings.HasPrefix(resp.BookingRef, "E2E") || refs[resp.BookingRef] {
			t.Fatalf("generated ref %q (seen: %v)", resp.BookingRef, refs)
		}
		refs[resp.BookingRef] = true
	}
}

func TestE2EReservationBadSignature(t *testing.T) {
	srv, st, fs := newE2EServer(t)

	if rec := signedPost(t, srv, "wrong-secret", validBody("BAD1")); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong secret = %d", rec.Code)
	}
	raw, _ := json.Marshal(validBody("BAD2"))
	unsigned := httptest.NewRecorder()
	srv.ServeHTTP(unsigned, httptest.NewRequest(http.MethodPost, e2eReservationsPath, bytes.NewReader(raw)))
	if unsigned.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned = %d", unsigned.Code)
	}
	// Signed body, then tampered: the MAC covers the raw bytes.
	req := httptest.NewRequest(http.MethodPost, e2eReservationsPath,
		bytes.NewReader(bytes.Replace(raw, []byte("BAD2"), []byte("BAD3"), 1)))
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req.Header.Set("x-servr-timestamp", ts)
	req.Header.Set("x-servr-signature", sign.Signature(testSecret, ts, raw))
	tampered := httptest.NewRecorder()
	srv.ServeHTTP(tampered, req)
	if tampered.Code != http.StatusUnauthorized {
		t.Fatalf("tampered = %d", tampered.Code)
	}

	if list, _ := st.ListReservations(); len(list) != 0 || len(fs.events) != 0 {
		t.Fatalf("rejected requests had effects: %d rows, %d events", len(list), len(fs.events))
	}
}

func TestE2EReservationUnknownHotel(t *testing.T) {
	srv, st, _ := newE2EServer(t)
	body := validBody("UNK1")
	body["hotel_code"] = "NOPE001"
	if rec := signedPost(t, srv, testSecret, body); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown hotel = %d %s", rec.Code, rec.Body)
	}
	// Wired hotel code but no signing secret = not wired.
	if err := st.SetSecret(""); err != nil {
		t.Fatal(err)
	}
	if rec := signedPost(t, srv, "", validBody("UNK2")); rec.Code != http.StatusNotFound {
		t.Fatalf("unwired hotel = %d %s", rec.Code, rec.Body)
	}
}

func TestE2EReservationValidation(t *testing.T) {
	srv, st, fs := newE2EServer(t)
	cases := map[string]func(map[string]any){
		"missing guest":         func(b map[string]any) { delete(b, "guest") },
		"missing last_name":     func(b map[string]any) { b["guest"].(map[string]any)["last_name"] = "" },
		"bad email":             func(b map[string]any) { b["guest"].(map[string]any)["email"] = "nope" },
		"bad arrival date":      func(b map[string]any) { b["arrival_date"] = "20/10/2026" },
		"departure before":      func(b map[string]any) { b["departure_date"] = "2026-10-19" },
		"zero adults":           func(b map[string]any) { b["adults"] = 0 },
		"negative children":     func(b map[string]any) { b["children"] = -1 },
		"unsupported status":    func(b map[string]any) { b["status"] = "CheckedOut" },
		"ref with line suffix":  func(b map[string]any) { b["booking_ref"] = "ABC-2" },
		"ref with slash":        func(b map[string]any) { b["booking_ref"] = "A/B" },
		"missing hotel_code":    func(b map[string]any) { delete(b, "hotel_code") },
		"wrong type for adults": func(b map[string]any) { b["adults"] = "two" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			body := validBody("VAL1")
			mutate(body)
			rec := signedPost(t, srv, testSecret, body)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d %s", rec.Code, rec.Body)
			}
		})
	}
	if list, _ := st.ListReservations(); len(list) != 0 || len(fs.events) != 0 {
		t.Fatalf("invalid requests had effects: %d rows, %d events", len(list), len(fs.events))
	}
}

func TestE2EReservationServrDown(t *testing.T) {
	srv, st, _ := newE2EServer(t)
	cfg, _ := st.Settings()
	cfg.ServrBaseURL = "http://127.0.0.1:1/unreachable"
	if err := st.SaveSettings(cfg); err != nil {
		t.Fatal(err)
	}
	rec := signedPost(t, srv, testSecret, validBody("DOWN1"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d %s", rec.Code, rec.Body)
	}
	var resp e2eReservationResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Delivered || resp.Events.Sync != "failed" || resp.Events.Record != "skipped" {
		t.Fatalf("response = %+v", resp)
	}
	if got, _ := st.ReservationByID(resp.ReservationID); got == nil || got.SyncStatus != "error" {
		t.Fatalf("stored = %+v", got)
	}
}
