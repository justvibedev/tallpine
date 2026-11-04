// Package kiro adapts Kiro's interactive terminal UI for Tallpine code reviews.
package kiro

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"

	workeragent "tallpine.eu/tallpine/backend/internal/adapters/agent/kiro"
	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/hookutil"
	"tallpine.eu/tallpine/backend/internal/ports"
)

const reviewerAgentName = "vo-reviewer"

// Reviewer is Kiro's Tallpine code-review adapter.
type Reviewer struct {
	resolveBinary func(context.Context) (string, error)
}

// New builds the Kiro reviewer adapter.
func New() *Reviewer {
	return &Reviewer{resolveBinary: workeragent.ResolveKiroBinary}
}

// Harness identifies Kiro in the reviewer registry.
func (r *Reviewer) Harness() domain.ReviewerHarness { return domain.ReviewerKiro }

var _ ports.Reviewer = (*Reviewer)(nil)
var _ ports.ReviewerCanceller = (*Reviewer)(nil)
var _ ports.ReviewerPromptReadinessProvider = (*Reviewer)(nil)

// ReviewPromptReadinessHints reuses Kiro's worker-TUI prompt markers.
func (*Reviewer) ReviewPromptReadinessHints(ctx context.Context) (ports.PromptReadinessHints, error) {
	return workeragent.New().PromptReadinessHints(ctx, ports.LaunchConfig{})
}

// ReviewCommand launches Kiro as a persistent interactive TUI. It deliberately
// avoids positional input and --no-interactive; Tallpine injects InitialMessage only
// after the pane exists. The process cwd is a Tallpine-owned empty directory, so
// project .kiro agents, MCP config, hooks, steering, skills, and AGENTS.md are
// not inherited. The generated custom agent admits read tools plus a
// deny-by-default shell policy for review inspection/reporting only.
func (r *Reviewer) ReviewCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, error) {
	binary, err := r.resolveBinary(ctx)
	if err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	if inv.TaskPromptRoot == "" {
		argv, err := workeragent.Fence([]string{binary, "chat", "--agent", reviewerAgentName}, fenceEnv(inv), inv.Policy, "")
		return ports.ReviewCommandSpec{Argv: argv}, err
	}
	reviewerDir := filepath.Join(inv.TaskPromptRoot, "kiro")
	if err := writeReviewerAgent(reviewerDir, inv); err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	argv, err := workeragent.Fence([]string{binary, "chat", "--agent", reviewerAgentName}, fenceEnv(inv), inv.Policy, reviewerDir)
	if err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	return ports.ReviewCommandSpec{
		Argv:             argv,
		InitialMessage:   inv.Prompt,
		WorkingDirectory: reviewerDir,
	}, nil
}

// ReviewRestoreCommand restores a recorded Kiro reviewer pane by relaunching
// with the reviewer agent/profile and current task context.
func (r *Reviewer) ReviewRestoreCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, bool, error) {
	cmd, err := r.ReviewCommand(ctx, inv)
	return cmd, true, err
}

// ReviewMessage keeps later passes in the same long-lived Kiro TUI.
func (r *Reviewer) ReviewMessage(_ context.Context, inv ports.ReviewInvocation) (string, error) {
	return inv.Prompt, nil
}

// ReviewCancel uses Kiro TUI's cancel-stream key. Ctrl-C is the quit binding,
// so the shared interrupt cancellation mode is intentionally not used here.
func (r *Reviewer) ReviewCancel(context.Context) (ports.ReviewCancelSpec, error) {
	return ports.ReviewCancelSpec{Mode: ports.ReviewCancelInput, Input: "\x1b"}, nil
}

func writeReviewerAgent(reviewerDir string, inv ports.ReviewInvocation) error {
	if inv.SystemPromptFile == "" || inv.WorkspacePath == "" {
		return fmt.Errorf("kiro reviewer requires system prompt and workspace paths")
	}
	system, err := os.ReadFile(inv.SystemPromptFile) //nolint:gosec // Tallpine-owned prompt path
	if err != nil {
		return fmt.Errorf("read Kiro reviewer system prompt: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(reviewerDir, ".kiro", "agents"), 0o700); err != nil {
		return fmt.Errorf("create Kiro reviewer directory: %w", err)
	}
	kiroSystemPath := filepath.Join(reviewerDir, "system.md")
	extra := `

Kiro security and reporting rules:
- Your process runs outside the checkout. Use the read, glob, and grep tools on the absolute worker checkout path from the task.
- Shell is deny-by-default. Only the narrow git inspection and review-reporting command shapes configured by Tallpine can run.
- The supported git shell shapes are exactly: status with optional --short; diff with --no-ext-diff --no-textconv; log; and show. Additional arguments must be single whitespace-free refs, options, or paths. Filenames with spaces, extra shell quoting, compound commands, redirections, and complex ref expressions are intentionally unsupported; use read, glob, or grep instead.
- For each JSON reporting body, base64-encode the UTF-8 JSON yourself and use the task's command with the JSON replaced by the base64 text: printf '%s' '<base64>' | base64 --decode | gh api ... or ... | tallpine review submit .... Use base64 -D instead of --decode on systems that require it.
- Never request or attempt any write, unrestricted shell, commit, push, extension, MCP server, skill, steering, or project agent resource.
`
	if err := hookutil.AtomicWriteFile(kiroSystemPath, append(system, extra...), 0o600); err != nil {
		return fmt.Errorf("write Kiro reviewer system prompt: %w", err)
	}

	config := map[string]any{
		"name":           reviewerAgentName,
		"description":    "Tallpine read-only code reviewer",
		"prompt":         "file://" + filepath.ToSlash(kiroSystemPath),
		"mcpServers":     map[string]any{},
		"includeMcpJson": false,
		"resources":      []any{},
		"tools":          []string{"read", "glob", "grep", "shell"},
		"allowedTools":   []string{"read", "glob", "grep"},
		"toolsSettings": map[string]any{
			"shell": map[string]any{
				"allowedCommands":   shellAllowedCommands(inv),
				"deniedCommands":    []string{".*(?:git[[:space:]]+(?:commit|push)|--output(?:=|[[:space:]])|>|>>|tee[[:space:]]).*"},
				"autoAllowReadonly": false,
				"denyByDefault":     true,
			},
		},
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Kiro reviewer agent: %w", err)
	}
	agentPath := filepath.Join(reviewerDir, ".kiro", "agents", reviewerAgentName+".json")
	if err := hookutil.AtomicWriteFile(agentPath, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write Kiro reviewer agent: %w", err)
	}
	return nil
}

func shellAllowedCommands(inv ports.ReviewInvocation) []string {
	workspace := regexp.QuoteMeta(filepath.ToSlash(inv.WorkspacePath))
	session := regexp.QuoteMeta(string(inv.WorkerSessionID))
	gitPrefix := `git --no-pager -C ['"]?` + workspace + `['"]? `
	safeArg := `[A-Za-z0-9_./:=,@{}^~+\-]+`
	base64Pipe := `printf '%s' '[A-Za-z0-9+/=]+' \| base64 (?:--decode|-D) \| `
	// Kiro accepts regex policies rather than structured argv. Anchor every
	// pattern to the complete command and intentionally support only the exact
	// whitespace-free argument shapes described in the reviewer prompt above.
	return []string{
		`^` + gitPrefix + `status(?: --short)?$`,
		`^` + gitPrefix + `diff --no-ext-diff --no-textconv(?: ` + safeArg + `)*(?: --(?: ` + safeArg + `)*)?$`,
		`^` + gitPrefix + `log(?: ` + safeArg + `)*$`,
		`^` + gitPrefix + `show(?: ` + safeArg + `)*(?: --(?: ` + safeArg + `)*)?$`,
		`^` + base64Pipe + `gh api --method POST repos/[A-Za-z0-9_.\-]+/[A-Za-z0-9_.\-]+/pulls/[0-9]+/reviews --input - --jq '[.]id'$`,
		`^` + base64Pipe + `tallpine review submit --session ` + session + ` --reviews -$`,
	}
}

// FencesState implements ports.StateFencer: every review runs
// inside the same seatbelt fence as a Kiro worker.
func (r *Reviewer) FencesState(goos string) bool { return workeragent.FencesState(goos) }

// fenceEnv is the env the fence locates Tallpine state from: the project env
// plus the daemon's run file and data dir, which the reviewer env may lack,
// and the reviewer's own temp dir the launcher sets as TMPDIR.
func fenceEnv(inv ports.ReviewInvocation) map[string]string {
	env := maps.Clone(inv.Env)
	if env == nil {
		env = map[string]string{}
	}
	if inv.RunFilePath != "" {
		env[brand.Env("RUN_FILE")] = inv.RunFilePath
	}
	if _, ok := env[brand.Env("DATA_DIR")]; !ok && inv.DataDir != "" {
		env[brand.Env("DATA_DIR")] = inv.DataDir
	}
	if inv.TempDir != "" {
		env["TMPDIR"] = inv.TempDir
	}
	return env
}
