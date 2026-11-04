package httpd

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/mobilebridge"
)

// authState keeps previously valid hashes so a client retrying an old password
// after rotation cannot lock out a newly paired client behind the same address.
type authState struct {
	hashes atomic.Pointer[authHashes]
	deviceAuth
}
type authHashes struct {
	current string
	// expires is when the current pairing code stops being redeemable; zero
	// means it does not expire (the hash is a credential in its own right).
	expires time.Time
	retired []string
}

// maxRetiredHashes caps how many superseded pairing-code hashes are remembered.
const maxRetiredHashes = 8

func (a *authState) setHash(h string) { a.setHashUntil(h, time.Time{}) }

// setHashUntil arms h as the current hash, redeemable until expires.
func (a *authState) setHashUntil(h string, expires time.Time) {
	for {
		previous := a.hashes.Load()
		if previous != nil && previous.current == h {
			return
		}
		next := &authHashes{current: h, expires: expires}
		if previous != nil {
			next.retired = append(next.retired, previous.retired...)
			if previous.current != "" {
				next.retired = append(next.retired, previous.current)
			}
			// Bounded: keep only the newest retired hashes.
			if extra := len(next.retired) - maxRetiredHashes; extra > 0 {
				next.retired = next.retired[extra:]
			}
		}
		if a.hashes.CompareAndSwap(previous, next) {
			return
		}
	}
}

// pairingCodeLive reports whether the current pairing code may still be redeemed.
func (a *authState) pairingCodeLive(now time.Time) bool {
	h := a.hashes.Load()
	return h != nil && h.current != "" && (h.expires.IsZero() || now.Before(h.expires))
}

func (a *authState) currentHash() string {
	if hashes := a.hashes.Load(); hashes != nil {
		return hashes.current
	}
	return ""
}

// pairingCodeMatches reports whether token is the current or a retired pairing
// code. Such a request is a stale client, not a guess, so it does not count
// against the lockout.
func (a *authState) pairingCodeMatches(token string) bool {
	if h := a.currentHash(); h != "" && mobilebridge.PasswordMatches(h, token) {
		return true
	}
	return a.retiredPasswordMatches(token)
}

func (a *authState) retiredPasswordMatches(token string) bool {
	if hashes := a.hashes.Load(); hashes != nil {
		for _, hash := range hashes.retired {
			if mobilebridge.PasswordMatches(hash, token) {
				return true
			}
		}
	}
	return false
}

// lockout throttles password guessing per source address and in total.
//
// Per source, five failures lock that source out for a minute. IPv6 sources
// count per /64, the block one client can rotate addresses within. A source
// alone is not enough: a client behind a tunnel, one rotating addresses, or a
// local process spoofing CF-Connecting-IP on the loopback bridge presents a
// new source per guess. So all failures also share a global budget; once it
// is spent, only a source that authenticated recently in this process is let
// through until the window ends. Device-token requests skip the lockout
// entirely (see authMiddleware): the token is 256 random bits, so presenting it
// is no guess. That holds for an unknown or revoked one too, which is answered
// DEVICE_TOKEN_REVOKED without counting. A client-supplied install id is never
// trusted; it travels in cleartext and is not secret.
type lockout struct {
	mu        sync.Mutex
	now       func() time.Time
	attempts  map[string]authAttempts
	lastSweep time.Time

	globalFails   int
	globalExpires time.Time
	// trusted holds sources ("src:") that authenticated, until when they may
	// pass a spent global budget.
	trusted map[string]time.Time
}

const (
	sourceFailureLimit = 5
	globalFailureLimit = 30
	lockoutWindow      = time.Minute
	trustedSourceTTL   = 24 * time.Hour
)

type authAttempts struct {
	fails   int
	expires time.Time
}

func newLockout(now func() time.Time) *lockout {
	return &lockout{now: now, attempts: map[string]authAttempts{}, trusted: map[string]time.Time{}}
}

// lockoutKey groups an IPv6 source by its /64.
func lockoutKey(src string) string {
	addr, err := netip.ParseAddr(src)
	if err != nil {
		return src
	}
	addr = addr.Unmap()
	if addr.Is6() {
		if prefix, err := addr.WithZone("").Prefix(64); err == nil {
			return prefix.String()
		}
	}
	return addr.String()
}

// blocked reports whether a request from src must not even try a credential.
func (l *lockout) blocked(src string) bool {
	src = lockoutKey(src)
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Before(l.globalExpires) && l.globalFails >= globalFailureLimit && !l.trustedLocked(now, src) {
		return true
	}
	a, ok := l.attempts[src]
	if !ok {
		return false
	}
	if now.Before(a.expires) {
		return a.fails >= sourceFailureLimit
	}
	delete(l.attempts, src)
	return false
}

func (l *lockout) fail(src string) {
	src = lockoutKey(src)
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Sub(l.lastSweep) >= time.Minute {
		for key, a := range l.attempts {
			if !now.Before(a.expires) {
				delete(l.attempts, key)
			}
		}
		for key, until := range l.trusted {
			if !now.Before(until) {
				delete(l.trusted, key)
			}
		}
		l.lastSweep = now
	}
	a := l.attempts[src]
	if !now.Before(a.expires) {
		a.fails = 0
	}
	a.fails++
	a.expires = now.Add(lockoutWindow)
	l.attempts[src] = a
	if !now.Before(l.globalExpires) {
		l.globalFails = 0
		l.globalExpires = now.Add(lockoutWindow)
	}
	l.globalFails++
}

func (l *lockout) trustedLocked(now time.Time, src string) bool {
	return now.Before(l.trusted["src:"+src])
}

// reset clears a source's failures after it authenticated and trusts it past a
// spent global budget for a while.
func (l *lockout) reset(src string) {
	src = lockoutKey(src)
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, src)
	l.trusted["src:"+src] = l.now().Add(trustedSourceTTL)
}

func (l *lockout) resetAll() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.attempts = map[string]authAttempts{}
	l.trusted = map[string]time.Time{}
	l.globalFails = 0
	l.globalExpires = time.Time{}
}

func sourceKey(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// clientSource is the address of a request's client, which lockouts count
// failures against. Cloudflared connects from loopback, so its visitor IP
// stands in for it. A LAN client must not be able to spoof its source with this
// header, and neither can another loopback peer (Tailscale Serve): the header
// counts only on the listener reserved for cloudflared.
func clientSource(r *http.Request) string {
	src := sourceKey(r)
	if ip := net.ParseIP(src); ip != nil && ip.IsLoopback() && viaCloudflared(r) {
		if forwarded := r.Header.Values("CF-Connecting-IP"); len(forwarded) == 1 {
			if visitor := net.ParseIP(forwarded[0]); visitor != nil {
				return visitor.String()
			}
		}
	}
	// Tailscale Serve also connects from loopback, so without this every tailnet
	// peer shares one lockout bucket: one bad peer locks out the owner's phone,
	// and the owner's authenticated requests keep resetting the counter for
	// everyone. Its X-Forwarded-For carries the peer's tailnet address. Only a
	// tailnet address is believed; a local process can still forge one, which
	// the global budget bounds, as with CF-Connecting-IP.
	if ip := net.ParseIP(src); ip != nil && ip.IsLoopback() && !viaCloudflared(r) {
		if forwarded := r.Header.Values("X-Forwarded-For"); len(forwarded) == 1 {
			if peer := net.ParseIP(strings.TrimSpace(forwarded[0])); peer != nil && inTailscale(peer.String()) {
				return peer.String()
			}
		}
	}
	return src
}

func viaCloudflared(r *http.Request) bool {
	v, _ := r.Context().Value(cloudflaredContextKey{}).(bool)
	return v
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return ""
}

// authCookieName carries the connection token for a preview page's in-page
// subresource requests. See connectionToken / maybeSetPreviewAuthCookie.
const authCookieName = "ao_conn"

// previewFilesMarker is the path segment that identifies a preview-file request
// (GET /api/v1/sessions/{id}/preview/files/*). The auth cookie is both scoped to
// and honored only on this path, so it can never authenticate any other endpoint.
const previewFilesMarker = "/preview/files/"

// previewFilesCookiePath returns the cookie Path to scope the auth cookie to the
// requesting session's preview files (".../preview/files/"), or "" if the request
// is not a preview-file request. Scoping this tightly is what keeps the cookie
// from ever reaching /kill, /send, another session, or any non-preview route.
func previewFilesCookiePath(urlPath string) string {
	i := strings.Index(urlPath, previewFilesMarker)
	if i < 0 {
		return ""
	}
	return urlPath[:i+len(previewFilesMarker)]
}

// connectionToken returns the caller's connection token. It comes from the
// Authorization: Bearer header (the mobile API client and a preview page's
// top-level navigation) or, ONLY on the preview-files route, the auth cookie (a
// preview page's subresource requests — images/CSS/JS — which the WebView issues
// without our header). Restricting the cookie to the preview-files path means it
// can never authenticate any other mobile endpoint even if a client sends it.
func connectionToken(r *http.Request) string {
	if t := bearerToken(r); t != "" {
		return t
	}
	if previewFilesCookiePath(r.URL.Path) != "" {
		if c, err := r.Cookie(authCookieName); err == nil {
			return c.Value
		}
	}
	return ""
}

// maybeSetPreviewAuthCookie drops the auth cookie when a preview FILE is fetched
// with a valid token, so the WebView's follow-up subresource requests on the same
// password-protected preview route authenticate too (they never carry our
// Authorization header). The cookie is Path-scoped to this session's preview
// files only, HttpOnly, and re-sent only when it doesn't already match the token
// that just authenticated — so a normal subresource costs no Set-Cookie, but a
// cookie left over from a regenerated password is overwritten instead of being
// kept until it 401s every image/CSS/JS on the page. This runs on the LAN
// listener only; the loopback/desktop preview path never reaches authMiddleware,
// so desktop preview behavior is unchanged.
func maybeSetPreviewAuthCookie(w http.ResponseWriter, r *http.Request, tok string) {
	path := previewFilesCookiePath(r.URL.Path)
	if path == "" {
		return
	}
	if c, err := r.Cookie(authCookieName); err == nil && c.Value == tok {
		return // already current; don't re-send Set-Cookie on every subresource
	}
	//nolint:gosec // Secure is intentionally omitted: the LAN bridge is plaintext
	// http by design (ADR 0001, home-network-only), and a Secure cookie would never
	// be sent over it. The token already travels the same plain link via Bearer.
	// nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure -- plain-http LAN bridge, see below
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    tok,
		Path:     path,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		// No Secure: the LAN link is plain http (a TLS tunnel still sends it),
		// matching how the Bearer token already travels.
	})
}

// identityProbePath is the one route the LAN listener serves without the
// connection password. The phone races several endpoints, and a private
// address is not an identity: 192.168.1.42 exists on most networks. Verifying
// which machine answered has to happen BEFORE a credential is presented, or
// the phone leaks its token to whatever device holds that address on a foreign
// network. The response carries an opaque host id and nothing else.
//
// Exact path, GET only, and checked ahead of the lockout so a phone racing
// endpoints cannot lock itself out probing. See
// docs/adr/0003-unauthenticated-identity-probe.md.
const identityProbePath = "/api/v1/identity"

// isIdentityProbe reports whether r is the exempt probe. Deliberately an exact
// match rather than a prefix, so nothing nested below the path inherits the
// exemption.
func isIdentityProbe(r *http.Request) bool {
	return r.Method == http.MethodGet && r.URL.Path == identityProbePath
}

// lanBearerContextKey is set only by the authenticated listener after a
// successful bearer check. Preview-scoped cookies never confer API authority.
type lanBearerContextKey struct{}

type lanPreviewCookieContextKey struct{}

func authenticatedLANBearer(r *http.Request) bool {
	authenticated, _ := r.Context().Value(lanBearerContextKey{}).(bool)
	return authenticated
}

// authMiddleware authenticates LAN requests with a per-device token (or, for
// phones not yet upgraded, the legacy password until its grace window ends).
// The pairing code is not a bearer: it only works at the pair-exchange route.
func authMiddleware(state *authState, lock *lockout) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isIdentityProbe(r) {
				next.ServeHTTP(w, r)
				return
			}
			src := clientSource(r) // for the lockout only
			tok := connectionToken(r)
			conn, _ := r.Context().Value(lanConnContextKey{}).(*lanConn)
			if conn != nil {
				conn.owner.mu.Lock()
			}
			// Record auth atomically with revocation's connection close. A device
			// token is 256 random bits, so presenting one is not a guess and skips
			// the failure lockout.
			_, device := state.verifyDevice(tok)
			legacy, legacyExpired := false, false
			if !device {
				legacy, legacyExpired = state.legacyState(tok, state.clock())
			}
			authenticated := device || legacy
			if conn != nil {
				if authenticated {
					conn.tokenHash = mobilebridge.HashPassword(tok)
					if legacy {
						conn.tokenHash = legacyTokenHash
					}
				}
				conn.owner.mu.Unlock()
			}
			if authenticated {
				if legacy && lock.blocked(src) {
					envelope.WriteAPIError(w, r, http.StatusTooManyRequests, "too_many_requests", "LOCKED_OUT",
						"too many failed attempts; try again shortly", nil)
					return
				}
				lock.reset(src)
				maybeSetPreviewAuthCookie(w, r, tok)
				cred := mobilebridge.HashPassword(tok)
				if legacy {
					cred = legacyTokenHash
				}
				r = r.WithContext(mobilebridge.WithCredential(r.Context(), cred))
				if bearerToken(r) != "" {
					r = r.WithContext(context.WithValue(r.Context(), lanBearerContextKey{}, true))
				} else {
					r = r.WithContext(context.WithValue(r.Context(), lanPreviewCookieContextKey{}, true))
				}
				next.ServeHTTP(w, r)
				return
			}
			if legacyExpired {
				envelope.WriteAPIError(w, r, http.StatusUnauthorized, "unauthorized", "LEGACY_PAIRING_EXPIRED",
					"this pairing is out of date; scan the pairing code on the desktop again", nil)
				return
			}
			if mobilebridge.LooksLikeDeviceToken(tok) {
				// An unpaired phone keeps presenting its old token until it learns
				// the pairing is gone. That is not a guess, so it must neither feed
				// the lockout nor be answered with one: the same source is about
				// to redeem a fresh pairing code.
				envelope.WriteAPIError(w, r, http.StatusUnauthorized, "unauthorized", "DEVICE_TOKEN_REVOKED",
					"this phone's pairing was removed; scan the pairing code on the desktop again", nil)
				return
			}
			if lock.blocked(src) {
				envelope.WriteAPIError(w, r, http.StatusTooManyRequests, "too_many_requests", "LOCKED_OUT",
					"too many failed attempts; try again shortly", nil)
				return
			}
			if !state.pairingCodeMatches(tok) {
				lock.fail(src)
			}
			envelope.WriteAPIError(w, r, http.StatusUnauthorized, "unauthorized", "BAD_PASSWORD",
				"missing or invalid connection token", nil)
		})
	}
}

var tailscaleRanges = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("fd7a:115c:a1e0::/48"),
}

// inTailscale reports whether addr is a tailnet address.
func inTailscale(addr string) bool {
	ip, err := netip.ParseAddr(addr)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	for _, p := range tailscaleRanges {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}
