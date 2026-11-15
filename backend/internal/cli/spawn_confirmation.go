package cli

import (
	"fmt"
	"io"
)

// writeSpawnConfirmation prints what the daemon answered a spawn with: its
// warnings, the "spawned session" line (the display name, falling back to
// name, the status, the claimed PR and prompt sizes) and the compaction line.
func writeSpawnConfirmation(out io.Writer, res spawnResult, name, claimed string) error {
	claimLabel := ""
	if claimed != "" {
		claimLabel = fmt.Sprintf(" (claimed %s)", claimed)
	}
	promptSize := ""
	if res.PromptBytes > 0 || res.SystemPromptBytes > 0 {
		promptSize = fmt.Sprintf(" [prompt %d B, system %d B]", res.PromptBytes, res.SystemPromptBytes)
	}
	displayName := res.Session.DisplayName
	if displayName == "" {
		displayName = name
	}
	status := res.Session.Status
	if res.Session.ProvisionState == "provisioning" {
		// The agent starts after this answer; a start that fails later
		// is filed as an [auto] start_failed report and reads "exited".
		status = "starting"
	} else if status == "idle" && res.PromptBytes > 0 {
		// The task prompt was handed to the agent, which picks it up
		// moments later; "idle" would read as a worker with no work.
		status = "starting"
	}
	for _, warning := range res.Warnings {
		if _, err := fmt.Fprintf(out, "warning: %s\n", warning); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(out, "spawned session %s %q (%s)%s%s\n", res.Session.ID, displayName, status, claimLabel, promptSize); err != nil {
		return err
	}
	return writeCompactionLine(out, res.Compaction)
}
