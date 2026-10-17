package sign

import (
	"testing"
	"time"
)

func TestSignatureKnownVector(t *testing.T) {
	// "sha256=" + hex(HMAC-SHA256(secret, "1760000000" + "." + body))
	got := Signature("s3cret", "1760000000", []byte(`{"event":"reservation.sync"}`))
	want := "sha256=b88bcc45c6699357ebd6cda63d3cdb05242bc231ddc6b4fe4ec5f68764c6d579"
	if got != want {
		t.Fatalf("signature mismatch:\n got %s\nwant %s", got, want)
	}
}

func TestSignatureEmptyBodyGET(t *testing.T) {
	// GET requests are signed over the empty body — the "." separator still
	// rides along.
	got := Signature("k", "1760000001", nil)
	got2 := Signature("k", "1760000001", []byte{})
	if got != got2 {
		t.Fatal("nil and empty body must sign identically")
	}
	if got == Signature("k", "1760000002", nil) {
		t.Fatal("timestamp must be part of the MAC input")
	}
}

func TestVerifyRoundTrip(t *testing.T) {
	now := time.Unix(1760000000, 0)
	body := []byte(`{"a":1}`)
	sig := Signature("topsecret", "1760000000", body)
	if err := Verify("topsecret", "1760000000", body, sig, now); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	if err := Verify("topsecret", "1760000000", []byte(`{"a":2}`), sig, now); err == nil {
		t.Fatal("different body accepted")
	}
	if err := Verify("other", "1760000000", body, sig, now); err == nil {
		t.Fatal("different secret accepted")
	}
}

func TestVerifyStaleTimestamp(t *testing.T) {
	now := time.Unix(1760000000, 0)
	sig := Signature("k", "1759996000", nil) // -4000s, outside ±300s
	if err := Verify("k", "1759996000", nil, sig, now); err == nil {
		t.Fatal("stale timestamp accepted")
	}
	// Fresh enough (-300s exactly) must pass.
	sig2 := Signature("k", "1759999700", nil)
	if err := Verify("k", "1759999700", nil, sig2, now); err != nil {
		t.Fatalf("boundary timestamp rejected: %v", err)
	}
}

func TestVerifyMalformedInputs(t *testing.T) {
	now := time.Now()
	if err := Verify("k", "176a", nil, "sha256=x", now); err == nil {
		t.Fatal("non-digit timestamp accepted")
	}
	if err := Verify("k", "", nil, "sha256=x", now); err == nil {
		t.Fatal("empty timestamp accepted")
	}
	if err := Verify("k", "1760000000", nil, "", now); err == nil {
		t.Fatal("empty signature accepted")
	}
	if err := Verify("k", "1760000000", nil, "md5=deadbeef", now); err == nil {
		t.Fatal("wrong scheme prefix accepted")
	}
}
