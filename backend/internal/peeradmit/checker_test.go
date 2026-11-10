package peeradmit

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/daemonsock"
	"tallpine.eu/tallpine/backend/internal/procmem"
)

type fakeVerifier struct {
	result Signature
	calls  atomic.Int32
}

func (f *fakeVerifier) Verify([8]uint32) Signature { f.calls.Add(1); return f.result }

type fakeRoots struct {
	agent, shell []int
	err          error
	reads        atomic.Int32
}

func (f *fakeRoots) AgentRoots(context.Context, Credential) ([]int, error) {
	f.reads.Add(1)
	return f.agent, f.err
}

func (f *fakeRoots) ShellRoots(context.Context) ([]int, error) {
	f.reads.Add(1)
	return f.shell, f.err
}

func unixObservation(pid int, kind CredentialKind) Observation {
	return Observation{
		Peer:       daemonsock.Peer{Transport: daemonsock.TransportUnix, PID: pid},
		Credential: Credential{Kind: kind, SessionID: "s1"},
		Method:     "GET", Path: "/api/v1/sessions/s1/secret?token=x",
	}
}

func startChecker(t *testing.T, opts Options) *Checker {
	t.Helper()
	if opts.Mode == "" {
		opts.Mode = ModeReport
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if opts.SessionOf == nil {
		opts.SessionOf = func(pid int) (int, error) { return pid, nil }
	}
	c := New(opts)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Run(ctx)
	return c
}

func flush(t *testing.T, c *Checker) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Flush(ctx); err != nil {
		t.Fatal(err)
	}
}

func chainOf(chain ...int) func(int) (Lineage, error) {
	return func(int) (Lineage, error) {
		return Lineage{Chain: chain, Session: 7, Executable: "/bin/caller"}, nil
	}
}

func TestCheckerRecordsWouldRefuseWithPeerDetails(t *testing.T) {
	c := startChecker(t, Options{Inspect: chainOf(500), Roots: &fakeRoots{shell: []int{9}}, Verifier: &fakeVerifier{result: SignatureOther}})
	obs := unixObservation(500, CredentialInstall)
	obs.Peer.HasAuditToken = true
	c.Observe(obs)
	flush(t, c)
	report := c.Report()
	if len(report.WouldRefuse) != 1 || report.Mode != ModeReport {
		t.Fatalf("report = %+v", report)
	}
	rec := report.WouldRefuse[0]
	if rec.Executable != "/bin/caller" || rec.TokenClass != "install" || rec.LastPID != 500 || rec.Reason != ReasonInstallTokenOutside {
		t.Fatalf("record = %+v", rec)
	}
	if strings.Contains(rec.Route, "?") || strings.Contains(rec.Route, "s1") {
		t.Fatalf("route must not carry ids or a query: %q", rec.Route)
	}
}

func TestCheckerCountsAdmittedClasses(t *testing.T) {
	roots := &fakeRoots{agent: []int{500}, shell: []int{500}}
	verifier := &fakeVerifier{result: SignatureDesktop}
	c := startChecker(t, Options{Inspect: chainOf(500), Roots: roots, Verifier: verifier})
	desktop := unixObservation(500, CredentialInstall)
	desktop.Peer.HasAuditToken = true
	c.Observe(desktop)
	c.Observe(unixObservation(500, CredentialSession))
	flush(t, c)
	report := c.Report()
	if report.Admitted[ClassDesktop] != 1 || report.Admitted[ClassAgent] != 1 || len(report.WouldRefuse) != 0 {
		t.Fatalf("report = %+v", report)
	}
}

func TestCheckerSignatureCachedPerAuditToken(t *testing.T) {
	verifier := &fakeVerifier{result: SignatureDesktop}
	c := startChecker(t, Options{Inspect: chainOf(500), Roots: &fakeRoots{}, Verifier: verifier})
	obs := unixObservation(500, CredentialInstall)
	obs.Peer.HasAuditToken = true
	c.Observe(obs)
	c.Observe(obs)
	flush(t, c)
	if got := verifier.calls.Load(); got != 1 {
		t.Fatalf("verifier calls = %d, want 1 (Electron opens a connection per request)", got)
	}
}

func TestCheckerStaleRootsForceOneFreshRead(t *testing.T) {
	now := time.Unix(1000, 0)
	roots := &fakeRoots{agent: []int{9}}
	c := startChecker(t, Options{Inspect: chainOf(500), Roots: roots, Now: func() time.Time { return now }})
	c.Observe(unixObservation(500, CredentialSession))
	flush(t, c)
	if n := roots.reads.Load(); n != 1 {
		t.Fatalf("reads after first request = %d, want 1: a cold cache read is already fresh", n)
	}
	roots.agent = []int{500} // a new pane opens
	now = now.Add(2 * time.Second)
	c.Observe(unixObservation(500, CredentialSession))
	flush(t, c)
	report := c.Report()
	if report.Admitted[ClassAgent] != 1 {
		t.Fatalf("new pane within the cache window must be found by a fresh read: %+v", report)
	}
}

func TestCheckerExitedPeerRecordedAndUnreadableRootsFailClosed(t *testing.T) {
	gone := func(int) (Lineage, error) { return Lineage{}, procmem.ErrProcessGone }
	c := startChecker(t, Options{Inspect: gone, Roots: &fakeRoots{}})
	c.Observe(unixObservation(500, CredentialSession))
	flush(t, c)
	if got := c.Report().WouldRefuse[0].Reason; got != ReasonPeerExited {
		t.Fatalf("reason = %s", got)
	}

	failing := startChecker(t, Options{Inspect: chainOf(500), Roots: &fakeRoots{err: errors.New("no tmux")}})
	failing.Observe(unixObservation(500, CredentialSession))
	flush(t, failing)
	if got := failing.Report().WouldRefuse[0].Reason; got != ReasonSessionRootsUnknown {
		t.Fatalf("reason = %s", got)
	}
}

func TestCheckerOffObservesNothing(t *testing.T) {
	inspected := false
	c := New(Options{Mode: ModeOff, ModeReason: "unsigned", Inspect: func(int) (Lineage, error) {
		inspected = true
		return Lineage{}, nil
	}})
	c.Observe(unixObservation(500, CredentialInstall))
	if inspected || len(c.queue) != 0 || c.Report().Mode != ModeOff {
		t.Fatalf("off mode must do nothing: inspected=%v queued=%d", inspected, len(c.queue))
	}
}

func TestNilCheckerIsSafe(t *testing.T) {
	var c *Checker
	c.Observe(Observation{})
	c.Run(context.Background())
	if err := c.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.Report().Mode != ModeOff {
		t.Fatal("nil checker reports off")
	}
}

func TestObserveNeverBlocksAndCountsDrops(t *testing.T) {
	c := New(Options{Mode: ModeReport, Inspect: chainOf(500), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}) // Run never started.
	for range queueSize + 3 {
		c.Observe(unixObservation(500, CredentialInstall))
	}
	if got := c.Report().Dropped; got != 3 {
		t.Fatalf("dropped = %d, want 3", got)
	}
}

func TestObserveContainsPanics(t *testing.T) {
	c := New(Options{Mode: ModeReport, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Inspect: func(int) (Lineage, error) { panic("boom") }})
	c.Observe(unixObservation(500, CredentialInstall))
	if got := c.Report().Dropped; got != 1 {
		t.Fatalf("dropped = %d", got)
	}
}

func TestClassificationPanicKeepsWorkerAlive(t *testing.T) {
	roots := &panicOnceRoots{}
	c := startChecker(t, Options{Inspect: chainOf(500), Roots: roots})
	c.Observe(unixObservation(500, CredentialSession))
	flush(t, c)
	c.Observe(unixObservation(500, CredentialSession))
	flush(t, c)
	report := c.Report()
	if report.Dropped != 1 || report.Admitted[ClassAgent] != 1 {
		t.Fatalf("worker must survive a panic: %+v", report)
	}
}

type panicOnceRoots struct{ calls atomic.Int32 }

func (p *panicOnceRoots) AgentRoots(context.Context, Credential) ([]int, error) {
	if p.calls.Add(1) == 1 {
		panic("boom")
	}
	return []int{500}, nil
}

func (p *panicOnceRoots) ShellRoots(context.Context) ([]int, error) { return nil, nil }

func TestRecordTableIsBounded(t *testing.T) {
	c := startChecker(t, Options{Inspect: chainOf(500), Roots: &fakeRoots{agent: []int{9}}})
	for i := range maxRecords + 5 {
		obs := unixObservation(500, CredentialSession)
		obs.Path = "/api/v1/resource" + strings.Repeat("x", i)
		c.Observe(obs)
	}
	flush(t, c)
	report := c.Report()
	if len(report.WouldRefuse) != maxRecords || report.Overflow != 5 {
		t.Fatalf("records = %d overflow = %d", len(report.WouldRefuse), report.Overflow)
	}
}

func TestRootSessionsOnlyWhenRootLeadsThem(t *testing.T) {
	c := startChecker(t, Options{
		// Peer is outside the tree, in session 200, which root 100 belongs to
		// but does not lead (the daemon-owned session case).
		Inspect:   func(int) (Lineage, error) { return Lineage{Chain: []int{500}, Session: 200}, nil },
		Roots:     &fakeRoots{agent: []int{100}},
		SessionOf: func(int) (int, error) { return 200, nil },
	})
	c.Observe(unixObservation(500, CredentialSession))
	flush(t, c)
	if got := c.Report().WouldRefuse[0].Reason; got != ReasonScopedTokenOutside {
		t.Fatalf("a session the root does not lead must not admit: %s", got)
	}
}

func TestRouteLabel(t *testing.T) {
	for _, tc := range []struct{ method, path, want string }{
		{"POST", "/shutdown", "POST /shutdown"},
		{"GET", "/api/v1/sessions", "GET /api/v1/sessions"},
		{"GET", "/api/v1/sessions/abc/files", "GET /api/v1/sessions/*"},
	} {
		if got := routeLabel(tc.method, tc.path); got != tc.want {
			t.Errorf("routeLabel(%s) = %q, want %q", tc.path, got, tc.want)
		}
	}
}
