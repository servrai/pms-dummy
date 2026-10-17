// Package sign implements the servr PMS webhook signature scheme in both
// directions: signing outbound requests and verifying inbound ones.
//
//	Signature = "sha256=" + hex(HMAC-SHA256(secret, timestamp + "." + rawBody))
//
// The signature is computed over the RAW body bytes with the unix-seconds
// timestamp and a literal "." prefixed — never over a re-marshaled body.
// GET requests are signed over the empty body. Mirrors servr's
// app/Support/IntegrationWebhook.php.
package sign

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"time"
)

// ReplayWindow matches servr's ±5 minute staleness check on the timestamp.
const ReplayWindow = 5 * time.Minute

// Signature returns the value for the x-servr-signature header.
func Signature(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// Verify checks an inbound x-servr-timestamp / x-servr-signature pair against
// the raw request body. The timestamp must be all digits and within the
// replay window of now; the comparison is constant-time.
func Verify(secret, timestamp string, body []byte, header string, now time.Time) error {
	if header == "" {
		return errors.New("missing signature header")
	}
	for _, c := range timestamp {
		if c < '0' || c > '9' {
			return errors.New("malformed timestamp")
		}
	}
	if len(timestamp) == 0 {
		return errors.New("missing timestamp")
	}
	var ts int64
	for _, c := range timestamp {
		ts = ts*10 + int64(c-'0')
	}
	d := now.Sub(time.Unix(ts, 0))
	if d < 0 {
		d = -d
	}
	if d > ReplayWindow {
		return errors.New("stale timestamp")
	}
	want := Signature(secret, timestamp, body)
	if subtle.ConstantTimeCompare([]byte(want), []byte(header)) != 1 {
		return errors.New("signature mismatch")
	}
	return nil
}
