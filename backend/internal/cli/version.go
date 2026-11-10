package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/buildinfo"
	"tallpine.eu/tallpine/backend/internal/peeradmit"
)

// VersionString renders the build metadata stamped into the binary; see
// package buildinfo.
func VersionString() string { return buildinfo.String() }

func newVersionCommand() *cobra.Command {
	var teamID bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if teamID {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), peerCheckTeamID(runtime.GOOS, buildinfo.TeamID, peeradmit.SignatureCheckBuilt))
				return err
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), VersionString())
			return err
		},
	}
	// Used only by scripts/release-local to check the build carries it.
	cmd.Flags().BoolVar(&teamID, "team-id", false, "Print the release Team ID the peer check runs under; empty when the check is off")
	_ = cmd.Flags().MarkHidden("team-id")
	return cmd
}

// peerCheckTeamID is the Team ID the peer check runs under, or empty when the
// check is off on this build, so the release check cannot pass an inactive one.
func peerCheckTeamID(goos, teamID string, signatureCheck bool) string {
	if mode, _ := peeradmit.DefaultMode(goos, teamID, signatureCheck); mode == peeradmit.ModeOff {
		return ""
	}
	return teamID
}
