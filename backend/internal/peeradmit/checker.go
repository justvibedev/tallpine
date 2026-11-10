package peeradmit

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"tallpine.eu/tallpine/backend/internal/daemonsock"
	"tallpine.eu/tallpine/backend/internal/procmem"
)

const (
	queueSize        = 1024
	maxRecords       = 256
	maxRootCache     = 512
	maxSignatures    = 64
	rootCacheTTL     = 3 * time.Second
	rootRefreshEvery = time.Second
	resolveTimeout   = 5 * time.Second
)

// SignatureVerifier checks the process an audit token names.
type SignatureVerifier interface {
	Verify(token [8]uint32) Signature
}

// RootResolver reads runtime root pids live; nothing about peers is stored.
type RootResolver interface {
	// AgentRoots returns the roots of credential's own session or review.
	AgentRoots(ctx context.Context, credential Credential) ([]int, error)
	// ShellRoots returns the roots of every desktop-opened terminal.
	ShellRoots(ctx context.Context) ([]int, error)
}

// Lineage is what the kernel says about a peer pid.
type Lineage struct {
	Chain      []int
	Session    int
	Executable string
}

// Options configures a Checker. Inspect and SessionOf default to procmem.
type Options struct {
	Mode       Mode
	ModeReason string
	TeamID     string
	Verifier   SignatureVerifier
	Roots      RootResolver
	Log        *slog.Logger
	Inspect    func(pid int) (Lineage, error)
	SessionOf  func(pid int) (int, error)
	Now        func() time.Time
}

// Observation is one authenticated request to classify.
type Observation struct {
	Peer       daemonsock.Peer
	Credential Credential
	Method     string
	Path       string
}

// Record is one aggregated would-refuse line.
type Record struct {
	Executable string
	TokenClass string
	Route      string
	Reason     Reason
	Count      int64
	LastPID    int
	LastSeen   time.Time
}

// Report is the Checker's state for diagnostics.
type Report struct {
	Mode        Mode
	ModeReason  string
	TeamID      string
	Admitted    map[Class]int64
	WouldRefuse []Record
	Overflow    int64
	Dropped     int64
}

type pending struct {
	obs     Observation
	lineage Lineage
	peer    Peer
	flushed chan struct{}
}

type recordKey struct {
	executable, tokenClass, route string
	reason                        Reason
}

type cachedRoots struct {
	roots RootSet
	at    time.Time
}

// Checker classifies requests off the request path and records the outcome.
// Observe does only cheap kernel reads and never blocks; slow work (signature
// checks, tmux reads) runs on Run's goroutine. A nil Checker observes nothing.
type Checker struct {
	opts    Options
	queue   chan pending
	dropped atomic.Int64

	mu       sync.Mutex
	admitted map[Class]int64
	records  map[recordKey]*Record
	overflow int64

	// Only Run's goroutine touches these.
	roots      map[string]cachedRoots
	signatures map[[8]uint32]Signature
}

// New builds a Checker. Call Run to start classifying.
func New(opts Options) *Checker {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.Inspect == nil {
		opts.Inspect = inspectProcess
	}
	if opts.SessionOf == nil {
		opts.SessionOf = procmem.SessionOf
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Checker{
		opts:       opts,
		queue:      make(chan pending, queueSize),
		admitted:   make(map[Class]int64),
		records:    make(map[recordKey]*Record),
		roots:      make(map[string]cachedRoots),
		signatures: make(map[[8]uint32]Signature),
	}
}

func inspectProcess(pid int) (Lineage, error) {
	chain, err := procmem.ParentChain(pid)
	if err != nil {
		return Lineage{}, err
	}
	session, err := procmem.SessionOf(pid)
	if err != nil {
		return Lineage{}, err
	}
	return Lineage{Chain: chain, Session: session, Executable: procmem.ExecutablePath(pid)}, nil
}

// Observe captures the peer now, while it is certainly in its request, and
// queues the classification. It cannot affect the request: a full queue only
// counts a drop and a panic is contained.
func (c *Checker) Observe(obs Observation) {
	if c == nil || c.opts.Mode == ModeOff {
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			c.dropped.Add(1)
			c.opts.Log.Error("peer check: observe panicked", "panic", recovered)
		}
	}()
	item := pending{obs: obs, peer: Peer{Transport: obs.Peer.Transport, Exited: obs.Peer.Exited, Err: obs.Peer.Err}}
	if obs.Peer.Transport == daemonsock.TransportUnix && obs.Peer.Err == "" && !obs.Peer.Exited {
		lineage, err := c.opts.Inspect(obs.Peer.PID)
		switch {
		case errors.Is(err, procmem.ErrProcessGone):
			item.peer.Exited = true
		case err != nil:
			item.peer.Err = err.Error()
		default:
			item.lineage = lineage
			item.peer.Chain = lineage.Chain
			item.peer.Session = lineage.Session
		}
	}
	select {
	case c.queue <- item:
	default:
		c.dropped.Add(1)
	}
}

// Run classifies queued observations until ctx ends.
func (c *Checker) Run(ctx context.Context) {
	if c == nil || c.opts.Mode == ModeOff {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case item := <-c.queue:
			c.process(ctx, item)
		}
	}
}

// Flush waits until everything queued before it has been classified.
func (c *Checker) Flush(ctx context.Context) error {
	if c == nil || c.opts.Mode == ModeOff {
		return nil
	}
	done := make(chan struct{})
	select {
	case c.queue <- pending{flushed: done}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Checker) process(ctx context.Context, item pending) {
	if item.flushed != nil {
		close(item.flushed)
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			c.dropped.Add(1)
			c.opts.Log.Error("peer check: classification panicked", "panic", recovered)
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()
	item.peer.Signature = c.signature(item.obs)
	decision := Decide(item.peer, item.obs.Credential, c.resolve(ctx, item, false))
	if decision.Class == ClassNotAdmitted && rootsMayBeStale(decision.Reason) {
		// A pane opened since the cached read must not count as outside.
		decision = Decide(item.peer, item.obs.Credential, c.resolve(ctx, item, true))
	}
	c.record(item, decision)
}

func rootsMayBeStale(reason Reason) bool {
	switch reason {
	case ReasonInstallTokenOutside, ReasonSignatureUnknown, ReasonShellRootsUnknown,
		ReasonScopedTokenOutside, ReasonSessionRootsUnknown:
		return true
	}
	return false
}

func (c *Checker) signature(obs Observation) Signature {
	if obs.Credential.Kind != CredentialInstall || !obs.Peer.HasAuditToken || c.opts.Verifier == nil {
		return SignatureUnknown
	}
	if cached, ok := c.signatures[obs.Peer.AuditToken]; ok {
		return cached
	}
	result := c.opts.Verifier.Verify(obs.Peer.AuditToken)
	if len(c.signatures) >= maxSignatures {
		clear(c.signatures)
	}
	c.signatures[obs.Peer.AuditToken] = result
	return result
}

func (c *Checker) resolve(ctx context.Context, item pending, fresh bool) Roots {
	if c.opts.Roots == nil {
		return Roots{}
	}
	credential := item.obs.Credential
	switch {
	case credential.scoped():
		key := "agent\x00" + credential.SessionID + "\x00" + credential.ReviewID
		return Roots{Agent: c.rootSet(ctx, key, fresh, func(ctx context.Context) ([]int, error) {
			return c.opts.Roots.AgentRoots(ctx, credential)
		})}
	case credential.Kind == CredentialInstall && item.peer.Signature != SignatureDesktop:
		return Roots{Shell: c.rootSet(ctx, "shell", fresh, c.opts.Roots.ShellRoots)}
	}
	return Roots{}
}

func (c *Checker) rootSet(ctx context.Context, key string, fresh bool, read func(context.Context) ([]int, error)) RootSet {
	now := c.opts.Now()
	if cached, ok := c.roots[key]; ok {
		age := now.Sub(cached.at)
		if age < rootRefreshEvery || (!fresh && age < rootCacheTTL) {
			return cached.roots
		}
	}
	pids, err := read(ctx)
	set := NewRootSet(pids, err, c.opts.SessionOf)
	if len(c.roots) >= maxRootCache {
		clear(c.roots)
	}
	c.roots[key] = cachedRoots{roots: set, at: now}
	return set
}

func (c *Checker) record(item pending, decision Decision) {
	if decision.Class != ClassNotAdmitted {
		c.mu.Lock()
		c.admitted[decision.Class]++
		c.mu.Unlock()
		return
	}
	route := routeLabel(item.obs.Method, item.obs.Path)
	tokenClass := string(item.obs.Credential.Kind)
	executable := item.lineage.Executable
	c.opts.Log.Warn("peer check: would refuse", "mode", c.opts.Mode, "pid", item.obs.Peer.PID,
		"executable", executable, "tokenClass", tokenClass, "route", route, "reason", decision.Reason)
	key := recordKey{executable: executable, tokenClass: tokenClass, route: route, reason: decision.Reason}
	now := c.opts.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	rec, ok := c.records[key]
	if !ok {
		if len(c.records) >= maxRecords {
			c.overflow++
			return
		}
		rec = &Record{Executable: executable, TokenClass: tokenClass, Route: route, Reason: decision.Reason}
		c.records[key] = rec
	}
	rec.Count++
	rec.LastPID = item.obs.Peer.PID
	rec.LastSeen = now
}

// routeLabel keeps the method and leading path only: ids and later segments
// would make the table unbounded, and a query string is never recorded.
func routeLabel(method, path string) string {
	const keep = 3
	path = strings.TrimPrefix(path, "/")
	segments := strings.SplitN(path, "/", keep+1)
	if len(segments) > keep {
		return method + " /" + strings.Join(segments[:keep], "/") + "/*"
	}
	return method + " /" + path
}

// Report returns the current mode and records for diagnostics.
func (c *Checker) Report() Report {
	if c == nil {
		return Report{Mode: ModeOff}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	report := Report{
		Mode: c.opts.Mode, ModeReason: c.opts.ModeReason, TeamID: c.opts.TeamID,
		Admitted: make(map[Class]int64, len(c.admitted)),
		Overflow: c.overflow, Dropped: c.dropped.Load(),
	}
	for class, count := range c.admitted {
		report.Admitted[class] = count
	}
	for _, rec := range c.records {
		report.WouldRefuse = append(report.WouldRefuse, *rec)
	}
	sort.Slice(report.WouldRefuse, func(i, j int) bool {
		a, b := report.WouldRefuse[i], report.WouldRefuse[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.Executable+a.Route < b.Executable+b.Route
	})
	return report
}
