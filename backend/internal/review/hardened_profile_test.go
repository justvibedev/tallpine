package review

import (
	"context"
	"testing"

	reviewerclaude "tallpine.eu/tallpine/backend/internal/adapters/reviewer/claudecode"
	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
)

// fencingReviewer is a fake reviewer that, like the Claude Code reviewer, can
// carry the state fence on macOS.
type fencingReviewer struct{ *fakeReviewer }

func (fencingReviewer) FencesState(goos string) bool { return reviewerclaude.New().FencesState(goos) }

// Under the hardened profile a reviewer terminal launches (fresh or restored)
// only when its adapter fences it on this OS, and it is capped at auto.
func TestReviewerTerminalUnderProfile(t *testing.T) {
	engine, _ := policytest.Managed(t, `{"schemaVersion":2,"profile":"hardened"}`, nil)
	prev := reviewerGOOS
	t.Cleanup(func() { reviewerGOOS = prev })
	for _, tc := range []struct {
		name    string
		fences  bool
		goos    string
		refused bool
	}{
		{"unfenceable reviewer darwin", false, "darwin", true},
		{"claude reviewer linux", true, "linux", true},
		{"claude reviewer windows", true, "windows", true},
		{"claude reviewer darwin", true, "darwin", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reviewerGOOS = tc.goos
			fake := &fakeReviewer{}
			var resolver fakeReviewerResolver
			if tc.fences {
				resolver = fakeReviewerResolver{reviewer: fencingReviewer{fake}, ok: true}
			} else {
				resolver = fakeReviewerResolver{reviewer: fake, ok: true}
			}
			l := NewLauncher(resolver, &fakeRuntime{}, t.TempDir(), WithPolicy(engine))
			_, spawnErr := l.Spawn(context.Background(), launchSpec())
			_, restoreErr := l.RestoreTerminal(context.Background(), launchSpec())
			for name, err := range map[string]error{"spawn": spawnErr, "restore": restoreErr} {
				if tc.refused != agentpolicy.IsFenceRequired(err) {
					t.Fatalf("%s err = %v, refused want %v", name, err, tc.refused)
				}
			}
			if !tc.refused && !permissioncap.Of(fake.gotInv.Policy).ForbidsBypass() {
				t.Fatal("reviewer under the profile is not capped")
			}
		})
	}
}
