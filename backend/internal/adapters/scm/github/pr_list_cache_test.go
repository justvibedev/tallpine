package github

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/ports"
)

type rewriteTransport struct{ target string }

func (r rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r2 := req.Clone(req.Context())
	r2.URL.Scheme, r2.URL.Host = "http", strings.TrimPrefix(r.target, "http://")
	return http.DefaultTransport.RoundTrip(r2)
}

func TestListPRsByRepoDoesNotCacheBodiesAndFiltersClientSide(t *testing.T) {
	var mu sync.Mutex
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		queries = append(queries, r.URL.RawQuery)
		mu.Unlock()
		w.Header().Set("ETag", `"abc"`)
		_, _ = fmt.Fprint(w, `[{"number":2,"updated_at":"2026-01-02T00:00:00Z"},{"number":1,"updated_at":"2026-01-01T00:00:00Z"}]`)
	}))
	defer srv.Close()
	p, err := NewProvider(ProviderOptions{Token: StaticTokenSource("t"), HTTPClient: &http.Client{Transport: rewriteTransport{srv.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	repo := ports.SCMRepo{Provider: "github", Owner: "o", Name: "r"}
	for i := 0; i < 5; i++ {
		after := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Second)
		got, err := p.ListPRsByRepo(ctx(), repo, after)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Number != 2 {
			t.Fatalf("poll %d filtered = %+v, want only PR 2", i, got)
		}
	}
	for _, q := range queries {
		if strings.Contains(q, "since") {
			t.Fatalf("since sent: %s", q)
		}
	}
	c := p.clientFor("")
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.bodyOut) != 0 || len(c.etagOut) != 0 {
		t.Fatalf("PR list polls stored cache entries: %d bodies", len(c.bodyOut))
	}
}

func TestETagCacheHasByteCap(t *testing.T) {
	c := NewClient(ClientOptions{})
	big := make([]byte, cacheMaxBytes/4+1)
	for i := 0; i < 20; i++ {
		c.storeCacheEntry(fmt.Sprintf("k%d", i), "e", big)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cacheLen > cacheMaxBytes {
		t.Fatalf("cacheLen %d exceeds cap %d", c.cacheLen, cacheMaxBytes)
	}
	total := 0
	for _, b := range c.bodyOut {
		total += len(b)
	}
	if total != c.cacheLen || total > cacheMaxBytes {
		t.Fatalf("accounting total=%d cacheLen=%d", total, c.cacheLen)
	}
}

func TestStoreCacheEntryOrphanETagKeyDoesNotLeakOrPanic(t *testing.T) {
	c := NewClient(ClientOptions{})
	c.etagOut["orphan"] = "stale" // as left by a late 304 after eviction
	body := make([]byte, cacheMaxBytes/2+1)
	for i := 0; i < 6; i++ {
		c.storeCacheEntry("orphan", "e", body)
		delete(c.bodyOut, "orphan") // simulate eviction without LRU bookkeeping drift
		c.cacheLRU, c.cacheLen = nil, 0
		c.etagOut["orphan"] = "stale"
	}
	c.storeCacheEntry("a", "e", body)
	c.storeCacheEntry("orphan", "e", body)
	c.storeCacheEntry("b", "e", body)
	c.mu.Lock()
	defer c.mu.Unlock()
	total := 0
	for _, b := range c.bodyOut {
		total += len(b)
	}
	if total != c.cacheLen || c.cacheLen > cacheMaxBytes || len(c.cacheLRU) != len(c.bodyOut) {
		t.Fatalf("total=%d cacheLen=%d lru=%d bodies=%d", total, c.cacheLen, len(c.cacheLRU), len(c.bodyOut))
	}
}
