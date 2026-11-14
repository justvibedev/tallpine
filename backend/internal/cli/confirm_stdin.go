package cli

import (
	"errors"
	"os"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
)

// requireInteractiveStdin fails fast, before a confirmation prompt is printed,
// when the command's stdin is a real file that is not a terminal (a pipe or
// redirect, as over SSH on Windows): the typed answer would never arrive and the
// command would hang. In-memory readers (tests, embedding) are left alone.
func requireInteractiveStdin(cmd *cobra.Command) error {
	if f, ok := cmd.InOrStdin().(*os.File); ok && !term.IsTerminal(f.Fd()) {
		return usageError{errors.New("confirmation needs an interactive terminal; stdin is not a TTY. Pass -y/--yes to skip the prompt")}
	}
	return nil
}
