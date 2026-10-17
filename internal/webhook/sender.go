package webhook

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/servrai/pms-dummy/internal/sign"
	"github.com/servrai/pms-dummy/internal/store"
)

// Sender posts signed events to servr's inbound webhook
// (POST {servr_base}/{hotel_code}) and logs every attempt.
type Sender struct {
	Store  *store.Store
	Client *http.Client
}

// NewSender builds a sender that never follows redirects (a 3xx from servr
// counts as failure on their side; ours should surface it, not chase it).
// 60s total: servr processes a reservation.sync manifest synchronously — a
// few hundred lines against a well-populated hotel takes well over 15s.
func NewSender(st *store.Store) *Sender {
	return &Sender{
		Store: st,
		Client: &http.Client{
			Timeout: 60 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// TargetURL renders the outbound endpoint from current settings.
func (s *Sender) TargetURL(st store.Settings) string {
	return strings.TrimRight(st.ServrBaseURL, "/") + "/" + st.HotelCode
}

// Send marshals {"event","data"}, signs it, posts it, and records the attempt
// in outbound_events. The returned bool reports HTTP 2xx.
func (s *Sender) Send(event string, data any) bool {
	st, err := s.Store.Settings()
	if err != nil {
		return false
	}
	body, err := marshalEnvelope(event, data)
	if err != nil {
		s.log(&store.OutboundEvent{Event: event, URL: s.TargetURL(st), Error: err.Error()})
		return false
	}
	url := s.TargetURL(st)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sig := sign.Signature(st.Secret, ts, body)

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		s.log(&store.OutboundEvent{Event: event, URL: url, PayloadJSON: string(body), Error: err.Error()})
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-servr-timestamp", ts)
	req.Header.Set("x-servr-signature", sig)

	start := time.Now()
	resp, err := s.Client.Do(req)
	durMS := time.Since(start).Milliseconds()
	ev := &store.OutboundEvent{Event: event, URL: url, PayloadJSON: store.PrettyJSON(envelopeForLog(event, data)), DurationMS: durMS}
	if err != nil {
		ev.Error = err.Error()
		s.log(ev)
		return false
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	status := int64(resp.StatusCode)
	ev.ResponseStatus = &status
	ev.ResponseBody = string(respBody)
	s.log(ev)
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func (s *Sender) log(ev *store.OutboundEvent) {
	if ev.PayloadJSON == "" {
		ev.PayloadJSON = "{}"
	}
	_ = s.Store.InsertOutboundEvent(ev)
}

type envelope struct {
	Event string `json:"event"`
	Data  any    `json:"data"`
}

func marshalEnvelope(event string, data any) ([]byte, error) {
	return json.Marshal(envelope{Event: event, Data: data})
}

func envelopeForLog(event string, data any) any { return envelope{Event: event, Data: data} }
