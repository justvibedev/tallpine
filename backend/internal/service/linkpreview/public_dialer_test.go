package linkpreview

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync/atomic"
	"testing"
)

func TestPublicAddress(t *testing.T) {
	for addr, want := range map[string]bool{
		"93.184.215.14":        true,
		"2606:2800:21f:cb07::": true,
		"1.1.1.1":              true,
		"127.0.0.1":            false,
		"127.8.9.10":           false,
		"::1":                  false,
		"0.0.0.0":              false,
		"::":                   false,
		"10.1.2.3":             false,
		"172.20.0.1":           false,
		"192.168.1.10":         false,
		"100.100.100.100":      false, // Tailscale / CGNAT
		"169.254.169.254":      false, // cloud metadata
		"fd00:ec2::254":        false, // AWS IPv6 metadata (ULA)
		"fe80::1":              false,
		"::ffff:127.0.0.1":     false, // IPv4-mapped loopback
		"::ffff:10.0.0.1":      false,
		"64:ff9b::a00:1":       false, // NAT64 of 10.0.0.1
		"2002:a00:1::":         false, // 6to4 of 10.0.0.1
		"224.0.0.1":            false,
		"255.255.255.255":      false,
		"198.18.0.1":           false,
	} {
		if got := publicAddress(netip.MustParseAddr(addr)); got != want {
			t.Errorf("publicAddress(%s) = %v, want %v", addr, got, want)
		}
	}
}

// The default client refuses loopback, by address and by a name that
// resolves to it, before any byte reaches the target.
func TestDefaultClientRefusesLocalTargets(t *testing.T) {
	var hits atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<title>secret</title>"))
	}))
	defer origin.Close()
	port := origin.URL[len("http://127.0.0.1:"):]

	svc := New(nil)
	for _, target := range []string{origin.URL + "/", "http://localhost:" + port + "/", "http://[::ffff:127.0.0.1]:" + port + "/"} {
		_, err := svc.Preview(context.Background(), target)
		if !errors.Is(err, ErrFetchFailed) {
			t.Errorf("Preview(%s) error = %v, want ErrFetchFailed", target, err)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("local target received %d requests", n)
	}
}

func TestRefuseNonPublicRejectsUnparsableAddresses(t *testing.T) {
	if err := refuseNonPublic("tcp", "not-an-address", nil); !errors.Is(err, errNonPublicAddress) {
		t.Fatalf("err = %v", err)
	}
	if err := refuseNonPublic("tcp6", "[fe80::1%en0]:80", nil); !errors.Is(err, errNonPublicAddress) {
		t.Fatalf("zoned link-local err = %v", err)
	}
	if err := refuseNonPublic("tcp4", "93.184.215.14:443", nil); err != nil {
		t.Fatalf("public address refused: %v", err)
	}
}
