// Package deepseek adapts the DeepSeek Harness worker agent for code-review sessions.
package deepseek

import (
	"context"

	workeragent "tallpine.eu/tallpine/backend/internal/adapters/agent/deepseekharness"
	"tallpine.eu/tallpine/backend/internal/adapters/reviewer/agentrestore"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// Reviewer is the DeepSeek Harness code-review adapter.
type Reviewer struct {
	agent ports.Agent
}

// New builds the DeepSeek reviewer adapter.
func New() *Reviewer {
	return &Reviewer{agent: workeragent.New()}
}

// Harness identifies this reviewer in the reviewer registry.
func (r *Reviewer) Harness() domain.ReviewerHarness { return domain.ReviewerDeepSeek }

var _ ports.Reviewer = (*Reviewer)(nil)
var _ ports.ReviewerCanceller = (*Reviewer)(nil)
var _ ports.ReviewerRestorer = (*Reviewer)(nil)
var _ ports.ReviewerReusePolicy = (*Reviewer)(nil)

// ReviewProcessReusable is false: the headless profile answers one task and
// exits, so every pass relaunches the reviewer.
func (r *Reviewer) ReviewProcessReusable() bool { return false }

// InvalidateBinaryResolution forgets the worker plugin's cached executable.
func (r *Reviewer) InvalidateBinaryResolution() {
	if invalidator, ok := r.agent.(ports.AgentBinaryResolutionInvalidator); ok {
		invalidator.InvalidateBinaryResolution()
	}
}

// There is deliberately no ports.ReviewerChatProfile: the DeepSeek ACP chat
// driver has no read-only launch, so reviews run in the terminal only.

// ReviewCommand launches the worker's headless profile with the file sandbox
// pinned to read-only (workeragent.ReadOnlyArgs) and the permission-mode env
// set the same way, so the reviewer cannot write to the checkout.
func (r *Reviewer) ReviewCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, error) {
	argv, err := r.agent.GetLaunchCommand(ctx, ports.LaunchConfig{
		Policy:           inv.Policy,
		Config:           inv.Config,
		SessionID:        inv.ReviewerID,
		WorkspacePath:    inv.WorkspacePath,
		DataDir:          inv.DataDir,
		Prompt:           inv.Prompt,
		SystemPrompt:     inv.SystemPrompt,
		SystemPromptFile: inv.SystemPromptFile,
		Permissions:      ports.PermissionModeAuto,
	})
	if err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	extra, err := workeragent.ReadOnlyArgs(inv.DataDir)
	if err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	return ports.ReviewCommandSpec{Argv: insertBeforeLastArg(argv, extra...), Env: readOnlyEnv()}, nil
}

// ReviewRestoreCommand resumes a captured DeepSeek session read-only; ok is
// false when no native session id is known.
func (r *Reviewer) ReviewRestoreCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, bool, error) {
	cmd, ok, err := agentrestore.Command(ctx, r.agent, inv, agentrestore.Options{Permissions: ports.PermissionModeAuto})
	if err != nil || !ok {
		return cmd, ok, err
	}
	extra, err := workeragent.ReadOnlyArgs(inv.DataDir)
	if err != nil {
		return ports.ReviewCommandSpec{}, false, err
	}
	cmd.Argv = insertBeforeLastArg(cmd.Argv, extra...)
	cmd.Env = readOnlyEnv()
	return cmd, true, nil
}

// ReviewMessage returns the centrally-authored task for an existing pane.
func (r *Reviewer) ReviewMessage(_ context.Context, inv ports.ReviewInvocation) (string, error) {
	return inv.Prompt, nil
}

// ReviewCancel interrupts the one-shot headless run with the terminal interrupt.
func (r *Reviewer) ReviewCancel(context.Context) (ports.ReviewCancelSpec, error) {
	return ports.ReviewCancelSpec{Mode: ports.ReviewCancelInterrupt, Interrupts: 2}, nil
}

func readOnlyEnv() map[string]string {
	return map[string]string{workeragent.PermissionModeEnv: "read-only"}
}

func insertBeforeLastArg(argv []string, extra ...string) []string {
	if len(argv) == 0 {
		return append([]string{}, extra...)
	}
	out := make([]string, 0, len(argv)+len(extra))
	out = append(out, argv[:len(argv)-1]...)
	out = append(out, extra...)
	return append(out, argv[len(argv)-1])
}
