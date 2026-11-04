package controllers_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/ports"
	sessionsvc "tallpine.eu/tallpine/backend/internal/service/session"
)

func TestSessionsAPI_SendValidation(t *testing.T) {
	srv := newSessionTestServer(t, newFakeSessionService())

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/tp-1/send", `{"message":""}`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "MESSAGE_REQUIRED")
}

func TestSessionsAPI_SendWithAttachment(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	reqBody := `{"message":"Make the button blue.","attachment":{"mimeType":"image/png","data":"` + base64.StdEncoding.EncodeToString([]byte("snapshot")) + `"}}`
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/tp-1/send", reqBody)
	if status != http.StatusOK {
		t.Fatalf("send = %d, want 200; body=%s", status, body)
	}
	if svc.sent != "Make the button blue." {
		t.Fatalf("sent message = %q, want unchanged (attachment referencing happens in the manager)", svc.sent)
	}
	if svc.sentAttachment == nil {
		t.Fatal("sentAttachment is nil, want the decoded attachment")
	}
	if svc.sentAttachment.Ext != ".png" || string(svc.sentAttachment.Data) != "snapshot" {
		t.Fatalf("sentAttachment = %+v, want Ext=.png Data=snapshot", svc.sentAttachment)
	}
	if svc.sentDeliveryOptions.AuthoredByUser {
		t.Fatal("ordinary automation send was marked user-authored")
	}
}

func TestSessionsAPI_SendCarriesUserAuthorshipSeparatelyFromDelivery(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/tp-1/send", `{"message":"Move this control.","userAuthored":true}`)
	if status != http.StatusOK {
		t.Fatalf("send = %d, want 200; body=%s", status, body)
	}
	if !svc.sentDeliveryOptions.AuthoredByUser {
		t.Fatal("send dropped user-authored delivery fact")
	}
}

func TestSessionsAPI_SendReportsDeliveryOutcome(t *testing.T) {
	svc := newFakeSessionService()
	svc.sendReceipt = ports.MessageReceipt{Outcome: ports.MessageQueued, ID: "turn-7"}
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/tp-1/send", `{"message":"later"}`)
	if status != http.StatusOK {
		t.Fatalf("send = %d, want 200; body=%s", status, body)
	}
	var got controllers.SendSessionMessageResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if got.Outcome != "queued" || got.DeliveryID != "turn-7" {
		t.Fatalf("response = %+v, want queued turn-7", got)
	}
}

// A duplicate client message id yields the "duplicate" outcome; the published
// OpenAPI enum must allow it or generated clients reject the daemon's response.
func TestSessionsAPI_SendReportsDuplicateOutcomeAllowedBySchema(t *testing.T) {
	svc := newFakeSessionService()
	svc.sendReceipt = ports.MessageReceipt{Outcome: ports.MessageDuplicate}
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/tp-1/send", `{"message":"again","clientMessageId":"dup-1"}`)
	if status != http.StatusOK {
		t.Fatalf("send = %d, want 200; body=%s", status, body)
	}
	var got controllers.SendSessionMessageResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if got.Outcome != "duplicate" || got.DeliveryID != "" {
		t.Fatalf("response = %+v, want duplicate with no delivery id", got)
	}
	spec, err := os.ReadFile("../apispec/openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi.yaml: %v", err)
	}
	m := regexp.MustCompile(`(?s)\n    SendSessionMessageResponse:\n(.*?)\n    \S`).FindStringSubmatch(string(spec))
	if m == nil {
		t.Fatal("SendSessionMessageResponse missing from openapi.yaml")
	}
	schema := m[1]
	if !strings.Contains(schema, "- duplicate") {
		t.Fatalf("openapi.yaml SendSessionMessageResponse outcome enum lacks duplicate:\n%s", schema)
	}
}

func TestSessionsAPI_SendRejectsUnsupportedAttachmentType(t *testing.T) {
	srv := newSessionTestServer(t, newFakeSessionService())

	reqBody := `{"message":"Make the button blue.","attachment":{"mimeType":"image/svg+xml","data":"` + base64.StdEncoding.EncodeToString([]byte("<svg/>")) + `"}}`
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/tp-1/send", reqBody)
	assertErrorCode(t, body, status, http.StatusBadRequest, "UNSUPPORTED_ATTACHMENT_TYPE")
}

func TestSessionsAPI_SendRejectsOversizedBody(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	// A body past the ~69 MiB send attachment cap is rejected while decoding
	// (MaxBytesReader), before attachment size validation and without
	// materializing the whole body. 80 MiB of base64 comfortably exceeds the cap.
	oversized := `{"message":"Make the button blue.","attachment":{"mimeType":"image/png","data":"` +
		strings.Repeat("A", 80<<20) + `"}}`
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/tp-1/send", oversized)
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_JSON")
	if svc.sent != "" {
		t.Fatalf("send service called with oversized body: %q", svc.sent)
	}
}

func TestSessionsAPI_CleanupWithProjectFilter(t *testing.T) {
	svc := newFakeSessionService()
	svc.cleanupResult = []domain.SessionID{"tp-1"}
	svc.cleanupSkipped = []sessionsvc.CleanupSkipped{{SessionID: "tp-2", Reason: "workspace has uncommitted changes"}}
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/cleanup?project=tp", "")
	if status != http.StatusOK {
		t.Fatalf("cleanup = %d, want 200; body=%s", status, body)
	}
	var got struct {
		OK      bool     `json:"ok"`
		Cleaned []string `json:"cleaned"`
		Skipped []struct {
			SessionID string `json:"sessionId"`
			Reason    string `json:"reason"`
		} `json:"skipped"`
	}
	mustJSON(t, body, &got)
	if !got.OK || len(got.Cleaned) != 1 || got.Cleaned[0] != "tp-1" {
		t.Fatalf("cleanup response = %#v", got)
	}
	if len(got.Skipped) != 1 || got.Skipped[0].SessionID != "tp-2" || got.Skipped[0].Reason != "workspace has uncommitted changes" {
		t.Fatalf("cleanup skipped = %#v, want preserved workspace with reason", got.Skipped)
	}
	if len(svc.cleanupProjects) != 1 || svc.cleanupProjects[0] != "tp" {
		t.Fatalf("cleanupProjects = %#v, want [tallpine]", svc.cleanupProjects)
	}
}

func TestSessionsAPI_CleanupWithoutProjectFilter(t *testing.T) {
	svc := newFakeSessionService()
	svc.cleanupResult = []domain.SessionID{"tp-1", "other-1"}
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/cleanup", "")
	if status != http.StatusOK {
		t.Fatalf("cleanup = %d, want 200; body=%s", status, body)
	}
	var got struct {
		Cleaned []string `json:"cleaned"`
	}
	mustJSON(t, body, &got)
	if len(got.Cleaned) != 2 || got.Cleaned[0] != "tp-1" || got.Cleaned[1] != "other-1" {
		t.Fatalf("cleanup response = %#v", got)
	}
	if len(svc.cleanupProjects) != 1 || svc.cleanupProjects[0] != "" {
		t.Fatalf("cleanupProjects = %#v, want empty project filter", svc.cleanupProjects)
	}
}

func TestSessionsAPI_PRRoutes(t *testing.T) {
	svc := newFakeSessionService()
	svc.linkedPRs = []domain.ChangeRequestReference{{URL: "https://gitlab.com/release/notes/-/merge_requests/9", Provider: "gitlab", Host: "gitlab.com", Repository: "release/notes", Number: 9}}
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions/tp-1/pr", "")
	if status != http.StatusOK {
		t.Fatalf("GET PRs = %d body=%s", status, body)
	}
	var listed struct {
		SessionID string `json:"sessionId"`
		LinkedPRs []struct {
			URL  string `json:"url"`
			Repo string `json:"repo"`
		} `json:"linkedPrs"`
		PRs []struct {
			URL            string `json:"url"`
			Number         int    `json:"number"`
			Title          string `json:"title"`
			State          string `json:"state"`
			StateChangedAt string `json:"stateChangedAt"`
			CreatedAt      string `json:"createdAt"`
			CI             struct {
				State         string `json:"state"`
				FailingChecks []struct {
					Name       string `json:"name"`
					Status     string `json:"status"`
					Conclusion string `json:"conclusion"`
					URL        string `json:"url"`
					LogTail    string `json:"logTail"`
				} `json:"failingChecks"`
			} `json:"ci"`
			Review struct {
				Decision     string `json:"decision"`
				UnresolvedBy []struct {
					ReviewerID string `json:"reviewerId"`
					Count      int    `json:"count"`
					ReviewURL  string `json:"reviewUrl"`
					Links      []struct {
						URL  string `json:"url"`
						File string `json:"file"`
						Line int    `json:"line"`
						Body string `json:"body"`
					} `json:"links"`
				} `json:"unresolvedBy"`
			} `json:"review"`
			Mergeability struct {
				State         string   `json:"state"`
				Reasons       []string `json:"reasons"`
				PRURL         string   `json:"prUrl"`
				ConflictFiles []struct {
					Path string `json:"path"`
				} `json:"conflictFiles"`
			} `json:"mergeability"`
		} `json:"prs"`
	}
	mustJSON(t, body, &listed)
	if listed.SessionID != "tp-1" || len(listed.PRs) != 1 || listed.PRs[0].State != "open" || listed.PRs[0].Title == "" {
		t.Fatalf("GET shape = %#v", listed)
	}
	if len(listed.LinkedPRs) != 1 || listed.LinkedPRs[0].Repo != "release/notes" || listed.LinkedPRs[0].URL != svc.linkedPRs[0].URL {
		t.Fatalf("linked PRs = %#v", listed.LinkedPRs)
	}
	if listed.PRs[0].StateChangedAt != "2026-06-04T11:30:00Z" {
		t.Fatalf("stateChangedAt = %q, want backend-selected PR state time", listed.PRs[0].StateChangedAt)
	}
	if listed.PRs[0].CreatedAt != "2026-06-04T09:00:00Z" {
		t.Fatalf("createdAt = %q, want provider PR creation time", listed.PRs[0].CreatedAt)
	}
	if checks := listed.PRs[0].CI.FailingChecks; len(checks) != 1 || checks[0].Name != "unit" || checks[0].LogTail != "" {
		t.Fatalf("failing checks = %#v", checks)
	}
	if reviewers := listed.PRs[0].Review.UnresolvedBy; len(reviewers) != 1 || reviewers[0].ReviewerID != "reviewer-a" || reviewers[0].ReviewURL == "" || reviewers[0].Links[0].Body != "" {
		t.Fatalf("reviewers = %#v", reviewers)
	}
	if merge := listed.PRs[0].Mergeability; merge.State != "conflicting" || len(merge.ConflictFiles) != 0 || merge.PRURL == "" {
		t.Fatalf("mergeability = %#v", merge)
	}

	body, status, _ = doRequest(t, srv, "POST", "/api/v1/sessions/tp-1/pr/claim", `{"pr":"142"}`)
	if status != http.StatusOK {
		t.Fatalf("claim = %d body=%s", status, body)
	}
	var claimed struct {
		OK            bool     `json:"ok"`
		SessionID     string   `json:"sessionId"`
		PRs           []any    `json:"prs"`
		BranchChanged bool     `json:"branchChanged"`
		TakenOverFrom []string `json:"takenOverFrom"`
	}
	mustJSON(t, body, &claimed)
	if !claimed.OK || claimed.SessionID != "tp-1" || len(claimed.PRs) != 1 || !claimed.BranchChanged || len(claimed.TakenOverFrom) != 0 {
		t.Fatalf("claim shape = %#v", claimed)
	}
}

func TestSessionPRSummaryOmitsUnavailableLifecycleTimes(t *testing.T) {
	payload, err := json.Marshal(controllers.NewSessionPRSummary(sessionsvc.PRSummary{}))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["createdAt"]; ok {
		t.Fatalf("createdAt must be omitted when provider creation time is unavailable: %s", payload)
	}
	if _, ok := got["stateChangedAt"]; ok {
		t.Fatalf("stateChangedAt must be omitted when lifecycle time is unavailable: %s", payload)
	}
}

// A mergeable or not-yet-observed PR has no blocking reasons. The contract
// types reasons as a string array and the desktop reads it with .includes, so
// "no reasons" must serialize as [] and never as null, which crashed the
// session inspector for every open mergeable PR.
func TestSessionPRSummaryReasonsAreNeverNull(t *testing.T) {
	for _, state := range []domain.Mergeability{domain.MergeMergeable, domain.MergeUnknown} {
		payload, err := json.Marshal(controllers.NewSessionPRSummary(sessionsvc.PRSummary{
			State:        domain.PRStateOpen,
			Mergeability: sessionsvc.PRMergeabilitySummary{State: state},
		}))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(payload), `"reasons":[]`) {
			t.Fatalf("%s mergeability must serialize reasons as [], got %s", state, payload)
		}
	}
}

func TestSessionsAPI_ClaimPRErrors(t *testing.T) {
	cases := []struct {
		name string
		body string
		err  error
		code int
		want string
	}{
		{"bad json", `{`, nil, http.StatusBadRequest, "INVALID_JSON"},
		{"missing pr", `{}`, nil, http.StatusBadRequest, "PR_REQUIRED"},
		{"invalid ref", `{"pr":"x"}`, sessionsvc.ErrInvalidPRRef, http.StatusBadRequest, "INVALID_PR_REF"},
		{"session missing", `{"pr":"142"}`, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session"), http.StatusNotFound, "SESSION_NOT_FOUND"},
		{"pr missing", `{"pr":"142"}`, sessionsvc.ErrPRNotFound, http.StatusNotFound, "PR_NOT_FOUND"},
		{"not open", `{"pr":"142"}`, sessionsvc.ErrPRNotOpen, http.StatusConflict, "PR_NOT_OPEN"},
		{"claimed", `{"pr":"142","allowTakeover":false}`, ports.PRClaimedByActiveSessionError{Owner: "tp-2"}, http.StatusConflict, "PR_CLAIMED_BY_ACTIVE_SESSION"},
		{"not claimable", `{"pr":"142"}`, sessionsvc.ErrSessionNotClaimable, http.StatusUnprocessableEntity, "SESSION_NOT_CLAIMABLE"},
		{"mismatch", `{"pr":"142"}`, sessionsvc.ErrProjectMismatch, http.StatusUnprocessableEntity, "PR_PROJECT_MISMATCH"},
		{"scm", `{"pr":"142"}`, sessionsvc.ErrSCMUnavailable, http.StatusServiceUnavailable, "SCM_UNAVAILABLE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newFakeSessionService()
			svc.claimErr = tc.err
			srv := newSessionTestServer(t, svc)
			body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/tp-1/pr/claim", tc.body)
			assertErrorCode(t, body, status, tc.code, tc.want)
			if tc.want == "PR_PROJECT_MISMATCH" {
				for _, hint := range []string{"registered workspace child origin", "tallpine project get", "full PR/MR URL", "valid root origin", "For single-repo forks", "canonicalRepoURL", "--canonical-repo-url", "merges into the existing config", "Git remotes"} {
					if !strings.Contains(string(body), hint) {
						t.Fatalf("mismatch missing %q guidance: %s", hint, body)
					}
				}
			}
			if tc.want == "INVALID_PR_REF" && !strings.Contains(string(body), "For a workspace child repository, pass its full PR/MR URL") {
				t.Fatalf("invalid ref missing workspace guidance: %s", body)
			}
		})
	}
}

func TestSessionsAPI_SendCarriesCooperativeSenderWithoutHumanAuthorship(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/tp-1/send", `{"message":"Do the next step","senderSessionId":"tp-orchestrator"}`)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if svc.sentDeliveryOptions.SenderSessionID != "tp-orchestrator" || svc.sentDeliveryOptions.AuthoredByUser {
		t.Fatalf("options=%+v", svc.sentDeliveryOptions)
	}
}

func TestSessionsAPI_SendReportsDeliveredSessionMessage(t *testing.T) {
	// Every delivered message reaches the turn notifier, owner-typed ones with
	// an empty sender: they start a turn that is no echo of a review approval.
	svc := newFakeSessionService()
	var got [][2]domain.SessionID
	deps := httpd.APIDeps{Sessions: svc, MessageSent: func(_ context.Context, sender, target domain.SessionID) {
		got = append(got, [2]domain.SessionID{sender, target})
	}}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, deps, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	if body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/tp-1/send", `{"message":"head abc123","senderSessionId":"tp-7"}`); status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/tp-1/send", `{"message":"from the user","userAuthored":true}`); status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if len(got) != 2 || got[0] != [2]domain.SessionID{"tp-7", "tp-1"} || got[1] != [2]domain.SessionID{"", "tp-1"} {
		t.Fatalf("observed = %v, want tp-7 -> tp-1 then owner -> tp-1", got)
	}
}

func TestSessionsAPI_InteractionRecencyKeepsHumanTimestampSeparate(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)
	human := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	direction := human.Add(time.Hour)
	rec := svc.sessions["tp-1"]
	rec.Metadata.LatestUserPromptAt = human
	rec.Metadata.LatestInteractionAt = direction
	svc.sessions["tp-1"] = rec
	body, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions/tp-1", "")
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	var response struct {
		Session struct {
			LastUserMessageAt time.Time `json:"lastUserMessageAt"`
			LastInteractionAt time.Time `json:"lastInteractionAt"`
		} `json:"session"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if !response.Session.LastUserMessageAt.Equal(human) || !response.Session.LastInteractionAt.Equal(direction) {
		t.Fatalf("timestamps=%+v", response)
	}
	// A later human prompt participates without rewriting the orchestration fact.
	rec.Metadata.LatestUserPromptAt = direction.Add(time.Hour)
	svc.sessions["tp-1"] = rec
	body, _, _ = doRequest(t, srv, "GET", "/api/v1/sessions/tp-1", "")
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if !response.Session.LastInteractionAt.Equal(rec.Metadata.LatestUserPromptAt) {
		t.Fatalf("new human recency=%v", response.Session.LastInteractionAt)
	}
}

func TestSessionsAPI_KillKeepsArtifactsByDefault(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)
	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/tp-1/kill", "")
	if status != http.StatusOK || strings.Contains(string(body), "artifactsDeleted") {
		t.Fatalf("kill response = %d %s", status, body)
	}
	if len(svc.artifactsDeleted) != 0 {
		t.Fatalf("artifacts deleted without the flag: %v", svc.artifactsDeleted)
	}
}

func TestSessionsAPI_KillDeletesArtifactsWhenAsked(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)
	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/tp-1/kill", `{"deleteArtifacts":true}`)
	var result struct {
		ArtifactsDeleted bool `json:"artifactsDeleted"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK || !result.ArtifactsDeleted || !svc.sessions["tp-1"].IsTerminated {
		t.Fatalf("kill response = %d %s", status, body)
	}
	if len(svc.artifactsDeleted) != 1 || svc.artifactsDeleted[0] != "tp-1" {
		t.Fatalf("artifactsDeleted = %v, want [tp-1]", svc.artifactsDeleted)
	}
}

func TestSessionsAPI_KillSucceedsWhenArtifactDeleteFails(t *testing.T) {
	svc := newFakeSessionService()
	svc.deleteArtifactsErr = errors.New("disk busy")
	srv := newSessionTestServer(t, svc)
	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/tp-1/kill", `{"deleteArtifacts":true}`)
	if status != http.StatusOK || strings.Contains(string(body), "artifactsDeleted") || !svc.sessions["tp-1"].IsTerminated {
		t.Fatalf("kill response = %d %s", status, body)
	}
}

func TestSessionsAPI_KillRejectsMalformedBody(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)
	_, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/tp-1/kill", `{"deleteArtifacts":`)
	if status != http.StatusBadRequest || svc.sessions["tp-1"].IsTerminated {
		t.Fatalf("status = %d terminated=%v, want 400 and a live session", status, svc.sessions["tp-1"].IsTerminated)
	}
}

func TestSessionsAPI_KillReportsAcceptedCleanup(t *testing.T) {
	svc := newFakeSessionService()
	svc.cleanupPending = true
	srv := newSessionTestServer(t, svc)
	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/tp-1/kill", "")
	var result struct {
		CleanupPending bool `json:"cleanupPending"`
		Freed          bool `json:"freed"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK || !result.CleanupPending || result.Freed || !svc.sessions["tp-1"].IsTerminated {
		t.Fatalf("accepted cleanup response = %d %s", status, body)
	}
}
