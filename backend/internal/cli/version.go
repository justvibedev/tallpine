package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/buildinfo"
)

// VersionString renders the build metadata stamped into the binary; see
// package buildinfo.
func VersionString() string { return buildinfo.String() }

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), VersionString())
			return err
		},
	}
}
