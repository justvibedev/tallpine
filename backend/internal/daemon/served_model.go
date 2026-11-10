package daemon

import (
	"context"
	"os"
	"strings"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/claudecode"
	"tallpine.eu/tallpine/backend/internal/domain"
)

type servedModelAccounts interface {
	SessionAccount(ctx context.Context, id domain.SessionID, p domain.AgentAccountProvider) (domain.AgentAccount, bool, error)
}

type servedModelProjects interface {
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
}

// servedModelReader reports the model a live Claude Code TUI session is
// answering with, from the model Claude's own transcript says answered last, so
// `session get` reflects an in-session /model switch. It returns "" (keep the
// persisted launch model) whenever it cannot be sure:
//   - not a Claude Code terminal session: Chat sessions persist the model on
//     every ChatUI selection, which is newer than the transcript;
//   - no assistant reply since the session last launched (FirstSignalAt): after a
//     relaunch, a Chat-to-terminal handoff or a resume fallback Claude runs the
//     persisted model, which older transcript entries do not describe;
//   - no transcript under the config dir the session launched with.
//
// The config dir is the session's account folder, else the project's
// CLAUDE_CONFIG_DIR, else the default. The account is read, never bound.
type servedModelReader struct {
	accounts servedModelAccounts
	projects servedModelProjects
}

func (r servedModelReader) ServedModel(ctx context.Context, sess domain.Session) string {
	if sess.Harness != domain.HarnessClaudeCode || sess.Metadata.AgentSessionID == "" ||
		domain.NormalizeSessionMode(sess.Mode) != domain.SessionModeTUI {
		return ""
	}
	env := map[string]string{}
	if r.projects != nil && sess.ProjectID != "" {
		if project, found, err := r.projects.GetProject(ctx, string(sess.ProjectID)); err == nil && found {
			if dir := strings.TrimSpace(project.Config.Env[claudeConfigDirKey]); dir != "" {
				env[claudeConfigDirKey] = dir
			}
		}
	}
	if r.accounts != nil {
		if account, _, err := r.accounts.SessionAccount(ctx, sess.ID, domain.AgentAccountProviderClaude); err == nil {
			if !account.Device() {
				env[claudeConfigDirKey] = account.Home
			} else if _, inherited := os.LookupEnv(claudeConfigDirKey); inherited {
				// Launch blanks a folder inherited from the daemon for the computer's
				// own login (and wins over project env), so Claude writes to ~/.claude.
				env[claudeConfigDirKey] = ""
			}
		}
	}
	// Only evidence from the current run counts: a relaunch starts Claude on the
	// persisted model, which an earlier /model switch in the transcript no longer
	// describes. FirstSignalAt is cleared at each launch and set by its first hook.
	if sess.FirstSignalAt.IsZero() {
		return ""
	}
	model, err := claudecode.LastAssistantModelSince(ctx, env, sess.Metadata.AgentSessionID, sess.FirstSignalAt)
	if err != nil {
		return ""
	}
	return model
}
