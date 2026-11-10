package cli

import "testing"

func TestPeerCheckTeamIDIsEmptyWhenTheCheckIsOff(t *testing.T) {
	cases := []struct {
		name, goos, team string
		signatureCheck   bool
		want             string
	}{
		{"active", "darwin", "ABCDE12345", true, "ABCDE12345"},
		{"no signature checking", "darwin", "ABCDE12345", false, ""},
		{"unsigned", "darwin", "", true, ""},
		{"not macOS", "linux", "ABCDE12345", true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := peerCheckTeamID(tc.goos, tc.team, tc.signatureCheck); got != tc.want {
				t.Fatalf("peerCheckTeamID = %q, want %q", got, tc.want)
			}
		})
	}
}
