package service

import (
	"context"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/mobilebridge"
)

// Values of MobileStatusResponse.PublicURLState.
const (
	publicURLNotConfigured = "not_configured"
	publicURLOK            = "ok"
	publicURLUnreachable   = "unreachable"
)

// liveCacheTTL is how long a liveness answer or the MagicDNS name is reused.
// Status is polled by the modal and by every phone refresh; neither may shell
// out to the tailscale CLI or dial the public URL each time.
const liveCacheTTL = 15 * time.Second

// endpointCache memoizes the two slow lookups behind the advertised list.
type endpointCache struct {
	mu sync.Mutex

	nameAt time.Time
	name   string

	liveKey string
	liveAt  time.Time
	live    bool
}

func (b *BridgeService) tailscaleName(hosts []string) string {
	if len(hosts) == 0 {
		return ""
	}
	c := &b.epCache
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.nameAt) >= liveCacheTTL {
		c.name = b.queryTS().Name
		c.nameAt = time.Now()
	}
	return c.name
}

func (b *BridgeService) publicLive(ep mobilebridge.Endpoint) bool {
	key := ep.PublicBaseURLString()
	c := &b.epCache
	c.mu.Lock()
	if c.liveKey == key && time.Since(c.liveAt) < liveCacheTTL {
		live := c.live
		c.mu.Unlock()
		return live
	}
	c.mu.Unlock()
	probe := b.ProbePublic
	if probe == nil {
		probe = mobilebridge.ProbePublicEndpoint
	}
	live := probe(context.Background(), ep)
	c.mu.Lock()
	c.liveKey, c.liveAt, c.live = key, time.Now(), live
	c.mu.Unlock()
	return live
}

// advertised is the one place the candidate list is assembled, so Status and
// the phone's refresh route cannot disagree. The public URL is advertised only
// while it answers: a stored URL whose Tailscale Serve config or proxy is gone
// would otherwise be handed to every phone as a dead relay.
func (b *BridgeService) advertised(st mobilebridge.State) (lan, ts []string, eps []mobilebridge.Endpoint, publicState string) {
	if !st.LoopbackOnly {
		lan, ts = b.lanHosts(), b.tailscaleHosts()
	}
	publicState = publicURLNotConfigured
	var public *mobilebridge.Endpoint
	if ep := publicEndpoint(st); ep != nil {
		if b.publicLive(*ep) {
			public, publicState = ep, publicURLOK
		} else {
			publicState = publicURLUnreachable
		}
	} else if st.FeatureEnabled && st.PublicBaseURL != "" {
		publicState = publicURLUnreachable // stored value no longer parses
	}
	eps = mobilebridge.Endpoints(mobilebridge.EndpointInputs{
		LANHosts:       lan,
		TailscaleHosts: ts,
		TailscaleName:  b.tailscaleName(ts),
		Port:           b.LAN.BoundPort(),
		Pin:            b.TLSPin,
		Tunnel:         b.tunnelEndpoint(),
		Public:         public,
	})
	return lan, ts, eps, publicState
}
