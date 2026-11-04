package mobilebridge

import "testing"

// tp is a gateway certificate pin; the listener endpoints are advertised only with one.
const tp = "ab12"

func TestEndpointsListsEveryLANAddress(t *testing.T) {
	// A machine on both Wi-Fi and Ethernet has two reachable LAN addresses.
	// The phone races them, so both must be advertised — the old AutopickLANIP
	// kept only the first, so the phone could never try the other.
	got := Endpoints(EndpointInputs{
		LANHosts: []string{"192.168.1.42", "10.0.0.5"},
		Port:     3011,
		Pin:      tp,
	})

	want := []Endpoint{
		{Kind: KindLAN, Host: "192.168.1.42", Port: 3011, Pin: tp},
		{Kind: KindLAN, Host: "10.0.0.5", Port: 3011, Pin: tp},
	}
	assertEndpoints(t, got, want)
}

func assertEndpoints(t *testing.T, got, want []Endpoint) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d endpoints %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("endpoint %d: got %+v want %+v", i, got[i], want[i])
		}
	}
}

func TestEndpointsPutsTailscaleAfterLAN(t *testing.T) {
	// Order encodes preference: the client's tie-break is lan > tailscale >
	// tunnel, and a client that cannot race should still try the fastest first.
	got := Endpoints(EndpointInputs{
		LANHosts:       []string{"192.168.1.42"},
		TailscaleHosts: []string{"100.72.46.7"},
		Port:           3011,
		Pin:            tp,
	})

	want := []Endpoint{
		{Kind: KindLAN, Host: "192.168.1.42", Port: 3011, Pin: tp},
		{Kind: KindTailscale, Host: "100.72.46.7", Port: 3011, Pin: tp},
	}
	assertEndpoints(t, got, want)
}

func TestEndpointsAppendsReadyTunnelLast(t *testing.T) {
	// The tunnel reaches any network but is the slowest path, so it sorts last.
	// It carries its own host and port and is always TLS.
	got := Endpoints(EndpointInputs{
		LANHosts: []string{"192.168.1.42"},
		Port:     3011,
		Pin:      tp,
		Tunnel:   &TunnelEndpoint{Ready: true, Hostname: "abc.trycloudflare.com"},
	})

	want := []Endpoint{
		{Kind: KindLAN, Host: "192.168.1.42", Port: 3011, Pin: tp},
		{Kind: KindTunnel, Host: "abc.trycloudflare.com", Port: 443, Secure: true},
	}
	assertEndpoints(t, got, want)
}

func TestEndpointsOmitsTunnelThatIsNotReady(t *testing.T) {
	// cloudflared prints a hostname several seconds before it has registered a
	// connection. Advertising it during that window hands the phone an endpoint
	// that answers 530, so readiness gates the whole entry.
	got := Endpoints(EndpointInputs{
		LANHosts: []string{"192.168.1.42"},
		Port:     3011,
		Pin:      tp,
		Tunnel:   &TunnelEndpoint{Ready: false, Hostname: "abc.trycloudflare.com"},
	})

	assertEndpoints(t, got, []Endpoint{
		{Kind: KindLAN, Host: "192.168.1.42", Port: 3011, Pin: tp},
	})
}

func TestEndpointsIsEmptyWhenNothingIsReachable(t *testing.T) {
	// No network and no tunnel is a real state, not an error. The phone must
	// receive an empty list and say "offline" rather than race a stale address.
	got := Endpoints(EndpointInputs{Port: 3011})
	assertEndpoints(t, got, nil)
}

func TestEndpointsOmitsTunnelWithoutHostname(t *testing.T) {
	got := Endpoints(EndpointInputs{
		Port:   3011,
		Pin:    tp,
		Tunnel: &TunnelEndpoint{Ready: true, Hostname: ""},
	})
	assertEndpoints(t, got, nil)
}

func TestEndpointsAdvertiseMagicDNSNameBeforeTailscaleIPs(t *testing.T) {
	got := Endpoints(EndpointInputs{
		TailscaleHosts: []string{"100.1.2.3"},
		TailscaleName:  "mac.tail1.ts.net",
		Port:           3011,
		Pin:            tp,
	})
	want := []Endpoint{
		{Kind: KindTailscale, Host: "mac.tail1.ts.net", Port: 3011, Pin: tp},
		{Kind: KindTailscale, Host: "100.1.2.3", Port: 3011, Pin: tp},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestEndpointsOmitMagicDNSNameWhenTailnetDown(t *testing.T) {
	got := Endpoints(EndpointInputs{LANHosts: []string{"192.168.1.2"}, TailscaleName: "mac.tail1.ts.net", Port: 3011, Pin: tp})
	if len(got) != 1 || got[0].Kind != KindLAN {
		t.Fatalf("got %+v", got)
	}
}

// Without a gateway pin the listener's own endpoints could only be reached over
// plain http, which the listener refuses, so they are not advertised; the
// tunnel and an https public URL still are.
func TestEndpointsOmitListenerEndpointsWithoutPin(t *testing.T) {
	pub := &Endpoint{Kind: KindRelay, Host: "tallpine.example.com", Port: 443, Secure: true}
	got := Endpoints(EndpointInputs{
		LANHosts:       []string{"192.168.1.42"},
		TailscaleHosts: []string{"100.72.46.7"},
		TailscaleName:  "mac.tail1.ts.net",
		Port:           3011,
		Tunnel:         &TunnelEndpoint{Ready: true, Hostname: "abc.trycloudflare.com"},
		Public:         pub,
	})
	assertEndpoints(t, got, []Endpoint{
		{Kind: KindTunnel, Host: "abc.trycloudflare.com", Port: 443, Secure: true},
		*pub,
	})
}
