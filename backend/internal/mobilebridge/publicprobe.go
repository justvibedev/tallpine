package mobilebridge

import (
	"context"
	"net/http"
	"time"
)

// publicProbeTimeout bounds one liveness check. Status is polled by the
// Connect Mobile modal, so a dead host must not stall it.
const publicProbeTimeout = 2 * time.Second

// ProbePublicEndpoint reports whether ep answers the unauthenticated identity
// route, i.e. whether something is actually serving the advertised public URL.
// A Tailscale Serve config that was cleared, or a proxy that went away, leaves
// the stored URL behind; advertising it makes every phone race a dead address.
func ProbePublicEndpoint(ctx context.Context, ep Endpoint) bool {
	ctx, cancel := context.WithTimeout(ctx, publicProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ep.PublicBaseURLString()+"/api/v1/identity", http.NoBody)
	if err != nil {
		return false
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = res.Body.Close() }()
	return res.StatusCode == http.StatusOK
}
