package mobilebridge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureGatewayTLSIsStableAndPrivate(t *testing.T) {
	dir := t.TempDir()
	a, err := EnsureGatewayTLS(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := EnsureGatewayTLS(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a.Pin == "" || len(a.Pin) != 64 || a.Pin != b.Pin {
		t.Fatalf("pin not stable across loads: %q vs %q", a.Pin, b.Pin)
	}
	for _, f := range []string{tlsKeyFile, tlsCertFile} {
		fi, err := os.Stat(filepath.Join(TLSDir(dir), f))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v, want 0600", f, fi.Mode().Perm())
		}
	}
	other, err := EnsureGatewayTLS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if other.Pin == a.Pin {
		t.Fatal("two data dirs produced the same key")
	}
}

func TestEndpointsStampPinOnGatewayEndpointsOnly(t *testing.T) {
	eps := Endpoints(EndpointInputs{
		LANHosts:       []string{"192.168.1.5"},
		TailscaleHosts: []string{"100.64.0.2"},
		TailscaleName:  "mac.tail.ts.net",
		Port:           4000,
		Pin:            "abc",
		Tunnel:         &TunnelEndpoint{Ready: true, Hostname: "x.trycloudflare.com"},
	})
	for _, e := range eps {
		want := e.Kind == KindLAN || e.Kind == KindTailscale
		if (e.Pin == "abc") != want || (e.Pin != "" && !want) {
			t.Fatalf("endpoint %+v: pin stamping wrong", e)
		}
		if e.Secure && e.Kind != KindTunnel {
			t.Fatalf("pinned endpoint must not set Secure (old phones would CA-validate): %+v", e)
		}
	}
}
