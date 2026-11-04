package mobilebridge

// EndpointKind names a way to reach this daemon. The phone races every
// advertised endpoint and prefers the lowest-latency kind that answers.
type EndpointKind string

// The advertised endpoint kinds, in the client's preference order: LAN is
// fastest, Tailscale crosses networks when it is already installed, and the
// tunnel reaches anything but is the slowest.
const (
	KindLAN       EndpointKind = "lan"
	KindTailscale EndpointKind = "tailscale"
	KindTunnel    EndpointKind = "tunnel"
	// KindRelay is the user-supplied public base URL (Tailscale MagicDNS name,
	// own reverse proxy). The wire kind is "relay" because every shipped phone
	// build already parses it and ranks it after the other kinds.
	KindRelay EndpointKind = "relay"
)

// tunnelPort is the port a Cloudflare tunnel hostname is always reached on.
const tunnelPort = 443

// TunnelEndpoint is the current state of the managed cloudflared sidecar.
// Ready is false until the connector has registered with an edge; a hostname
// alone is not enough, because cloudflared prints one seconds before the
// tunnel actually carries traffic.
type TunnelEndpoint struct {
	Ready    bool
	Hostname string
}

// Endpoint is one advertised route to the daemon.
type Endpoint struct {
	Kind   EndpointKind `json:"kind"`
	Host   string       `json:"host"`
	Port   int          `json:"port"`
	Secure bool         `json:"secure"`
	// Pin is the SHA-256 of the gateway certificate's public key (lowercase
	// hex). Present on the endpoints served by the gateway's own TLS (lan and
	// tailscale); a phone that understands it connects over https and accepts
	// only that key. Phones that predate it ignore the field and keep using
	// plain http, which is why Secure stays false for these endpoints.
	Pin string `json:"pin,omitempty"`
}

// EndpointInputs is everything Endpoints needs to build the candidate list.
type EndpointInputs struct {
	LANHosts       []string
	TailscaleHosts []string
	// TailscaleName is this node's MagicDNS name, or "" when unknown. It is
	// advertised as a plain-http tailscale endpoint ahead of the IPs: iOS App
	// Transport Security refuses cleartext http to the 100.64.0.0/10 range but
	// the phone build exempts *.ts.net, and the hop is WireGuard encrypted.
	TailscaleName string
	Port          int
	// Pin is the gateway certificate pin stamped on the lan and tailscale
	// endpoints; empty when the gateway does not serve TLS, in which case
	// neither is advertised.
	Pin string
	// Tunnel is nil when remote access is off.
	Tunnel *TunnelEndpoint
	// Public is the validated user-supplied base URL, nil when none is set.
	Public *Endpoint
}

// Endpoints builds the candidate list the phone races.
//
// A zero port means the LAN listener is not bound — Connect Mobile is off, or
// still starting — so there is nothing to advertise at all. Emitting host:0
// entries would have the phone race addresses that cannot work, and the tunnel
// is no exception: it forwards to that same port, so with no listener behind it
// there is nothing for it to reach.
func Endpoints(in EndpointInputs) []Endpoint {
	var out []Endpoint
	if in.Port <= 0 {
		return nil
	}
	// The listener's own endpoints are reachable only over its pinned TLS; it
	// refuses plain http from the network. Without a pin (the gateway key could
	// not be created) there is nothing a phone could use, so none are listed.
	if in.Pin == "" {
		in.LANHosts, in.TailscaleHosts = nil, nil
	}
	for _, h := range in.LANHosts {
		out = append(out, Endpoint{Kind: KindLAN, Host: h, Port: in.Port, Pin: in.Pin})
	}
	// The name only means something while the tailnet is up on this machine.
	if in.TailscaleName != "" && len(in.TailscaleHosts) > 0 {
		out = append(out, Endpoint{Kind: KindTailscale, Host: in.TailscaleName, Port: in.Port, Pin: in.Pin})
	}
	for _, h := range in.TailscaleHosts {
		out = append(out, Endpoint{Kind: KindTailscale, Host: h, Port: in.Port, Pin: in.Pin})
	}
	if in.Tunnel != nil && in.Tunnel.Ready && in.Tunnel.Hostname != "" {
		out = append(out, Endpoint{Kind: KindTunnel, Host: in.Tunnel.Hostname, Port: tunnelPort, Secure: true})
	}
	if in.Public != nil {
		out = append(out, *in.Public)
	}
	return out
}
