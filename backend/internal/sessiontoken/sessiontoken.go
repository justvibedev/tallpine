// Package sessiontoken mints and verifies the scoped credentials the daemon
// hands to the agents it launches (see
// docs/adr/0008-scoped-agent-session-tokens.md).
//
// The install token in daemon.token carries full daemon authority. An agent
// gets TALLPINE_SESSION_TOKEN instead: a credential bound to its own session (or,
// for a reviewer, to its review and the worker under review) that the daemon
// authorizes against a capability policy rather than treating as an admin.
//
// Tokens are stateless MACs. The key is derived from the install token, so a
// daemon restart keeps every running agent's token valid (agents in tmux and
// persistent chat hosts outlive the daemon), and rotating the install token
// revokes every session token at once. A session token additionally binds the
// session's current browser-capability verifier, which Session Manager rotates
// and persists on every agent launch: relaunching, restoring, or resuming a
// session rotates its token and revokes the previous launch's.
package sessiontoken

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/domain"
)

// EnvVar carries an agent's scoped token. The CLI prefers it over the install
// token file, and sends it in the same X-VO-Token header.
var EnvVar = brand.Env("SESSION_TOKEN")

// RunFileEnvVar names the run file of the daemon that issued EnvVar. The CLI
// sends the token only to that daemon; any other daemon (an isolated dev or
// lab daemon with its own TALLPINE_RUN_FILE) gets the install token from its own
// run-file directory, as before.
var RunFileEnvVar = brand.Env("SESSION_TOKEN_RUN_FILE")

// AltEnvVar and AltRunFileEnvVar carry the same two values under names without
// KEY, TOKEN, SECRET or PASSWORD in them. Harnesses that sanitize the environment
// of the commands they run (Codex's default shell environment policy, DeepSeek
// Harness's subprocess scrub) drop every credential-shaped name, so the primary
// pair never reaches the CLI there and it fell back to the install token, which
// the daemon refuses for per-session requests such as GitHub credentials.
var (
	AltEnvVar        = brand.Env("SESSION_AUTH")
	AltRunFileEnvVar = brand.Env("SESSION_AUTH_RUN_FILE")
)

// FromEnv returns the session credential and the run file of the daemon that
// issued it, from the primary variables or, when a harness stripped those, from
// the alias pair. The token and run file always come from the same pair.
func FromEnv() (token, issuerRunFile string) {
	if token = strings.TrimSpace(os.Getenv(EnvVar)); token != "" {
		return token, os.Getenv(RunFileEnvVar)
	}
	if token = strings.TrimSpace(os.Getenv(AltEnvVar)); token != "" {
		return token, os.Getenv(AltRunFileEnvVar)
	}
	return "", ""
}

// BoundTo reports whether a token issued for issuerRunFile may be sent to the
// daemon whose run file is runFile. Both must be set and name the same file.
func BoundTo(issuerRunFile, runFile string) bool {
	issuer, target := strings.TrimSpace(issuerRunFile), strings.TrimSpace(runFile)
	if issuer == "" || target == "" {
		return false
	}
	if filepath.Clean(issuer) == filepath.Clean(target) {
		return true
	}
	a, errA := filepath.EvalSymlinks(issuer)
	b, errB := filepath.EvalSymlinks(target)
	return errA == nil && errB == nil && a == b
}

// Kind names whose credential a token is.
type Kind string

const (
	// KindSession is a worker or orchestrator session's launch credential.
	KindSession Kind = "session"
	// KindReviewer is a reviewer agent's credential, bound to its review.
	KindReviewer Kind = "reviewer"
)

const (
	sessionPrefix  = "vos1."
	reviewerPrefix = "vor1."
	keyLabel       = "vo-session-token-key-v1"
)

// Authority mints and verifies scoped tokens under one install token.
type Authority struct {
	key []byte
}

// New derives an authority from the install token. An empty install token
// yields nil: with daemon auth off there is nothing to scope.
func New(installToken string) *Authority {
	if strings.TrimSpace(installToken) == "" {
		return nil
	}
	mac := hmac.New(sha256.New, []byte(installToken))
	_, _ = mac.Write([]byte(keyLabel))
	return &Authority{key: mac.Sum(nil)}
}

// IssueSession returns the token for one launch of session id, identified by
// the browser-capability verifier persisted for that launch. It returns "" when
// either is missing, so a caller never injects an unverifiable token.
func (a *Authority) IssueSession(id domain.SessionID, launchVerifier string) string {
	if a == nil || !validID(string(id)) || launchVerifier == "" {
		return ""
	}
	return sessionPrefix + string(id) + "." + a.mac(KindSession, string(id), launchVerifier)
}

// IssueReviewer returns the token for the reviewer of review reviewID, which
// reviews worker. It returns "" when either id is missing.
func (a *Authority) IssueReviewer(reviewID string, worker domain.SessionID) string {
	if a == nil || !validID(reviewID) || worker == "" {
		return ""
	}
	return reviewerPrefix + reviewID + "." + a.mac(KindReviewer, reviewID, string(worker))
}

// VerifySession reports whether token is session id's credential for the
// launch whose verifier is launchVerifier. Comparison is constant time.
func (a *Authority) VerifySession(token string, id domain.SessionID, launchVerifier string) bool {
	expected := a.IssueSession(id, launchVerifier)
	return expected != "" && hmac.Equal([]byte(token), []byte(expected))
}

// VerifyReviewer reports whether token is the reviewer credential for
// reviewID reviewing worker. Comparison is constant time.
func (a *Authority) VerifyReviewer(token, reviewID string, worker domain.SessionID) bool {
	expected := a.IssueReviewer(reviewID, worker)
	return expected != "" && hmac.Equal([]byte(token), []byte(expected))
}

// Parse returns the kind and the session or review id a token claims. The
// claim is unverified until VerifySession or VerifyReviewer accepts it.
func Parse(token string) (Kind, string, bool) {
	var kind Kind
	var rest string
	switch {
	case strings.HasPrefix(token, sessionPrefix):
		kind, rest = KindSession, strings.TrimPrefix(token, sessionPrefix)
	case strings.HasPrefix(token, reviewerPrefix):
		kind, rest = KindReviewer, strings.TrimPrefix(token, reviewerPrefix)
	default:
		return "", "", false
	}
	// The MAC is base64url and never contains a dot; the id may (project ids
	// such as "acme.dev" prefix session ids).
	cut := strings.LastIndexByte(rest, '.')
	if cut <= 0 || cut == len(rest)-1 {
		return "", "", false
	}
	id := rest[:cut]
	if !validID(id) {
		return "", "", false
	}
	return kind, id, true
}

// IsScoped reports whether token has the shape of a scoped token, as opposed
// to the install token.
func IsScoped(token string) bool {
	_, _, ok := Parse(token)
	return ok
}

func (a *Authority) mac(kind Kind, id, binding string) string {
	mac := hmac.New(sha256.New, a.key)
	_, _ = mac.Write([]byte(kind))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(id))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(binding))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// validID keeps ids that can appear in a header value and a request path.
func validID(id string) bool {
	if id == "" || len(id) > 256 {
		return false
	}
	for _, r := range id {
		if r <= ' ' || r == 0x7f || r == '/' || r == '\\' || r == '%' || r > 0x7e {
			return false
		}
	}
	return true
}
