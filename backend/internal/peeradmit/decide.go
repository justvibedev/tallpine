// Package peeradmit decides whether the process behind a daemon request is one
// Tallpine itself started (B631). It answers from kernel-reported peer facts,
// never from a request header.
//
// On a signed macOS build the decision is enforced: Checker.Admit refuses a
// request whose peer is not admitted. The owner can turn it off from the
// desktop app (app_settings daemon.peerCheck); report mode records only.
package peeradmit

import (
	"slices"

	"tallpine.eu/tallpine/backend/internal/daemonsock"
	"tallpine.eu/tallpine/backend/internal/releasesigner"
)

// Mode is how the peer check acts on a decision.
type Mode string

// Mode values.
const (
	ModeOff     Mode = "off"
	ModeReport  Mode = "report"
	ModeEnforce Mode = "enforce"
)

// DefaultMode is the build's effective mode and why, from facts fixed at build
// time. No environment variable can change it.
func DefaultMode(goos, teamID string, signatureCheck bool) (Mode, string) {
	if goos != "darwin" {
		return ModeOff, "not macOS"
	}
	if teamID == "" {
		return ModeOff, "unsigned or development build"
	}
	if !releasesigner.TeamIDPattern.MatchString(teamID) {
		return ModeOff, "malformed build Team ID"
	}
	if !signatureCheck {
		return ModeOff, "built without signature checking"
	}
	return ModeEnforce, "signed build"
}

// NewRootSet builds a RootSet from a lookup's result. Only a root that leads
// its own POSIX session contributes that session.
func NewRootSet(pids []int, err error, sessionOf func(int) (int, error)) RootSet {
	set := RootSet{PIDs: pids}
	if err != nil {
		set.Err = err.Error()
	}
	for _, pid := range pids {
		if session, err := sessionOf(pid); err == nil && session == pid {
			set.Sessions = append(set.Sessions, session)
		}
	}
	return set
}

// Class is who a request came from.
type Class string

// Class values.
const (
	ClassDesktop      Class = "desktop"
	ClassAgent        Class = "agent"
	ClassDesktopShell Class = "desktop_shell"
	ClassNotAdmitted  Class = "not_admitted"
)

// Reason says why a class was chosen; it is what the records aggregate on.
type Reason string

// Reason values.
const (
	ReasonSignedDesktop       Reason = "signed_desktop"
	ReasonUnderSessionRoots   Reason = "under_session_roots"
	ReasonUnderShellRoots     Reason = "under_shell_roots"
	ReasonTCPNoIdentity       Reason = "tcp_no_peer_identity"
	ReasonPeerExited          Reason = "peer_exited"
	ReasonPeerUnreadable      Reason = "peer_unreadable"
	ReasonNoCredential        Reason = "no_credential" // #nosec G101 -- Reason label, not a credential.
	ReasonInstallTokenOutside Reason = "install_token_outside_desktop"
	ReasonSignatureUnknown    Reason = "signature_unknown"
	ReasonShellRootsUnknown   Reason = "shell_roots_unavailable"
	ReasonScopedTokenOutside  Reason = "scoped_token_outside_session"
	ReasonSessionRootsUnknown Reason = "session_roots_unavailable"
)

// Signature is the outcome of checking a peer's code signature.
type Signature int

// Signature values.
const (
	SignatureUnknown Signature = iota
	SignatureDesktop
	SignatureOther
)

// Peer is the facts about the caller that the decision uses.
type Peer struct {
	Transport daemonsock.PeerTransport
	// Chain is the peer pid and its ancestors.
	Chain []int
	// Session is the peer's POSIX session id.
	Session   int
	Exited    bool
	Err       string
	Signature Signature
}

// CredentialKind is the class of token a request presented.
type CredentialKind string

// CredentialKind values.
const (
	CredentialNone    CredentialKind = "none"
	CredentialInstall CredentialKind = "install"
	CredentialSession CredentialKind = "session"
	CredentialReview  CredentialKind = "reviewer"
)

// Credential is the verified token a request presented. SessionID and
// ReviewID name whose roots a scoped token must run under.
type Credential struct {
	Kind      CredentialKind
	SessionID string
	ReviewID  string
}

func (c Credential) scoped() bool {
	return c.Kind == CredentialSession || c.Kind == CredentialReview
}

// RootSet is one set of runtime roots. Sessions holds only sessions those
// roots lead, so a root that merely sits in someone else's session (the
// daemon's, a login shell's) admits nothing through it. Err is set when the
// roots could not be read.
type RootSet struct {
	PIDs     []int
	Sessions []int
	Err      string
}

func (r RootSet) contains(peer Peer) bool {
	for _, pid := range peer.Chain {
		if slices.Contains(r.PIDs, pid) {
			return true
		}
	}
	return peer.Session > 1 && slices.Contains(r.Sessions, peer.Session)
}

// Roots are the root sets that can admit a request: Agent for the presented
// scoped token's own session or review, Shell for desktop-opened terminals.
type Roots struct {
	Agent RootSet
	Shell RootSet
}

// Decision is the outcome for one request.
type Decision struct {
	Class  Class
	Reason Reason
}

func notAdmitted(reason Reason) Decision {
	return Decision{Class: ClassNotAdmitted, Reason: reason}
}

// Decide classifies one request. A peer it cannot identify is not admitted.
// Being a daemon child is never a reason: chat agents are daemon children and
// stay scoped.
func Decide(peer Peer, credential Credential, roots Roots) Decision {
	switch {
	case peer.Transport != daemonsock.TransportUnix:
		return notAdmitted(ReasonTCPNoIdentity)
	case peer.Exited:
		return notAdmitted(ReasonPeerExited)
	case peer.Err != "" || len(peer.Chain) == 0:
		return notAdmitted(ReasonPeerUnreadable)
	}
	switch {
	case credential.Kind == CredentialInstall:
		return decideInstall(peer, roots.Shell)
	case credential.scoped():
		return decideScoped(peer, roots.Agent)
	}
	return notAdmitted(ReasonNoCredential)
}

func decideInstall(peer Peer, shells RootSet) Decision {
	if peer.Signature == SignatureDesktop {
		return Decision{Class: ClassDesktop, Reason: ReasonSignedDesktop}
	}
	if shells.contains(peer) {
		return Decision{Class: ClassDesktopShell, Reason: ReasonUnderShellRoots}
	}
	switch {
	case shells.Err != "":
		return notAdmitted(ReasonShellRootsUnknown)
	case peer.Signature == SignatureUnknown:
		return notAdmitted(ReasonSignatureUnknown)
	}
	return notAdmitted(ReasonInstallTokenOutside)
}

func decideScoped(peer Peer, agent RootSet) Decision {
	if agent.contains(peer) {
		return Decision{Class: ClassAgent, Reason: ReasonUnderSessionRoots}
	}
	if agent.Err != "" {
		return notAdmitted(ReasonSessionRootsUnknown)
	}
	return notAdmitted(ReasonScopedTokenOutside)
}
