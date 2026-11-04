package mobilebridge

import (
	"errors"
	"testing"
)

func TestParsePublicBaseURL(t *testing.T) {
	ok := []struct {
		in   string
		want Endpoint
	}{
		{"https://tallpine.example.com", Endpoint{Kind: KindRelay, Host: "tallpine.example.com", Port: 443, Secure: true}},
		{"  https://Tallpine.example.com:8443/ ", Endpoint{Kind: KindRelay, Host: "tallpine.example.com", Port: 8443, Secure: true}},
		{"https://mac.tail1234.ts.net", Endpoint{Kind: KindRelay, Host: "mac.tail1234.ts.net", Port: 443, Secure: true}},
	}
	for _, c := range ok {
		got, err := ParsePublicBaseURL(c.in)
		if err != nil || got != c.want {
			t.Errorf("ParsePublicBaseURL(%q) = %+v, %v; want %+v", c.in, got, err, c.want)
		}
	}
	bad := []string{
		"", "tallpine.example.com", "ftp://tallpine.example.com", "https://user:pw@tallpine.example.com",
		"https://tallpine.example.com/tallpine", "https://tallpine.example.com/?x=1", "https://tallpine.example.com/#f",
		"http://tallpine.example.com", "http://8.8.8.8:3011",
		// Plain http is refused for every host, private and tailnet ones included.
		"http://100.101.102.103:3011", "http://mac.tail1234.ts.net:3011", "http://192.168.1.5:3011", "http://localhost:3011", "https://[::1]:3011", "https://tallpine.example.com:99999",
	}
	for _, in := range bad {
		if _, err := ParsePublicBaseURL(in); !errors.Is(err, ErrInvalidPublicBaseURL) {
			t.Errorf("ParsePublicBaseURL(%q) err = %v; want ErrInvalidPublicBaseURL", in, err)
		}
	}
}

func TestEndpointsAdvertisesPublicOnlyWhenListenerBound(t *testing.T) {
	pub := &Endpoint{Kind: KindRelay, Host: "tallpine.example.com", Port: 443, Secure: true}
	if got := Endpoints(EndpointInputs{Port: 0, Public: pub}); got != nil {
		t.Fatalf("unbound listener must advertise nothing, got %v", got)
	}
	got := Endpoints(EndpointInputs{Port: 3011, LANHosts: []string{"192.168.1.5"}, Pin: "ab12", Public: pub})
	if len(got) != 2 || got[0].Kind != KindLAN || got[1] != *pub {
		t.Fatalf("endpoints = %+v", got)
	}
}
