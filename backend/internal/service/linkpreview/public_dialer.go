package linkpreview

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

// errNonPublicAddress refuses a connection to an address a link preview must
// never reach.
var errNonPublicAddress = errors.New("linkpreview: refused non-public address")

// nonPublicPrefixes are the ranges a preview fetch may not connect to: this
// host, private and carrier-grade NAT networks (Tailscale included), link-local
// and cloud metadata addresses, and reserved, benchmark, and multicast space.
// IPv4-mapped IPv6 addresses are unmapped before the check.
var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/3"),
	netip.MustParsePrefix("::/127"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("fec0::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

// publicAddress reports whether ip is a globally routable unicast address.
func publicAddress(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.Zone() != "" {
		return false
	}
	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

// refuseNonPublic is a net.Dialer Control hook. It runs on the address the
// socket is about to connect to, after DNS resolution, so a hostname that
// resolves (or rebinds) to a private address is refused as well, and every
// redirect hop is checked again when it dials.
func refuseNonPublic(_, address string, _ syscall.RawConn) error {
	addrPort, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("%w: %s", errNonPublicAddress, address)
	}
	if !publicAddress(addrPort.Addr()) {
		return fmt.Errorf("%w: %s", errNonPublicAddress, addrPort.Addr())
	}
	return nil
}

// publicOnlyTransport fetches previews directly (never through an
// environment proxy, which would resolve the target where the guard cannot
// see it) and only to public addresses. Without it the daemon is an SSRF
// proxy: any caller could read loopback services, the LAN, or cloud metadata
// through GET /api/v1/link-preview.
func publicOnlyTransport() *http.Transport {
	dialer := &net.Dialer{Timeout: fetchTimeout, KeepAlive: 30 * time.Second, Control: refuseNonPublic}
	return &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   fetchTimeout,
		ExpectContinueTimeout: time.Second,
	}
}
