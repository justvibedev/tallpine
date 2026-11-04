package controllers_test

import (
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

func snapshotWithDetail(summary, input string) chatsvc.Snapshot {
	return chatsvc.Snapshot{Activities: []domain.ConversationActivity{{
		Kind: domain.ActivityKindApproval, Status: domain.ActivityStatusPending, RequestID: "req-1",
		Summary: summary,
		Detail:  []byte(`{` + input + `,"decisions":[{"id":"a","label":"Allow","kind":"allow_once"},{"id":"r","label":"Reject","kind":"reject_once"}]}`),
	}}}
}

func TestOrchestratorCannotAllowHumanOnlyRequests(t *testing.T) {
	cases := map[string]string{
		"install token":     `"command":"cat ~/.tallpine/daemon.token"`,
		"session key read":  `"file_path":"/Users/u/.tallpine/session.key"`,
		"database":          `"command":"sqlite3 /Users/u/.tallpine/data/ao.db .dump"`,
		"github pat":        `"file_path":"/Users/u/.tallpine/data/github-pat.json"`,
		"auth off":          `"command":"launchctl setenv TALLPINE_DAEMON_AUTH off"`,
		"keychain":          `"command":"security find-generic-password -w -s x"`,
		"codex raw command": `"rawCommand":"security dump-keychain"`,
		"ssh key":           `"command":"cat ~/.ssh/id_ed25519"`,
		"claude settings":   `"file_path":"/Users/u/.claude/settings.json"`,
		"codex auth":        `"file_path":"/Users/u/.codex/auth.json"`,
		"zshrc":             `"file_path":"/Users/u/.zshrc"`,
		"launch agent":      `"file_path":"/Users/u/Library/LaunchAgents/x.plist"`,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			svc := &fakeConversationService{snapshot: snapshotWithDetail("run tool", input)}
			agent := orchAgent
			h := approvalsRouter(svc, &agent)
			rec := postResolve(h, `{"decisionId":"a"}`)
			if rec.Code != 403 || !strings.Contains(rec.Body.String(), "APPROVAL_HUMAN_ONLY") {
				t.Fatalf("allow = %d %s", rec.Code, rec.Body)
			}
			if svc.approvalRequestID != "" {
				t.Fatal("a refused allow reached the provider")
			}
			if rec := postResolve(h, `{"decisionId":"r"}`); rec.Code != 204 {
				t.Fatalf("orchestrator reject = %d %s", rec.Code, rec.Body)
			}
			human := &fakeConversationService{snapshot: snapshotWithDetail("run tool", input)}
			if rec := postResolve(approvalsRouter(human, nil), `{"decisionId":"a"}`); rec.Code != 204 {
				t.Fatalf("human allow = %d", rec.Code)
			}
		})
	}
}

func TestOrchestratorMayAllowOrdinaryWorktreeRequests(t *testing.T) {
	for _, input := range []string{
		`"command":"go test ./..."`,
		`"file_path":"/Users/u/.tallpine/data/worktrees/p/p-1/backend/profile.go"`,
		`"command":"npm run build -- --profile"`,
		`"file_path":"/Users/u/.tallpine/data/worktrees/p/p-1/docs/security.md"`,
	} {
		svc := &fakeConversationService{snapshot: snapshotWithDetail("run tool", input)}
		agent := orchAgent
		if rec := postResolve(approvalsRouter(svc, &agent), `{"decisionId":"a"}`); rec.Code != 204 {
			t.Errorf("%s: allow = %d %s", input, rec.Code, rec.Body)
		}
	}
}
