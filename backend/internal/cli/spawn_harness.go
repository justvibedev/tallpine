package cli

import (
	"fmt"
	"strings"
)

func resolveSpawnHarness(explicit, kind string, project projectDetails) (string, error) {
	if harness := strings.TrimSpace(explicit); harness != "" {
		return harness, nil
	}
	if project.Config != nil {
		if kind == "orchestrator" {
			if harness := strings.TrimSpace(project.Config.Orchestrator.Agent); harness != "" {
				return harness, nil
			}
		} else {
			if harness := strings.TrimSpace(project.Config.Worker.Agent); harness != "" {
				return harness, nil
			}
		}
	}
	// No per-project pin: inherit the daemon's default agent (TALLPINE_AGENT).
	if harness := strings.TrimSpace(project.Agent); harness != "" {
		return harness, nil
	}
	if kind == "orchestrator" {
		return "", usageError{fmt.Errorf("agent could not be resolved; pass --agent or configure `tallpine project set-config %s --orchestrator-agent <agent>`", project.ID)}
	}
	return "", usageError{fmt.Errorf("agent could not be resolved; pass --agent or configure `tallpine project set-config %s --worker-agent <agent>`", project.ID)}
}
