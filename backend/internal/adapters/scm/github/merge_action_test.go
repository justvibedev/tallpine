package github

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/ports"
)

func validMergeRequest() ports.SCMMergeRequest {
	return ports.SCMMergeRequest{
		PR: ports.SCMPRRef{
			Repo:   ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "octocat", Name: "hello", Repo: "octocat/hello"},
			Number: 42,
			URL:    "https://github.com/octocat/hello/pull/42",
		},
		ExpectedHeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Method:          ports.SCMMergeSquash,
	}
}

func TestMergePullRequest_UsesSquashAndExpectedHead(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodGet, "/repos/octocat/hello/pulls/42", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"title": "t", "body": "no trailers"})
	})
	f.on(http.MethodPut, "/repos/octocat/hello/pulls/42/merge", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			SHA         string `json:"sha"`
			MergeMethod string `json:"merge_method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.SHA != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" || body.MergeMethod != "squash" {
			t.Fatalf("body = %#v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"sha": "merge-sha", "merged": true, "message": "merged"})
	})

	got, err := newProviderForTest(t, f).MergePullRequest(ctx(), validMergeRequest())
	if err != nil {
		t.Fatal(err)
	}
	if got.MergeCommitSHA != "merge-sha" {
		t.Fatalf("result = %#v", got)
	}
}

func TestMergePullRequest_MapsGitHubFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		want   error
	}{
		{name: "not found", status: http.StatusNotFound, want: ports.ErrSCMNotFound},
		{name: "head changed", status: http.StatusConflict, want: ports.ErrSCMHeadChanged},
		{name: "merge blocked", status: http.StatusMethodNotAllowed, want: ports.ErrSCMNotMergeable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeGH(t)
			f.on(http.MethodGet, "/repos/octocat/hello/pulls/42", func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"title": "t", "body": "no trailers"})
			})
			f.on(http.MethodPut, "/repos/octocat/hello/pulls/42/merge", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(map[string]string{"message": tc.name})
			})
			_, err := newProviderForTest(t, f).MergePullRequest(ctx(), validMergeRequest())
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestMergePullRequest_RejectsIncompleteGuard(t *testing.T) {
	request := validMergeRequest()
	request.ExpectedHeadSHA = "abc"
	if _, err := newProviderForTest(t, newFakeGH(t)).MergePullRequest(ctx(), request); err == nil {
		t.Fatal("merge with invalid expected head succeeded")
	}
}

func TestRequestReview_PostsRequestedReviewer(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodPost, "/repos/octocat/hello/pulls/42/requested_reviewers", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Reviewers []string `json:"reviewers"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Reviewers) != 1 || body.Reviewers[0] != "reviewer" {
			t.Fatalf("body = %#v", body)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	})

	err := newProviderForTest(t, f).RequestReview(ctx(), ports.SCMReviewRequest{
		PR:       validMergeRequest().PR,
		Reviewer: "@reviewer",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestResolveReviewThread_UsesGraphQLMutation(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodPost, "/graphql", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(body.Query, "resolveReviewThread") || body.Variables["threadId"] != "thread-1" {
			t.Fatalf("body = %#v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"resolveReviewThread": map[string]any{"thread": map[string]any{"id": "thread-1", "isResolved": true}}}})
	})

	err := newProviderForTest(t, f).ResolveReviewThread(ctx(), ports.SCMReviewResolveRequest{
		PR:       validMergeRequest().PR,
		ThreadID: "thread-1",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestResolveReviewThread_RejectsMissingResolutionConfirmation(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodPost, "/graphql", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"resolveReviewThread": map[string]any{
				"thread": map[string]any{"id": "thread-1"},
			}},
		})
	})

	err := newProviderForTest(t, f).ResolveReviewThread(ctx(), ports.SCMReviewResolveRequest{
		PR:       validMergeRequest().PR,
		ThreadID: "thread-1",
	})
	if err == nil || !strings.Contains(err.Error(), "not confirmed resolved") {
		t.Fatalf("error = %v, want a missing confirmation error", err)
	}
}

func TestResolveReviewThread_RejectsUnconfirmedMutation(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodPost, "/graphql", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"resolveReviewThread": map[string]any{"thread": nil}},
		})
	})

	err := newProviderForTest(t, f).ResolveReviewThread(ctx(), ports.SCMReviewResolveRequest{
		PR:       validMergeRequest().PR,
		ThreadID: "thread-1",
	})
	if err == nil || !strings.Contains(err.Error(), "returned no thread") {
		t.Fatalf("error = %v, want an unconfirmed mutation error", err)
	}
}

func TestResolveReviewThread_RejectsUnresolvedMutation(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodPost, "/graphql", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"resolveReviewThread": map[string]any{
				"thread": map[string]any{"id": "thread-1", "isResolved": false},
			}},
		})
	})

	err := newProviderForTest(t, f).ResolveReviewThread(ctx(), ports.SCMReviewResolveRequest{
		PR:       validMergeRequest().PR,
		ThreadID: "thread-1",
	})
	if err == nil || !strings.Contains(err.Error(), "not confirmed resolved") {
		t.Fatalf("error = %v, want an unresolved mutation error", err)
	}
}

func TestMergePullRequest_KeepsBodyTrailersInSquashMessage(t *testing.T) {
	f := newFakeGH(t)
	trailers := "Ported-from: OrchestratorInc/agent-orchestrator#7306 (640f3e7fba)\nCo-Authored-By: A <a@example.com>"
	f.on(http.MethodGet, "/repos/octocat/hello/pulls/42", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"title": "fix: thing", "user": map[string]any{"login": "pruser"}, "body": "Summary\n\nTests: ok\n\n" + trailers + "\n\n🤖 Generated with [Claude Code](https://claude.com/claude-code)"})
	})
	commit := func(msg, name, email string) map[string]any {
		login := "pruser"
		if name != "Me" {
			login = name
		}
		return map[string]any{"author": map[string]any{"login": login}, "commit": map[string]any{"message": msg, "author": map[string]any{"name": name, "email": email}}}
	}
	f.on(http.MethodGet, "/repos/octocat/hello/pulls/42/commits", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]any{
			commit("feat: one\n\nbody one\n\nCo-Authored-By: A <a@example.com>", "First", "first@example.com"),
			commit("fix: two", "Other", "other@example.com"),
			commit("fix: three", "Me", "me@example.com"),
		})
	})
	f.on(http.MethodPut, "/repos/octocat/hello/pulls/42/merge", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			CommitTitle   string `json:"commit_title"`
			CommitMessage string `json:"commit_message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		wantTail := "Co-authored-by: First <first@example.com>\nCo-authored-by: Other <other@example.com>\n" + trailers
		if body.CommitTitle != "fix: thing (#42)" || !strings.HasSuffix(body.CommitMessage, "\n\n"+wantTail) ||
			!strings.Contains(body.CommitMessage, "* feat: one\n\nbody one") || !strings.Contains(body.CommitMessage, "* fix: two") ||
			strings.Count(strings.ToLower(body.CommitMessage), "co-authored-by: a <a@example.com>") != 1 {
			t.Fatalf("body = %#v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"sha": "merge-sha", "merged": true})
	})
	if _, err := newProviderForTest(t, f).MergePullRequest(ctx(), validMergeRequest()); err != nil {
		t.Fatal(err)
	}
}

func TestTrailerBlock(t *testing.T) {
	for body, want := range map[string]string{
		"": "",
		"Summary\n\nCo-Authored-By: A <a@b>\n\n🤖 Generated with Claude Code": "Co-Authored-By: A <a@b>",
		"Summary only":                       "",
		"Summary\n\nSee: the docs, ok\nmore": "",
		"Summary\n\nCo-Authored-By: A <a@b>": "Co-Authored-By: A <a@b>",
	} {
		if got := trailerBlock(body); got != want {
			t.Errorf("trailerBlock(%q) = %q, want %q", body, got, want)
		}
	}
}
