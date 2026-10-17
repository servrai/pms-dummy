// Package webhook holds the two halves of the servr integration transport:
// the inbound signature gate and the outbound signed sender.
package webhook

import (
	"bytes"
	"io"
	"net/http"
	"time"

	"github.com/servrai/pms-dummy/internal/sign"
	"github.com/servrai/pms-dummy/internal/store"
)

// Gate verifies inbound servr requests: HMAC signature over the raw body,
// ±5 min timestamp window, master-switch. It re-reads settings per request
// so secret rotations and the active toggle apply immediately.
type Gate struct{ Store *store.Store }

// Wrap returns a handler that enforces the signature before next runs.
func (g *Gate) Wrap(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		st, err := g.Store.Settings()
		if err != nil {
			http.Error(w, `{"error":"settings unavailable"}`, http.StatusInternalServerError)
			return
		}
		if !st.InboundActive {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, `{"error":"integration inactive"}`)
			return
		}
		// Verify over the RAW bytes — never a re-marshaled body.
		body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if err != nil {
			http.Error(w, `{"error":"unreadable body"}`, http.StatusBadRequest)
			return
		}
		if err := sign.Verify(st.Secret, r.Header.Get("x-servr-timestamp"), body,
			r.Header.Get("x-servr-signature"), time.Now()); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"error":"invalid signature"}`)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		next(w, r)
	}
}
