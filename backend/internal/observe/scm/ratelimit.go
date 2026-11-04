package scm

import (
	"errors"
	"time"
)

// rateLimitedError is the optional capability of a provider error that carries
// structured retry hints (Retry-After / RateLimit-Reset). The gitlab and
// github adapters' RateLimitError types both satisfy this via errors.As; a
// bare sentinel without structured hints falls through to the bounded default.
type rateLimitedError interface {
	GetRetryAfter() time.Duration
	GetResetAt() time.Time
}

// rateLimitCooldown extracts the cooldown duration from a provider rate-limit
// error. It prefers the provider's Retry-After, falls back to ResetAt, and
// finally to a bounded default with jitter so the observer does not hammer
// the API every 30s while rate-limited. Returns ok=false
// when err is not a rate-limit error.
func rateLimitCooldown(now time.Time, err error) (time.Duration, bool) {
	var rl rateLimitedError
	if !errors.As(err, &rl) {
		return 0, false
	}
	if ra := rl.GetRetryAfter(); ra > 0 {
		return boundedCooldown(ra), true
	}
	if reset := rl.GetResetAt(); !reset.IsZero() && reset.After(now) {
		return boundedCooldown(reset.Sub(now)), true
	}
	return defaultRateLimitCooldown(now), true
}

// boundedCooldown clamps the provider-suggested cooldown to a sane window so a
// misbehaving server cannot force the observer to sleep for hours.
const (
	minRateLimitCooldown = 30 * time.Second
	maxRateLimitCooldown = 10 * time.Minute
	// incrementalDiscoveryOverlap is subtracted from the last sync cursor so
	// MRs updated near the cursor boundary are not missed due to clock skew
	// or updated_at granularity
	incrementalDiscoveryOverlap = 5 * time.Minute
)

func boundedCooldown(d time.Duration) time.Duration {
	if d < minRateLimitCooldown {
		d = minRateLimitCooldown
	}
	if d > maxRateLimitCooldown {
		d = maxRateLimitCooldown
	}
	return d
}

func defaultRateLimitCooldown(now time.Time) time.Duration {
	// 60s ± 15s jitter, deterministic per clock tick so tests using a fixed
	// clock are reproducible.
	base := 60 * time.Second
	jitter := time.Duration(now.UnixNano()%int64(15*time.Second)) - 7*time.Second
	return boundedCooldown(base + jitter)
}

// inRateLimitCooldown reports whether the named provider is currently under a
// rate-limit cooldown. Provider key is the SCMRepo.Provider
// value (e.g. "gitlab", "github").
func (o *Observer) inRateLimitCooldown(now time.Time, providerKey string) bool {
	if o.rateLimitUntil == nil {
		return false
	}
	until, ok := o.rateLimitUntil[providerKey]
	if !ok {
		return false
	}
	if now.Before(until) {
		return true
	}
	// Cooldown expired — clear the entry so it doesn't grow unboundedly.
	delete(o.rateLimitUntil, providerKey)
	return false
}

// setRateLimitCooldown records that providerKey should be skipped until now+d.
func (o *Observer) setRateLimitCooldown(now time.Time, providerKey string, d time.Duration) {
	if o.rateLimitUntil == nil {
		o.rateLimitUntil = map[string]time.Time{}
	}
	o.rateLimitUntil[providerKey] = now.Add(d)
}
