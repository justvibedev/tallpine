package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

func TestConfirmFailsFastWhenStdinIsNotATTY(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close() // never written or closed: a prompt would block forever

	for name, confirm := range map[string]func(*cobra.Command) (bool, error){
		"typed":   func(c *cobra.Command) (bool, error) { return confirmTyped(c, "Type yes to confirm: ") },
		"project": func(c *cobra.Command) (bool, error) { return confirmProjectRemoval(c, "p1") },
		"cleanup": func(c *cobra.Command) (bool, error) {
			return confirmCleanup(c, apitypes.CleanupRunReport{})
		},
		"backlog":    func(c *cobra.Command) (bool, error) { return confirmBacklogDelete(c, "Delete B1?") },
		"automation": func(c *cobra.Command) (bool, error) { return confirmAutomationDelete(c, "a1") },
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			cmd := &cobra.Command{}
			cmd.SetIn(r)
			cmd.SetOut(&out)
			_, err := confirm(cmd)
			if err == nil || !strings.Contains(err.Error(), "-y") {
				t.Fatalf("err = %v, want a message telling the user to pass -y", err)
			}
			if out.Len() != 0 {
				t.Errorf("prompt printed despite non-TTY stdin: %q", out.String())
			}
		})
	}
}
