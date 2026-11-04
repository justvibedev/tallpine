// Package releasesigner holds the release signing identity checks shared by
// `tallpine start` (downloaded releases) and the daemon's local release install.
package releasesigner

import "regexp"

// ExpectedTeamID is the Apple Team ID a release bundle must be signed by. It is
// injected at build time from the release signing identity:
//
//	go build -ldflags "-X tallpine.eu/tallpine/backend/internal/releasesigner.ExpectedTeamID=<id>" ./cmd/tallpine
//
// It is intentionally empty in source; with no value, verification refuses.
var ExpectedTeamID = ""

// TeamIDPattern matches a well-formed Apple Team ID.
var TeamIDPattern = regexp.MustCompile(`^[A-Z0-9]{10}$`)

// TeamRequirement is the codesign requirement for a Developer ID app signed
// under Apple's anchor with the given Team ID.
func TeamRequirement(teamID string) string {
	return `anchor apple generic and certificate leaf[subject.OU] = "` + teamID + `"` +
		` and certificate 1[field.1.2.840.113635.100.6.2.6]` +
		` and certificate leaf[field.1.2.840.113635.100.6.1.13]`
}
