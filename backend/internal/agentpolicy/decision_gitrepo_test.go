package agentpolicy_test

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules"
	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestDecisionGitRepoResolvesForTheBoundSession(t *testing.T) {
	engine, err := agentpolicy.New(rules.All(), nil,
		agentpolicy.WithManagedFile("", nil),
		agentpolicy.WithEnv(func(string) (string, bool) { return "", false }),
		agentpolicy.WithGitRepo(func(_ context.Context, s agentpolicy.SessionRef) string {
			return "/repo/" + string(s.Project) + "/" + string(s.ID)
		}))
	if err != nil {
		t.Fatal(err)
	}
	d := engine.Decide(context.Background(), agentpolicy.SessionRef{Project: "p"})
	if got := d.GitRepo(); got != "/repo/p/" {
		t.Errorf("GitRepo = %q", got)
	}
	if got := d.WithSession(domain.SessionID("s-1")).GitRepo(); got != "/repo/p/s-1" {
		t.Errorf("GitRepo after WithSession = %q", got)
	}
	if got := (agentpolicy.Decision{}).GitRepo(); got != "" {
		t.Errorf("zero decision GitRepo = %q", got)
	}
}
