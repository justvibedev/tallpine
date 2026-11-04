package controllers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	backlogsvc "tallpine.eu/tallpine/backend/internal/service/backlog"
	reportsvc "tallpine.eu/tallpine/backend/internal/service/report"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

// backlogAPI wires the real controllers, services and SQLite store. The
// X-Test-Agent header ("worker:<session>" or "orchestrator:<session>") stands
// in for the scoped-token middleware, which has its own tests.
type backlogAPI struct {
	t       *testing.T
	handler http.Handler
	store   *sqlite.Store
}

func newBacklogAPI(t *testing.T) *backlogAPI {
	t.Helper()
	s := sqlitetest.MustOpen(t)
	for _, id := range []string{"p1", "p2"} {
		if err := s.UpsertProject(context.Background(), domain.ProjectRecord{ID: id, Path: "/tmp/" + id, RegisteredAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	backlog := backlogsvc.New(backlogsvc.Deps{Store: s})
	reports := reportsvc.New(reportsvc.Deps{Store: s, FileDeferred: backlog.FileDeferred})
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if h := req.Header.Get("X-Test-Agent"); h != "" {
				role, sess, _ := strings.Cut(h, ":")
				a := requestscope.Agent{Role: requestscope.AgentRole(role), SessionID: domain.SessionID(sess), ProjectID: "p1"}
				req = req.WithContext(requestscope.WithAgent(req.Context(), a))
			}
			next.ServeHTTP(w, req)
		})
	})
	r.Route("/api/v1", func(r chi.Router) {
		(&controllers.BacklogController{Svc: backlog}).Register(r)
		(&controllers.ReportsController{Svc: reports}).Register(r)
	})
	return &backlogAPI{t: t, handler: r, store: s}
}

func (a *backlogAPI) do(method, path, agent string, body any) (int, map[string]any) {
	a.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			a.t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, "/api/v1"+path, &buf)
	if agent != "" {
		req.Header.Set("X-Test-Agent", agent)
	}
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	out := map[string]any{}
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			a.t.Fatalf("%s %s: bad json %q", method, path, rec.Body.String())
		}
	}
	return rec.Code, out
}

func (a *backlogAPI) must(want int, method, path, agent string, body any) map[string]any {
	a.t.Helper()
	code, out := a.do(method, path, agent, body)
	if code != want {
		a.t.Fatalf("%s %s = %d, want %d: %v", method, path, code, want, out)
	}
	return out
}

func itemOf(out map[string]any) map[string]any { return out["item"].(map[string]any) }

func TestBacklogAPIRoundTrip(t *testing.T) {
	a := newBacklogAPI(t)
	base := "/projects/p1/backlog"

	parent := itemOf(a.must(201, "POST", base, "", map[string]any{"title": "Big feature", "doneWhen": "ships", "scope": "pr"}))
	if parent["ref"] != "B1" || parent["state"] != "open" || parent["scope"] != "pr" || parent["origin"].(map[string]any)["kind"] != "human" {
		t.Fatalf("parent = %v", parent)
	}
	child := itemOf(a.must(201, "POST", base, "", map[string]any{"title": "Part one", "parent": "B1", "hold": "later"}))
	other := itemOf(a.must(201, "POST", base, "", map[string]any{"title": "Standalone idea", "note": "# md"}))
	if child["parentRef"] != "B1" || other["ref"] != "B3" {
		t.Fatalf("child=%v other=%v", child, other)
	}

	// List filters.
	count := func(q string) int { return len(a.must(200, "GET", base+q, "", nil)["items"].([]any)) }
	if count("") != 3 || count("?state=open") != 3 || count("?state=done") != 0 || count("?parent=B1") != 1 ||
		count("?held=held") != 1 || count("?held=unheld") != 2 || count("?q=IDEA") != 1 {
		t.Fatal("list filters returned unexpected counts")
	}
	a.must(400, "GET", base+"?state=bogus", "", nil)

	// Get by ref and by id, with the parent's child count.
	got := itemOf(a.must(200, "GET", base+"/B1", "", nil))
	if got["childCount"].(float64) != 1 || itemOf(a.must(200, "GET", base+"/"+other["id"].(string), "", nil))["ref"] != "B3" {
		t.Fatalf("get = %v", got)
	}
	a.must(404, "GET", base+"/B99", "", nil)
	a.must(404, "GET", "/projects/p2/backlog/B1", "", nil)

	// Partial patch: other fields survive; empty parent detaches; bad scope refused.
	patched := itemOf(a.must(200, "PATCH", base+"/B2", "", map[string]any{"title": "Part 1", "parent": ""}))
	if patched["title"] != "Part 1" || patched["hold"] != "later" || patched["parentRef"] != nil {
		t.Fatalf("patched = %v", patched)
	}
	a.must(200, "PATCH", base+"/B2", "", map[string]any{"parent": "B1"})
	a.must(400, "PATCH", base+"/B2", "", map[string]any{"scope": "sideways"})
	a.must(409, "PATCH", base+"/B3", "", map[string]any{"parent": "B2"}) // B2 is a child
	a.must(400, "PATCH", base+"/B2", "", map[string]any{"unknown": 1})

	// Custom fields.
	f := itemOf(a.must(200, "PUT", base+"/B3/fields/branch", "", map[string]any{"value": "feat/x"}))
	if f["fields"].(map[string]any)["branch"] != "feat/x" {
		t.Fatalf("fields = %v", f["fields"])
	}
	cleared := itemOf(a.must(200, "DELETE", base+"/B3/fields/branch", "", nil))
	if len(cleared["fields"].(map[string]any)) != 0 {
		t.Fatalf("fields not cleared: %v", cleared["fields"])
	}
	a.must(400, "PUT", base+"/B3/fields/BAD_KEY", "", map[string]any{"value": "x"})

	// Links, including a blocked_by cycle.
	l := itemOf(a.must(200, "POST", base+"/B3/links", "", map[string]any{"kind": "pr", "ref": "https://github.com/o/r/pull/1"}))
	if len(l["links"].([]any)) != 1 {
		t.Fatalf("links = %v", l["links"])
	}
	a.must(200, "POST", base+"/B1/links", "", map[string]any{"kind": "blocked_by", "ref": "B3"})
	a.must(409, "POST", base+"/B3/links", "", map[string]any{"kind": "blocked_by", "ref": "B1"})
	a.must(200, "DELETE", base+"/B3/links", "", map[string]any{"kind": "pr", "ref": "https://github.com/o/r/pull/1"})

	// next skips the parent (B1) and held B2, so B3 is first; closing B3 leaves nothing.
	next := a.must(200, "GET", base+"/next", "", nil)
	if itemOf(next)["ref"] != "B3" {
		t.Fatalf("next = %v", next)
	}
	closed := itemOf(a.must(200, "POST", base+"/B3/state", "", map[string]any{"state": "done", "reason": "shipped"}))
	if closed["state"] != "done" || closed["closedReason"] != "shipped" {
		t.Fatalf("closed = %v", closed)
	}
	if a.must(200, "GET", base+"/next", "", nil)["item"] != nil {
		t.Fatal("next should be empty once B3 is done")
	}
	a.must(400, "POST", base+"/B3/state", "", map[string]any{"state": "bogus"})

	// Reorder: B2 before B1.
	a.must(204, "POST", base+"/reorder", "", map[string]any{"ref": "B2", "before": "B1"})
	items := a.must(200, "GET", base, "", nil)["items"].([]any)
	if items[0].(map[string]any)["ref"] != "B2" {
		t.Fatalf("order = %v", items)
	}
	a.must(400, "POST", base+"/reorder", "", map[string]any{"ref": "B2"})
}

func TestBacklogAPIAgentRules(t *testing.T) {
	a := newBacklogAPI(t)
	base := "/projects/p1/backlog"
	w := itemOf(a.must(201, "POST", base, "worker:w1", map[string]any{"title": "Follow-up", "open": true, "excerpt": "saw it"}))
	origin := w["origin"].(map[string]any)
	if w["state"] != "inbox" || origin["kind"] != "worker" || origin["sessionId"] != "w1" || origin["excerpt"] != "saw it" {
		t.Fatalf("worker item = %v", w)
	}
	o := itemOf(a.must(201, "POST", base, "orchestrator:o1", map[string]any{"title": "Idea"}))
	if o["state"] != "inbox" || o["origin"].(map[string]any)["kind"] != "orchestrator" {
		t.Fatalf("orchestrator item = %v", o)
	}
	oo := itemOf(a.must(201, "POST", base, "orchestrator:o1", map[string]any{"title": "Asked for", "open": true}))
	if oo["state"] != "open" {
		t.Fatalf("orchestrator --open item = %v", oo)
	}
}

func TestReportDeferFilesInboxItemsThroughTheRealPath(t *testing.T) {
	a := newBacklogAPI(t)
	sess, err := a.store.CreateSession(context.Background(), domain.SessionRecord{
		ProjectID: "p1", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Activity:  domain.Activity{State: domain.ActivityActive, LastActivityAt: time.Now().UTC()},
		Metadata:  domain.SessionMetadata{Branch: "feat/x", WorkspacePath: "/ws"},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	agent := "worker:" + string(sess.ID)

	out := a.must(201, "POST", "/reports", agent, map[string]any{
		"state": "checkpoint", "note": "halfway", "defer": []string{"Fix truncation", "Add retry", "  fix  TRUNCATION "},
	})
	deferred := out["deferred"].([]any)
	if len(deferred) != 3 || deferred[0].(map[string]any)["status"] != "filed" || deferred[2].(map[string]any)["status"] != "merged" ||
		deferred[2].(map[string]any)["ref"] != deferred[0].(map[string]any)["ref"] {
		t.Fatalf("deferred = %v", deferred)
	}
	items := a.must(200, "GET", "/projects/p1/backlog?state=inbox", "", nil)["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("inbox = %d, want 2 (duplicate merged)", len(items))
	}
	first := items[0].(map[string]any)["origin"].(map[string]any)
	if first["kind"] != "worker" || first["sessionId"] != string(sess.ID) || first["excerpt"] != "halfway" {
		t.Fatalf("origin = %v", first)
	}

	// Over the per-report limit: rejected, report not stored, nothing filed.
	a.must(400, "POST", "/reports", agent, map[string]any{"message": "x", "defer": []string{"a", "b", "c", "d"}})
	a.must(400, "POST", "/reports", agent, map[string]any{"message": "x", "defer": []string{strings.Repeat("y", 201)}})
	a.must(400, "POST", "/reports", agent, map[string]any{"message": "x", "defer": []string{"  "}})
	if n := len(a.must(200, "GET", "/projects/p1/backlog", "", nil)["items"].([]any)); n != 2 {
		t.Fatalf("items after rejected reports = %d, want 2", n)
	}
	// A defer without any report content is refused by the existing validation.
	a.must(400, "POST", "/reports", agent, map[string]any{"defer": []string{"orphan"}})
}

func TestBacklogAPIWorkerCreateIsCappedDedupedAndRestricted(t *testing.T) {
	a := newBacklogAPI(t)
	base := "/projects/p1/backlog"
	w := "worker:w1"

	// Duplicates (normalized) merge into the existing item instead of filing again.
	first := itemOf(a.must(201, "POST", base, w, map[string]any{"title": "Fix the thing", "note": "body"}))
	again := itemOf(a.must(201, "POST", base, w, map[string]any{"title": "  fix  THE thing "}))
	if again["ref"] != first["ref"] || first["note"] != "body" {
		t.Fatalf("first=%v again=%v", first, again)
	}
	if n := len(a.must(200, "GET", base, "", nil)["items"].([]any)); n != 1 {
		t.Fatalf("items = %d, want 1 after merge", n)
	}

	// Parent, hold, scope and agent notes are refused for workers.
	a.must(201, "POST", base, "orchestrator:o1", map[string]any{"title": "Parent", "open": true})
	for name, body := range map[string]map[string]any{
		"parent": {"title": "c", "parent": "B2"},
		"hold":   {"title": "c", "hold": "x"},
		"scope":  {"title": "c", "scope": "pr"},
		"notes":  {"title": "c", "agentNotes": "x"},
	} {
		code, out := a.do("POST", base, w, body)
		if code != 400 {
			t.Errorf("%s: code=%d body=%v, want 400", name, code, out)
		}
	}
	if got := itemOf(a.must(200, "GET", base+"/B2", "", nil)); got["childCount"].(float64) != 0 {
		t.Fatalf("worker attached a child: %v", got)
	}

	// The per-session cap applies to `backlog add` too (1 filed so far).
	for i := 1; i < domain.MaxDeferPerSession; i++ {
		a.must(201, "POST", base, w, map[string]any{"title": "item " + strings.Repeat("x", i)})
	}
	code, out := a.do("POST", base, w, map[string]any{"title": "one too many"})
	if code != 429 || !strings.Contains(out["message"].(string), "not filed") {
		t.Fatalf("over cap: code=%d out=%v", code, out)
	}
	// A duplicate still merges at the cap.
	a.must(201, "POST", base, w, map[string]any{"title": "Fix the thing"})
}

func TestBacklogAPIDeleteAndPurge(t *testing.T) {
	a := newBacklogAPI(t)
	base := "/projects/p1/backlog"
	mk := func(title string, extra map[string]any) string {
		body := map[string]any{"title": title, "open": true}
		for k, v := range extra {
			body[k] = v
		}
		return itemOf(a.must(201, "POST", base, "", body))["ref"].(string)
	}
	setState := func(ref, state string) {
		a.must(200, "POST", base+"/"+ref+"/state", "", map[string]any{"state": state})
	}
	deleted := func(out map[string]any) []string {
		var refs []string
		for _, r := range out["deleted"].([]any) {
			refs = append(refs, r.(string))
		}
		return refs
	}

	open, done, parent := mk("open", nil), mk("done", nil), mk("parent", nil)
	kid := mk("kid", map[string]any{"parent": parent})
	setState(done, "done")

	// Open items are refused with a clear conflict; closed ones go.
	code, out := a.do("DELETE", base+"/"+open, "", nil)
	if code != 409 || !strings.Contains(out["message"].(string), "only done or dropped") {
		t.Fatalf("delete open = %d %v", code, out)
	}
	a.must(200, "GET", base+"/"+open, "", nil)
	// A parent with an open child is refused; once the child is closed both go.
	setState(parent, "done") // closed by hand: the open child stays open
	if code, out := a.do("DELETE", base+"/"+parent, "", nil); code != 409 || !strings.Contains(out["message"].(string), kid) {
		t.Fatalf("delete parent with open child = %d %v", code, out)
	}
	a.must(200, "GET", base+"/"+kid, "", nil)
	setState(kid, "dropped")
	if got := deleted(a.must(200, "DELETE", base+"/"+parent, "", nil)); len(got) != 2 || got[0] != kid || got[1] != parent {
		t.Fatalf("parent delete = %v, want child then parent", got)
	}
	a.must(404, "GET", base+"/"+kid, "", nil)
	if got := deleted(a.must(200, "DELETE", base+"/"+done, "orchestrator:o1", nil)); len(got) != 1 || got[0] != done {
		t.Fatalf("deleted = %v", got)
	}
	a.must(404, "GET", base+"/"+done, "", nil)

	// Purge validates its states and reports refs.
	if code, _ := a.do("POST", base+"/purge", "", map[string]any{"states": []string{"open"}}); code != 400 {
		t.Fatalf("purge open states = %d", code)
	}
	if code, _ := a.do("POST", base+"/purge", "", map[string]any{"states": []string{}}); code != 400 {
		t.Fatalf("purge no states = %d", code)
	}
	setState(open, "dropped")
	dry := a.must(200, "POST", base+"/purge", "", map[string]any{"states": []string{"dropped"}, "dryRun": true})
	if got := deleted(dry); len(got) != 1 || got[0] != open {
		t.Fatalf("dry run = %v", dry)
	}
	a.must(200, "GET", base+"/"+open, "", nil)
	// Nothing closed more than 30 days ago yet.
	if got := deleted(a.must(200, "POST", base+"/purge", "", map[string]any{"states": []string{"dropped"}, "olderThanDays": 30})); len(got) != 0 {
		t.Fatalf("olderThanDays purged recent items: %v", got)
	}
	if got := deleted(a.must(200, "POST", base+"/purge", "orchestrator:o1", map[string]any{"states": []string{"dropped"}})); len(got) != 1 || got[0] != open {
		t.Fatalf("purge = %v", got)
	}
	a.must(404, "GET", base+"/"+open, "", nil)
}
