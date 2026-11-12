package peeradmit

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/daemonsock"
)

func enforcing(t *testing.T, opts Options) *Checker {
	t.Helper()
	opts.Mode = ModeEnforce
	if opts.LaunchGrace == 0 {
		opts.LaunchGrace = -1
	}
	return startChecker(t, opts)
}

func TestAdmitEnforcesEachCallerClass(t *testing.T) {
	cases := []struct {
		name      string
		kind      CredentialKind
		signature Signature
		chain     []int
		roots     *fakeRoots
		transport daemonsock.PeerTransport
		want      bool
	}{
		{"signed desktop with install token", CredentialInstall, SignatureDesktop, []int{500}, &fakeRoots{}, daemonsock.TransportUnix, true},
		{"desktop-opened shell with install token", CredentialInstall, SignatureOther, []int{500, 40}, &fakeRoots{shell: []int{40}}, daemonsock.TransportUnix, true},
		{"install token outside desktop and shells", CredentialInstall, SignatureOther, []int{500, 1}, &fakeRoots{shell: []int{40}}, daemonsock.TransportUnix, false},
		{"agent under its session roots", CredentialSession, SignatureUnknown, []int{500, 30}, &fakeRoots{agent: []int{30}}, daemonsock.TransportUnix, true},
		{"stolen scoped token outside the session", CredentialSession, SignatureUnknown, []int{500, 1}, &fakeRoots{agent: []int{30}}, daemonsock.TransportUnix, false},
		{"reviewer under its roots", CredentialReview, SignatureUnknown, []int{500, 30}, &fakeRoots{agent: []int{30}}, daemonsock.TransportUnix, true},
		// A chat agent is a daemon child: its chain reaches the daemon, which
		// is no shell root, so the install token admits nothing for it.
		{"chat agent holding the install token", CredentialInstall, SignatureOther, []int{500, 77, 1}, &fakeRoots{shell: []int{40}}, daemonsock.TransportUnix, false},
		{"TCP with install token", CredentialInstall, SignatureDesktop, nil, &fakeRoots{}, daemonsock.TransportTCP, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := enforcing(t, Options{Inspect: chainOf(tc.chain...), Roots: tc.roots, Verifier: &fakeVerifier{result: tc.signature}})
			obs := unixObservation(500, tc.kind)
			obs.Peer.Transport = tc.transport
			obs.Peer.HasAuditToken = true
			if got := c.Admit(context.Background(), obs); got != tc.want {
				t.Fatalf("Admit = %v, want %v (report %+v)", got, tc.want, c.Report())
			}
		})
	}
}

func TestAdmitRecordsRefusalsAsRefused(t *testing.T) {
	var logs bytes.Buffer
	c := enforcing(t, Options{Inspect: chainOf(500, 1), Roots: &fakeRoots{}, Verifier: &fakeVerifier{result: SignatureOther},
		Log: slog.New(slog.NewTextHandler(&logs, nil))})
	obs := unixObservation(500, CredentialInstall)
	obs.Peer.HasAuditToken = true
	if c.Admit(context.Background(), obs) {
		t.Fatal("install token outside the desktop was admitted")
	}
	report := c.Report()
	if report.Mode != ModeEnforce || len(report.WouldRefuse) != 1 || report.WouldRefuse[0].Reason != ReasonInstallTokenOutside {
		t.Fatalf("report = %+v", report)
	}
	if !strings.Contains(logs.String(), `msg="peer check: refused"`) || !strings.Contains(logs.String(), "mode=enforce") {
		t.Fatalf("log = %s", logs.String())
	}
}

func TestAdmitReportModeAdmitsAndRecords(t *testing.T) {
	c := startChecker(t, Options{Inspect: chainOf(500, 1), Roots: &fakeRoots{}, Verifier: &fakeVerifier{result: SignatureOther}})
	if !c.Admit(context.Background(), unixObservation(500, CredentialInstall)) {
		t.Fatal("report mode refused a request")
	}
	flush(t, c)
	if len(c.Report().WouldRefuse) != 1 {
		t.Fatalf("report mode did not record: %+v", c.Report())
	}
}

func TestOffSwitchAppliesLive(t *testing.T) {
	c := enforcing(t, Options{Inspect: chainOf(500, 1), Roots: &fakeRoots{}, Verifier: &fakeVerifier{result: SignatureOther}})
	obs := unixObservation(500, CredentialInstall)
	if c.Admit(context.Background(), obs) {
		t.Fatal("enforcing admitted an outside caller")
	}
	c.SetDisabled(true)
	if mode, why := c.ModeAndReason(); mode != ModeOff || !strings.Contains(why, "desktop app") {
		t.Fatalf("mode after off = %q %q", mode, why)
	}
	if !c.Admit(context.Background(), obs) {
		t.Fatal("off switch did not admit")
	}
	c.SetDisabled(false)
	if c.Admit(context.Background(), obs) {
		t.Fatal("back to default did not enforce again")
	}
	if report := c.Report(); report.BuildMode != ModeEnforce || report.Disabled {
		t.Fatalf("report = %+v", report)
	}
}

func TestUnsignedBuildStaysInactive(t *testing.T) {
	mode, _ := DefaultMode("darwin", "", true)
	c := New(Options{Mode: mode, Inspect: chainOf(500, 1), Roots: &fakeRoots{}})
	c.SetDisabled(false)
	if c.Active() || c.Mode() != ModeOff || !c.Admit(context.Background(), unixObservation(500, CredentialInstall)) {
		t.Fatalf("unsigned build checked peers: mode %q", c.Mode())
	}
	if !c.DesktopCaller(context.Background()) {
		t.Fatal("unsigned build guarded the off switch")
	}
}

func TestDesktopCallerIsOnlyTheSignedApp(t *testing.T) {
	for _, tc := range []struct {
		name      string
		signature Signature
		shell     []int
		want      bool
	}{
		{"signed desktop", SignatureDesktop, nil, true},
		{"desktop-opened shell", SignatureOther, []int{500}, false},
		{"outside caller", SignatureOther, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := enforcing(t, Options{Inspect: chainOf(500), Roots: &fakeRoots{shell: tc.shell}, Verifier: &fakeVerifier{result: tc.signature}})
			c.SetDisabled(true) // the switch is guarded even while off
			ctx := daemonsock.ContextWithPeer(context.Background(), daemonsock.Peer{Transport: daemonsock.TransportUnix, PID: 500, HasAuditToken: true})
			if got := c.DesktopCaller(ctx); got != tc.want {
				t.Fatalf("DesktopCaller = %v, want %v", got, tc.want)
			}
		})
	}
}

// lateRoots returns no roots until stored is set, like a session whose
// runtime handle is written after its agent's first hook ran.
type lateRoots struct {
	stored atomic.Bool
	reads  atomic.Int32
}

func (l *lateRoots) AgentRoots(context.Context, Credential) ([]int, error) {
	l.reads.Add(1)
	if l.stored.Load() {
		return []int{30}, nil
	}
	return nil, nil
}

func (l *lateRoots) ShellRoots(context.Context) ([]int, error) { return nil, nil }

func TestDesktopCallerWithoutPeerIsRefused(t *testing.T) {
	c := enforcing(t, Options{Inspect: chainOf(500), Roots: &fakeRoots{}, Verifier: &fakeVerifier{result: SignatureDesktop}})
	if c.DesktopCaller(context.Background()) {
		t.Fatal("a caller with no socket peer counted as the desktop")
	}
}

func TestAdmitWaitsForALaunchBeingStored(t *testing.T) {
	var clock atomic.Int64
	roots := &lateRoots{}
	c := enforcing(t, Options{
		Inspect: chainOf(500, 30), Roots: roots, LaunchGrace: time.Second, RetryEvery: 10 * time.Millisecond,
		// Each read advances the clock past the refresh floor, so every retry reads again.
		Now: func() time.Time { return time.Unix(clock.Add(2), 0) },
	})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); time.Sleep(50 * time.Millisecond); roots.stored.Store(true) }()
	if !c.Admit(context.Background(), unixObservation(500, CredentialSession)) {
		t.Fatalf("an agent whose launch was being stored was refused after %d reads", roots.reads.Load())
	}
	wg.Wait()
}

func TestAdmitRefusesAStolenTokenAfterTheGrace(t *testing.T) {
	roots := &lateRoots{}
	c := enforcing(t, Options{Inspect: chainOf(500, 1), Roots: roots, LaunchGrace: 30 * time.Millisecond, RetryEvery: 10 * time.Millisecond})
	if c.Admit(context.Background(), unixObservation(500, CredentialSession)) {
		t.Fatal("a scoped token outside its session was admitted")
	}
}

func TestAdmitFailsClosedOnPanic(t *testing.T) {
	c := enforcing(t, Options{Inspect: func(int) (Lineage, error) { panic("boom") }, Roots: &fakeRoots{}})
	if c.Admit(context.Background(), unixObservation(500, CredentialInstall)) {
		t.Fatal("a panicking check admitted the request")
	}
}

func TestNilCheckerAdmits(t *testing.T) {
	var c *Checker
	if !c.Admit(context.Background(), unixObservation(1, CredentialInstall)) || c.Mode() != ModeOff {
		t.Fatal("nil checker refused")
	}
	c.SetDisabled(true)
}
