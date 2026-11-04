package mobilebridge

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ErrInvalidPublicBaseURL wraps every rejection from ParsePublicBaseURL so
// callers can answer 400 without matching message text.
var ErrInvalidPublicBaseURL = errors.New("invalid public base URL")

func badPublicURL(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidPublicBaseURL, reason)
}

// ParsePublicBaseURL turns the user's reachable base URL (a Tailscale
// MagicDNS/100.x address or their own reverse proxy) into the endpoint pairing
// codes advertise. The phone builds requests as scheme://host:port with no path
// prefix, so anything beyond scheme, host and port is rejected rather than
// silently dropped.
//
// Only https is accepted: the phone never talks plain http, on any network,
// because the device token travels in every request. A URL saved by an older
// build with http no longer parses, so it is reported unreachable and never
// advertised.
func ParsePublicBaseURL(raw string) (Endpoint, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return Endpoint{}, badPublicURL("enter a full URL such as https://tallpine.example.com")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" {
		return Endpoint{}, badPublicURL("the URL must use https; phones never connect over plain http")
	}
	if u.User != nil {
		return Endpoint{}, badPublicURL("credentials in the URL are not allowed")
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return Endpoint{}, badPublicURL("the URL must not contain a path, query or fragment")
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" {
		return Endpoint{}, badPublicURL("host is required")
	}
	if strings.Contains(host, ":") {
		return Endpoint{}, badPublicURL("IPv6 literals are not supported; use a hostname")
	}
	port := 443
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return Endpoint{}, badPublicURL("port must be between 1 and 65535")
		}
		port = n
	}
	return Endpoint{Kind: KindRelay, Host: host, Port: port, Secure: true}, nil
}

// PublicBaseURLString renders an endpoint back to the canonical base URL.
func (e Endpoint) PublicBaseURLString() string {
	scheme := "http"
	if e.Secure {
		scheme = "https"
	}
	return scheme + "://" + net.JoinHostPort(e.Host, strconv.Itoa(e.Port))
}
