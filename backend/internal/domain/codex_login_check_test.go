package domain

import "testing"

// One table for "signed in" across the observations the adapter reads from
// Codex and the snapshots the Codex account service keeps.
func TestCodexLoginCheckIsOneDefinitionOfSignedIn(t *testing.T) {
	email := "a@example.com"
	for _, tc := range []struct {
		name               string
		auth               AgentAuthenticationState
		method             CodexAuthMethod
		status             CodexAccountStatus
		wantSignedIn, want bool // want = logged in (any launchable login)
	}{
		{"chatgpt authorized", AgentAuthenticationAuthorized, CodexAuthMethodChatGPT, CodexAccountStatusValid, true, true},
		{"api key authorized is logged in, not subscription", AgentAuthenticationAuthorized, CodexAuthMethodAPIKey, CodexAccountStatusValid, false, true},
		{"chatgpt unauthorized", AgentAuthenticationUnauthorized, CodexAuthMethodChatGPT, CodexAccountStatusValid, false, false},
		{"unknown auth", AgentAuthenticationUnknown, CodexAuthMethodChatGPT, CodexAccountStatusValid, false, false},
		{"authorized method unknown", AgentAuthenticationAuthorized, CodexAuthMethodUnknown, CodexAccountStatusValid, false, true},
		{"signed-out account ignores a stale authorized observation", AgentAuthenticationAuthorized, CodexAuthMethodChatGPT, CodexAccountStatusSignedOut, false, false},
		{"broken account has no login", AgentAuthenticationAuthorized, CodexAuthMethodChatGPT, CodexAccountStatusBroken, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snap := CodexAccountSnapshot{Status: tc.status, AuthMethod: tc.method, AccountEmail: &email, Authentication: AgentAuthenticationObservation{State: tc.auth}}
			got := CodexSnapshotLoginCheck(snap)
			if got.SignedIn != tc.wantSignedIn || got.LoggedIn != tc.want {
				t.Fatalf("snapshot check = %+v, want signedIn=%t loggedIn=%t", got, tc.wantSignedIn, tc.want)
			}
			if tc.status == CodexAccountStatusValid {
				direct := CodexLoginCheck(tc.auth, tc.method, &email)
				if direct != got {
					t.Fatalf("adapter-side check %+v differs from snapshot check %+v", direct, got)
				}
			}
		})
	}
}
