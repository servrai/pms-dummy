// Package e2e is a BLACKBOX test suite: it talks to a running pms-dummy over
// HTTP only (public URL or local instance), drives the forms UI with a cookie
// jar, and re-implements the servr signing scheme locally on purpose —
// independence from internal/ is the point.
package e2e

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"time"
)

// signedRequest builds a request carrying the servr webhook signature:
//
//	x-servr-signature: sha256=HMAC_SHA256(secret, timestamp + "." + rawBody)
//
// The MAC is computed over the EXACT bytes that will be sent.
func signedRequest(method, url, secret string, body []byte) (*http.Request, error) {
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(body)
	req.Header.Set("x-servr-timestamp", ts)
	req.Header.Set("x-servr-signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

// signedRequestAt is signedRequest with a pinned timestamp (staleness tests).
func signedRequestAt(method, url, secret string, body []byte, ts int64) (*http.Request, error) {
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	tss := strconv.FormatInt(ts, 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(tss))
	mac.Write([]byte("."))
	mac.Write(body)
	req.Header.Set("x-servr-timestamp", tss)
	req.Header.Set("x-servr-signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}
