package ghaccount

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeGH struct {
	loginDelay time.Duration
	logins     []string
	tokens     map[string]string
	loginCall  atomic.Int32
}

func (f *fakeGH) Logins(context.Context) ([]string, error) {
	f.loginCall.Add(1)
	time.Sleep(f.loginDelay)
	return append([]string(nil), f.logins...), nil
}

func (f *fakeGH) Token(_ context.Context, login string) (string, error) {
	return f.tokens[login], nil
}

// fakePush grants push by token -> repo key list.
type fakePush struct {
	allow map[string][]string
	calls atomic.Int32
	err   error
	delay time.Duration
}

func (f *fakePush) CanPush(ctx context.Context, token string, repo Repo) (bool, error) {
	f.calls.Add(1)
	select {
	case <-time.After(f.delay):
	case <-ctx.Done():
		return false, ctx.Err()
	}
	if f.err != nil {
		return false, f.err
	}
	for _, k := range f.allow[token] {
		if k == repo.Key() {
			return true, nil
		}
	}
	return false, nil
}

func newTestResolver(logins []string, allow map[string][]string) (*Resolver, *fakeGH, *fakePush, *time.Time) {
	gh := &fakeGH{logins: logins, tokens: map[string]string{}}
	for _, l := range logins {
		gh.tokens[l] = "tok-" + l
	}
	push := &fakePush{allow: allow}
	now := time.Unix(1000, 0)
	r := &Resolver{GH: gh, Push: push, Now: func() time.Time { return now }, SSHHost: func(_ context.Context, a string) (string, error) {
		if a == "github-work" {
			return "github.com", nil
		}
		return a, nil
	}}
	return r, gh, push, &now
}

func TestResolvePrefersOwnerAccountWithPush(t *testing.T) {
	r, _, _, _ := newTestResolver([]string{"zed", "acme"}, map[string][]string{
		"tok-zed": {"acme/app"}, "tok-acme": {"acme/app"},
	})
	if got := r.ResolveRemote(context.Background(), "git@github.com:acme/app.git", ""); got != "acme" {
		t.Fatalf("got %q, want owner account acme", got)
	}
}

func TestResolveFallsBackToAnyAccountWithPush(t *testing.T) {
	r, _, _, _ := newTestResolver([]string{"me", "other"}, map[string][]string{"tok-other": {"org/repo"}})
	if got := r.ResolveRemote(context.Background(), "https://github.com/org/repo", ""); got != "other" {
		t.Fatalf("got %q, want other", got)
	}
}

func TestResolveOverrideWinsWithoutAPICalls(t *testing.T) {
	r, gh, push, _ := newTestResolver([]string{"a", "b"}, map[string][]string{"tok-a": {"o/r"}})
	if got := r.ResolveRemote(context.Background(), "git@github.com:o/r", " b "); got != "b" {
		t.Fatalf("got %q, want override b", got)
	}
	if gh.loginCall.Load() != 0 || push.calls.Load() != 0 {
		t.Fatal("override must not list accounts or call the API")
	}
}

func TestResolveUnchangedCases(t *testing.T) {
	cases := map[string]struct {
		logins []string
		remote string
	}{
		"single account": {[]string{"only"}, "git@github.com:o/r"},
		"no gh accounts": {nil, "git@github.com:o/r"},
		"gitlab remote":  {[]string{"a", "b"}, "git@gitlab.com:o/r.git"},
		"https gitlab":   {[]string{"a", "b"}, "https://gitlab.com/o/r"},
		"unknown alias":  {[]string{"a", "b"}, "git@bitbucket-x:o/r"},
		"no push at all": {[]string{"a", "b"}, "git@github.com:o/r"},
	}
	for name, c := range cases {
		r, _, push, _ := newTestResolver(c.logins, nil)
		if got := r.ResolveRemote(context.Background(), c.remote, ""); got != "" {
			t.Errorf("%s: got %q, want empty (unchanged)", name, got)
		}
		if len(c.logins) < 2 && push.calls.Load() != 0 {
			t.Errorf("%s: made API calls", name)
		}
	}
}

func TestResolveSSHAlias(t *testing.T) {
	r, _, _, _ := newTestResolver([]string{"a", "b"}, map[string][]string{"tok-b": {"owner/repo"}})
	if got := r.ResolveRemote(context.Background(), "git@github-work:owner/repo.git", ""); got != "b" {
		t.Fatalf("alias with HostName github.com: got %q, want b", got)
	}
}

func TestResolveCachesAndInvalidatesOnAuthChange(t *testing.T) {
	r, gh, push, now := newTestResolver([]string{"a", "b"}, map[string][]string{"tok-b": {"o/r"}})
	ctx := context.Background()
	r.ResolveRemote(ctx, "git@github.com:o/r", "")
	first := push.calls.Load()
	r.ResolveRemote(ctx, "git@github.com:o/r", "")
	if push.calls.Load() != first {
		t.Fatal("second resolve within TTL must hit the cache")
	}
	// Auth change: a new account appears once the login list is refreshed.
	gh.logins = []string{"a", "b", "c"}
	gh.tokens["c"] = "tok-c"
	*now = now.Add(2 * time.Minute)
	r.ResolveRemote(ctx, "git@github.com:o/r", "")
	if push.calls.Load() == first {
		t.Fatal("changed account list must re-run the permission checks")
	}
	// TTL expiry.
	before := push.calls.Load()
	*now = now.Add(11 * time.Minute)
	r.ResolveRemote(ctx, "git@github.com:o/r", "")
	if push.calls.Load() == before {
		t.Fatal("expired entry must be re-checked")
	}
}

func TestResolverNeverLogsTokens(t *testing.T) {
	var buf bytes.Buffer
	r, _, _, _ := newTestResolver([]string{"a", "b"}, map[string][]string{"tok-b": {"o/r"}})
	r.Logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	r.ResolveRemote(context.Background(), "git@github.com:o/r", "")
	if strings.Contains(buf.String(), "tok-") {
		t.Fatalf("token leaked into logs: %s", buf.String())
	}
	for _, p := range r.picks {
		if strings.Contains(p.login+p.fingerprint, "tok-") {
			t.Fatal("token persisted in pick cache")
		}
	}
}

func TestResolveRepoOverrideWins(t *testing.T) {
	r, gh, push, _ := newTestResolver([]string{"a", "b"}, map[string][]string{"tok-a": {"o/r"}})
	if got := r.ResolveRepo(context.Background(), Repo{Owner: "o", Name: "r"}, "b"); got != "b" {
		t.Fatalf("got %q, want override b", got)
	}
	if gh.loginCall.Load() != 0 || push.calls.Load() != 0 {
		t.Fatal("override must not list accounts or call the API")
	}
}

// The CanPush failure branch logs the error; neither it nor any other log line
// may carry a token.
func TestResolverNeverLogsTokensOnPermissionError(t *testing.T) {
	var buf bytes.Buffer
	r, _, push, _ := newTestResolver([]string{"a", "b"}, nil)
	push.err = errors.New("boom")
	r.Logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	if got := r.ResolveRemote(context.Background(), "git@github.com:o/r", ""); got != "" {
		t.Fatalf("got %q, want unchanged on permission error", got)
	}
	if !strings.Contains(buf.String(), "permission check failed") {
		t.Fatalf("error branch not exercised: %q", buf.String())
	}
	if strings.Contains(buf.String(), "tok-") {
		t.Fatalf("token leaked into logs: %s", buf.String())
	}
	if len(r.picks) != 0 {
		t.Fatal("a failed check must not be cached as definitive")
	}
}

// Concurrent resolves of one repo run the permission check once.
func TestConcurrentResolveRunsChecksOnce(t *testing.T) {
	r, gh, push, _ := newTestResolver([]string{"a", "b"}, map[string][]string{"tok-a": {"o/r"}, "tok-b": {"o/r"}})
	push.delay = 150 * time.Millisecond
	gh.loginDelay = 100 * time.Millisecond
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := r.ResolveRepo(context.Background(), Repo{Owner: "o", Name: "r"}, ""); got != "a" {
				t.Errorf("got %q", got)
			}
		}()
	}
	wg.Wait()
	if n := push.calls.Load(); n != 1 {
		t.Fatalf("permission checks = %d, want 1 shared run", n)
	}
	if n := gh.loginCall.Load(); n != 1 {
		t.Fatalf("gh auth status runs = %d, want 1", n)
	}
}

func TestTokenRejectsInvalidLogin(t *testing.T) {
	r, gh, _, _ := newTestResolver([]string{"a", "b"}, nil)
	gh.tokens["-oFoo"] = "tok-x"
	for _, bad := range []string{"-oFoo", "a b", ""} {
		if _, err := r.Token(context.Background(), bad); !errors.Is(err, ErrInvalidLogin) {
			t.Errorf("Token(%q) err = %v, want ErrInvalidLogin", bad, err)
		}
	}
}

// A caller with a short deadline (or one that is cancelled) may lead a cold
// pick; concurrent callers must still get the login, and the leader gives up
// alone instead of waiting out the shared work.
func TestShortLivedLeaderDoesNotPoisonFollowers(t *testing.T) {
	cases := map[string]func() (context.Context, context.CancelFunc){
		"short deadline": func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), 50*time.Millisecond)
		},
		"cancelled": func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			time.AfterFunc(50*time.Millisecond, cancel)
			return ctx, cancel
		},
	}
	for name, leaderCtx := range cases {
		t.Run(name, func(t *testing.T) {
			r, _, push, _ := newTestResolver([]string{"a", "b"}, map[string][]string{"tok-a": {"o/r"}})
			push.delay = 300 * time.Millisecond
			repo := Repo{Owner: "o", Name: "r"}
			lctx, cancel := leaderCtx()
			defer cancel()
			var leaderGot string
			var leaderTook time.Duration
			done := make(chan struct{})
			go func() {
				defer close(done)
				start := time.Now()
				leaderGot = r.ResolveRepo(lctx, repo, "")
				leaderTook = time.Since(start)
			}()
			time.Sleep(20 * time.Millisecond) // the leader is in flight now
			if got := r.ResolveRepo(context.Background(), repo, ""); got != "a" {
				t.Fatalf("follower got %q, want a: the leader's deadline poisoned the shared work", got)
			}
			<-done
			if leaderGot != "" || leaderTook > 250*time.Millisecond {
				t.Fatalf("leader got %q after %v, want it to give up alone at its own deadline", leaderGot, leaderTook)
			}
		})
	}
}

// A timed-out shared run is not cached as "no push".
func TestTimedOutSharedRunIsNotCached(t *testing.T) {
	r, _, push, _ := newTestResolver([]string{"a", "b"}, map[string][]string{"tok-a": {"o/r"}})
	push.delay = time.Hour
	r.StepTimeout = 30 * time.Millisecond
	if got := r.ResolveRepo(context.Background(), Repo{Owner: "o", Name: "r"}, ""); got != "" {
		t.Fatalf("got %q", got)
	}
	if len(r.picks) != 0 {
		t.Fatal("a timed-out run must not be cached")
	}
}

type fakeIdentity struct{ login string }

func (f fakeIdentity) Login(context.Context, string) (string, error) { return f.login, nil }

func TestTokenRefusesWrongAccount(t *testing.T) {
	r, gh, _, _ := newTestResolver([]string{"a", "b"}, nil)
	gh.tokens["b"] = "tok-b"
	r.Identity = fakeIdentity{login: "a"}
	if _, err := r.Token(context.Background(), "b"); !errors.Is(err, ErrWrongAccount) {
		t.Fatalf("err = %v, want ErrWrongAccount", err)
	}
	r.Identity = fakeIdentity{login: "B"}
	if tok, err := r.Token(context.Background(), "b"); err != nil || tok != "tok-b" {
		t.Fatalf("tok=%q err=%v", tok, err)
	}
}
