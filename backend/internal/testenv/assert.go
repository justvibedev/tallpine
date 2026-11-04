package testenv

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// MustNoError fails the test now when err is non-nil; the optional first
// context string prefixes the message.
func MustNoError(t testing.TB, err error, context ...string) {
	t.Helper()
	if err != nil {
		if len(context) > 0 {
			t.Fatalf("%s: %v", context[0], err)
		}
		t.Fatal(err)
	}
}

// LoopbackRequest is httptest.NewRequest with the default example.com host
// rewritten to the daemon's loopback address, so host-guard middleware admits it.
func LoopbackRequest(method, target string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, target, body)
	if req.Host == "example.com" {
		req.Host = "127.0.0.1:3101"
	}
	return req
}
