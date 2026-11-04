package apitypes

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

// TestSessionWireKeys pins the JSON keys of every session DTO the CLI and the
// controllers share. Both sides use these types, so a renamed tag would move
// the daemon and the CLI together and no round trip could notice; this test is
// what makes such a rename a deliberate, reviewed wire change.
func TestSessionWireKeys(t *testing.T) {
	yes := true
	cases := []struct {
		name string
		v    any
		want string
	}{
		{"RenameSessionRequest", RenameSessionRequest{}, "displayName"},
		{"RenameSessionResponse", RenameSessionResponse{}, "displayName,ok,sessionId"},
		{"RestoreSessionResponse", RestoreSessionResponse{}, "ok,restoreMode,session,sessionId"},
		{"ExitAgentResponse", ExitAgentResponse{}, "ok,session,sessionId"},
		{"ResumeAgentResponse", ResumeAgentResponse{}, "ok,resumeMode,session,sessionId"},
		{"KillSessionResponse", KillSessionResponse{Freed: true, CleanupPending: true, ArtifactsDeleted: true}, "artifactsDeleted,cleanupPending,freed,ok,sessionId"},
		{"CleanupSkippedSession", CleanupSkippedSession{}, "reason,sessionId"},
		{"CleanupSessionsResponse", CleanupSessionsResponse{}, "alreadyGone,cleaned,ok,skipped"},
		{"ClaimPRRequest", ClaimPRRequest{AllowTakeover: &yes}, "allowTakeover,pr"},
		{"ClaimPRResponse", ClaimPRResponse{}, "branchChanged,ok,prs,sessionId,takenOverFrom"},
		{"SessionPRFacts", SessionPRFacts{}, "ci,mergeability,number,review,reviewComments,state,updatedAt,url"},
		{"SessionResponse", SessionResponse{}, "session"},
		{"SessionLastAssistantMessage", SessionLastAssistantMessage{}, "at,text"},
		{"ListSessionsResponse", ListSessionsResponse{}, "sessions"},
		{"SessionArchiveSummary", SessionArchiveSummary{}, "byProject,total"},
		{"ListSessionPRsResponse", ListSessionPRsResponse{}, "linkedPrs,prs,sessionId"},
		{"SessionPRCISummary", SessionPRCISummary{}, "autoInjectCI,failingChecks,state"},
		{"SessionPRReviewSummary", SessionPRReviewSummary{}, "decision,hasUnresolvedHumanComments,unresolvedBy"},
		{"SessionPRMergeabilitySummary", SessionPRMergeabilitySummary{}, "prUrl,reasons,state"},
		{"SessionPRReference", SessionPRReference{}, "host,number,provider,repo,url"},
		{"BackgroundTaskPayload", BackgroundTaskPayload{}, "id,name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.v)
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]json.RawMessage
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			if got := strings.Join(keys, ","); got != tc.want {
				t.Errorf("wire keys = %s, want %s", got, tc.want)
			}
		})
	}
}
