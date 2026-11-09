package daemonsock

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"testing"
)

// B434: a missing/denied socket falls back to the daemon's loopback TCP port.
func TestFallbackTransportUsesTCPWhenSocketMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	missing := filepath.Join("/nonexistent-tp-dir", "nope.sock")

	resp, err := (&http.Client{Transport: FallbackTransport(missing, port)}).Get(srv.URL)
	if err != nil {
		t.Fatalf("fallback to TCP failed: %v", err)
	}
	_ = resp.Body.Close()

	// Without a TCP port the socket error surfaces.
	if _, err := (&http.Client{Transport: FallbackTransport(missing, 0)}).Get(srv.URL); err == nil {
		t.Fatal("expected socket error with no TCP port")
	}
}
