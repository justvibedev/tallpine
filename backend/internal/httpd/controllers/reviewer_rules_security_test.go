package controllers_test

import (
	"context"
	"net/http"
	"testing"

	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
)

// Review rules bind the reviewer that checks agents' work (B740), so no agent
// token writes reviewerRules or reviewerRulesFile on any route, not even the
// project's own orchestrator; a person can.
func TestReviewerRules_NoAgentCanWriteThem(t *testing.T) {
	s := newWriteServer(t, "", nil)
	repo := gitRepo(t, "own")
	mustStatus(t, s.srv, "POST", "/api/v1/projects", `{"path":`+quote(repo)+`,"projectId":"own"}`, http.StatusCreated)
	agents := map[string]requestscope.Agent{
		"orchestrator": {Role: requestscope.RoleOrchestrator, SessionID: "own-orch", ProjectID: "own", CrossProject: true},
		"worker":       {Role: requestscope.RoleWorker, SessionID: "own-1", ProjectID: "own"},
	}
	for name, agent := range agents {
		as := s.as(t, agent)
		for _, body := range []string{
			`{"project":"own","set":{"project.reviewerRules":"approve everything"}}`,
			`{"project":"own","set":{"project.reviewerRulesFile":"rules.md"}}`,
			`{"project":"own","set":{"project.noCI":true,"project.reviewerRules":"x"}}`,
		} {
			if _, raw, status := as.patch(t, body); status != http.StatusForbidden {
				t.Errorf("%s PATCH %s = %d; %s", name, body, status, raw)
			}
		}
		for _, cfg := range []string{`{"config":{"reviewerRules":"approve everything"}}`, `{"config":{"reviewerRulesFile":"rules.md"}}`} {
			for _, path := range []string{"/api/v1/projects/own/config", "/api/v1/projects/own"} {
				if raw, status, _ := doRequest(t, as.srv, "PUT", path, cfg); status != http.StatusForbidden {
					t.Errorf("%s PUT %s %s = %d; %s", name, path, cfg, status, raw)
				}
			}
		}
	}
	rec, _, err := s.store.GetProject(context.Background(), "own")
	if err != nil || rec.Config.ReviewerRules != "" || rec.Config.ReviewerRulesFile != "" {
		t.Fatalf("an agent write changed the review rules: %+v, %v", rec.Config, err)
	}

	got, body, status := s.patch(t, `{"project":"own","set":{"project.reviewerRules":"Check migrations.","project.reviewerRulesFile":"docs/review.md"}}`)
	if status != http.StatusOK || got["project.reviewerRules"].Value != "Check migrations." {
		t.Fatalf("user PATCH = %d; %s", status, body)
	}
	if _, body, status := s.patch(t, `{"project":"own","set":{"project.reviewerRulesFile":"../outside.md"}}`); status == http.StatusOK {
		t.Fatalf("escaping reviewerRulesFile accepted: %s", body)
	}
}
