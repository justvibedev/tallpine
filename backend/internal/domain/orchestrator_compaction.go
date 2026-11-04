package domain

// OrchestratorAutoCompactDefault is the compaction window an orchestrator
// starts with when nothing else chooses one. Orchestrators are long-lived and
// take a turn for every report batch, so without it they grow toward the
// model's full window (about 1M tokens on a 1M model), where each call costs
// several times more and answers get worse. A compaction keeps the session, so
// its chat, workers, PR claims and review delivery are untouched.
const OrchestratorAutoCompactDefault = "120k"

// AutoCompactOff is the project value that turns the orchestrator default off,
// leaving the agent's own compaction default.
const AutoCompactOff = "off"

// DefaultAutoCompactChoice is the compaction choice for a session that made
// none of its own. harnessDefault is the user's Settings default for the
// session's agent ("" when none is saved); projectEnvWindow reports that the
// project's environment sets the agent's compaction window itself.
//
// Workers get harnessDefault, as before. An orchestrator takes, in order: the
// project's orchestrator.autoCompactWindow (AutoCompactOff returns
// harnessDefault, the agent's own behavior), the project's environment window
// (returned as "", so the env stays in effect), then
// OrchestratorAutoCompactDefault. The Settings default is per agent and sized
// for workers: a "40%" there resolved to 400k on a 1M model and kept a
// long-lived orchestrator from ever compacting (B398), so it does not shadow
// the orchestrator's own default.
func DefaultAutoCompactChoice(kind SessionKind, project ProjectConfig, harnessDefault string, projectEnvWindow bool) string {
	if kind != KindOrchestrator {
		return harnessDefault
	}
	switch own := project.Orchestrator.AutoCompactWindow; own {
	case AutoCompactOff:
		return harnessDefault
	case "":
	default:
		return own
	}
	if projectEnvWindow {
		return ""
	}
	return OrchestratorAutoCompactDefault
}
