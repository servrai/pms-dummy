package web

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/servrai/pms-dummy/internal/pms"
	"github.com/servrai/pms-dummy/internal/store"
)

func writeJSON(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(body)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	io.WriteString(w, "ok")
}

// handleActions is servr's delivery endpoint. The Gate has already verified
// the signature and restored the raw body.
func (s *Server) handleActions(w http.ResponseWriter, r *http.Request) {
	st, err := s.Store.Settings()
	if err != nil {
		writeJSON(w, 500, []byte(`{"error":"settings unavailable"}`))
		return
	}

	// Fault injection (polygon): exercise servr's retry/backoff semantics.
	switch st.FaultMode {
	case "force_500":
		s.logFaultedDelivery(r, 500, "force_500")
		writeJSON(w, 500, []byte(`{"error":"injected fault: force_500"}`))
		return
	case "timeout":
		s.logFaultedDelivery(r, 500, "timeout")
		time.Sleep(65 * time.Second) // exceed servr's client timeout
		writeJSON(w, 500, []byte(`{"error":"injected fault: timeout"}`))
		return
	case "respond_error_code":
		s.logFaultedDelivery(r, 422, "respond_error_code:"+st.FaultErrorCode)
		writeJSON(w, 422, mustJSON(pms.ActionErrorBody{Error: "injected fault", ActionErrorCode: st.FaultErrorCode}))
		return
	case "delay":
		time.Sleep(time.Duration(st.FaultDelayMS) * time.Millisecond)
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		writeJSON(w, 400, []byte(`{"error":"unreadable body"}`))
		return
	}
	var env pms.Envelope
	if err := json.Unmarshal(body, &env); err != nil || env.RequestID == "" || env.ActionType == "" {
		writeJSON(w, 422, []byte(`{"error":"malformed delivery envelope"}`))
		return
	}

	outcome := pms.Apply(s.Store, &env)
	if outcome.Applied && st.ConfirmAfterApply {
		reqID := env.RequestID
		go s.Sender.Send("action.confirm", pms.ConfirmData{RequestID: reqID})
	}
	writeJSON(w, outcome.Status, outcome.Body)
}

// logFaultedDelivery records a fault-injected refusal in the actions log so
// the UI shows what servr experienced.
func (s *Server) logFaultedDelivery(r *http.Request, httpStatus int, fault string) {
	var env pms.Envelope
	body, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	_ = json.Unmarshal(body, &env)
	_, _ = s.Store.InsertAction(&store.Action{
		RequestID: env.RequestID, ActionType: env.ActionType, BookingRef: env.BookingRef,
		ReservationNumber: env.ReservationNumber,
		PayloadJSON:       store.PrettyJSON(env),
		Status:            "error", HTTPStatus: int64Ptr(httpStatus),
		ErrorMessage:      "fault injected: " + fault,
	})
}

func int64Ptr(i int) *int64 { v := int64(i); return &v }

// handleGetRecord answers servr's reservation read. 404 = deleted (servr then
// cancels pre-arrival bookings locally).
func (s *Server) handleGetRecord(ref string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resv, err := s.Store.ReservationByBookingRef(ref)
		if err != nil {
			writeJSON(w, 500, []byte(`{"error":"lookup failed"}`))
			return
		}
		if resv == nil {
			writeJSON(w, 404, []byte(`{"error":"reservation not found"}`))
			return
		}
		writeJSON(w, 200, mustJSON(pms.BuildRecord(resv)))
	}
}

// handleEcho is the self-loop target: it verifies the sender's signature with
// the same secret (via the Gate) and answers like servr would.
func (s *Server) handleEcho(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	var env struct {
		Event string `json:"event"`
	}
	_ = json.Unmarshal(body, &env)
	writeJSON(w, 200, mustJSON(map[string]any{"status": "ok", "echo": true, "event": env.Event}))
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte(`{"error":"marshal failure"}`)
	}
	return b
}
