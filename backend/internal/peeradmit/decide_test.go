package peeradmit

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/daemonsock"
)

func TestDefaultMode(t *testing.T) {
	cases := []struct {
		name, goos, team string
		noSignatureCheck bool
		want             Mode
	}{
		{"signed macOS records", "darwin", "ABCDE12345", false, ModeReport},
		{"signed macOS without signature checking is off", "darwin", "ABCDE12345", true, ModeOff},
		{"unsigned macOS is off", "darwin", "", false, ModeOff},
		{"malformed team is off", "darwin", `X" or anchor apple`, false, ModeOff},
		{"linux is off", "linux", "ABCDE12345", false, ModeOff},
		{"windows is off", "windows", "ABCDE12345", false, ModeOff},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, why := DefaultMode(tc.goos, tc.team, !tc.noSignatureCheck)
			if got != tc.want || why == "" {
				t.Fatalf("DefaultMode = %q, %q; want %q with a reason", got, why, tc.want)
			}
		})
	}
}

func TestDecide(t *testing.T) {
	const peerPID, rootPID, otherPID, rootSession = 500, 100, 900, 100
	unixPeer := func(mutate func(*Peer)) Peer {
		p := Peer{Transport: daemonsock.TransportUnix, Chain: []int{peerPID, 300}, Session: 700, Signature: SignatureOther}
		if mutate != nil {
			mutate(&p)
		}
		return p
	}
	descendant := func(p *Peer) { p.Chain = []int{peerPID, rootPID, 50} }
	sameSession := func(p *Peer) { p.Session = rootSession }
	signed := func(p *Peer) { p.Signature = SignatureDesktop }
	install := Credential{Kind: CredentialInstall}
	session := Credential{Kind: CredentialSession, SessionID: "s1"}
	reviewer := Credential{Kind: CredentialReview, ReviewID: "r1"}
	none := Credential{Kind: CredentialNone}
	under := RootSet{PIDs: []int{rootPID}, Sessions: []int{rootSession}}
	elsewhere := RootSet{PIDs: []int{otherPID}, Sessions: []int{otherPID}}
	unreadable := RootSet{Err: "tmux failed"}

	cases := []struct {
		name       string
		peer       Peer
		credential Credential
		roots      Roots
		class      Class
		reason     Reason
	}{
		{"signed desktop with install token", unixPeer(signed), install, Roots{}, ClassDesktop, ReasonSignedDesktop},
		{"desktop-opened shell child", unixPeer(descendant), install, Roots{Shell: under}, ClassDesktopShell, ReasonUnderShellRoots},
		{"desktop-opened shell, same session after parent exit", unixPeer(sameSession), install, Roots{Shell: under}, ClassDesktopShell, ReasonUnderShellRoots},
		{"agent descends from its session roots", unixPeer(descendant), session, Roots{Agent: under}, ClassAgent, ReasonUnderSessionRoots},
		{"agent shares its session's POSIX session", unixPeer(sameSession), session, Roots{Agent: under}, ClassAgent, ReasonUnderSessionRoots},
		{"reviewer under its own roots", unixPeer(descendant), reviewer, Roots{Agent: under}, ClassAgent, ReasonUnderSessionRoots},

		{"install token from a plain Terminal", unixPeer(nil), install, Roots{Shell: elsewhere}, ClassNotAdmitted, ReasonInstallTokenOutside},
		{"install token from another signed app", unixPeer(nil), install, Roots{}, ClassNotAdmitted, ReasonInstallTokenOutside},
		{"install token, signature unreadable", unixPeer(func(p *Peer) { p.Signature = SignatureUnknown }), install, Roots{}, ClassNotAdmitted, ReasonSignatureUnknown},
		{"install token, shell roots unreadable", unixPeer(nil), install, Roots{Shell: unreadable}, ClassNotAdmitted, ReasonShellRootsUnknown},
		{"agent holding the install token is not a shell", unixPeer(descendant), install, Roots{Agent: under, Shell: elsewhere}, ClassNotAdmitted, ReasonInstallTokenOutside},
		{"scoped token from outside its session", unixPeer(nil), session, Roots{Agent: elsewhere}, ClassNotAdmitted, ReasonScopedTokenOutside},
		{"scoped token, child of another session", unixPeer(descendant), session, Roots{Agent: elsewhere}, ClassNotAdmitted, ReasonScopedTokenOutside},
		{"scoped token, roots unreadable", unixPeer(nil), session, Roots{Agent: unreadable}, ClassNotAdmitted, ReasonSessionRootsUnknown},
		{"scoped token from the signed desktop admits nothing", unixPeer(signed), session, Roots{Agent: elsewhere}, ClassNotAdmitted, ReasonScopedTokenOutside},
		{"scoped token does not borrow shell roots", unixPeer(descendant), session, Roots{Agent: elsewhere, Shell: under}, ClassNotAdmitted, ReasonScopedTokenOutside},

		{"TCP has no peer identity", Peer{Transport: daemonsock.TransportTCP, Signature: SignatureDesktop}, install, Roots{Shell: under}, ClassNotAdmitted, ReasonTCPNoIdentity},
		{"TCP scoped token", Peer{Transport: daemonsock.TransportTCP}, session, Roots{Agent: under}, ClassNotAdmitted, ReasonTCPNoIdentity},
		{"exited peer fails closed", Peer{Transport: daemonsock.TransportUnix, Exited: true, Signature: SignatureDesktop}, install, Roots{}, ClassNotAdmitted, ReasonPeerExited},
		{"unreadable peer fails closed", Peer{Transport: daemonsock.TransportUnix, Err: "getsockopt"}, session, Roots{Agent: under}, ClassNotAdmitted, ReasonPeerUnreadable},
		{"empty chain fails closed", Peer{Transport: daemonsock.TransportUnix, Signature: SignatureDesktop}, install, Roots{}, ClassNotAdmitted, ReasonPeerUnreadable},
		{"no credential", unixPeer(descendant), none, Roots{Agent: under, Shell: under}, ClassNotAdmitted, ReasonNoCredential},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Decide(tc.peer, tc.credential, tc.roots)
			if got.Class != tc.class || got.Reason != tc.reason {
				t.Fatalf("Decide = %+v, want %s/%s", got, tc.class, tc.reason)
			}
		})
	}
}

func TestSessionZeroAndOneNeverMatch(t *testing.T) {
	peer := Peer{Transport: daemonsock.TransportUnix, Chain: []int{500}, Session: 1, Signature: SignatureOther}
	got := Decide(peer, Credential{Kind: CredentialInstall}, Roots{Shell: RootSet{Sessions: []int{1}}})
	if got.Class != ClassNotAdmitted {
		t.Fatalf("session 1 must not admit: %+v", got)
	}
}
