package review

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/protectstate"
	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
	"tallpine.eu/tallpine/backend/internal/sessionscratch"
)

// reviewerGOOS is the host OS reviewer launches are admitted for; tests swap it.
var reviewerGOOS = runtime.GOOS

func (l *agentLauncher) Spawn(ctx context.Context, spec LaunchSpec) (LaunchResult, error) {
	spec, err := l.withAccountEnv(ctx, spec)
	if err != nil {
		return LaunchResult{}, err
	}
	inv, err := l.prepareInvocation(ctx, spec)
	if err != nil {
		return LaunchResult{}, err
	}
	if reviewer, ok := l.reviewers.Reviewer(spec.Harness); ok {
		if profile, ok := reviewer.(ports.ReviewerChatProfile); ok && spec.InterfaceMode != domain.ReviewerInterfaceTUI && l.reviewChatSupported(profile) {
			res, err := l.startReviewerChat(ctx, spec, inv, profile, false)
			return l.recordLaunch(ctx, spec, res, err)
		}
	}
	// A retained native id means this stable reviewer has provider-owned
	// history even though its terminal process is gone. Recreate the pane by
	// resuming that conversation; a first launch has no id and still pins the
	// adapter's deterministic fresh identity through ReviewCommand.
	res, err := l.launchReviewerTerminalWithMode(ctx, spec, inv, strings.TrimSpace(spec.AgentSessionID) != "")
	return l.recordLaunch(ctx, spec, res, err)
}

func (l *agentLauncher) RestoreTerminal(ctx context.Context, spec LaunchSpec) (LaunchResult, error) {
	spec, err := l.withAccountEnv(ctx, spec)
	if err != nil {
		return LaunchResult{}, err
	}
	inv, err := l.prepareIdleInvocation(ctx, spec)
	if err != nil {
		return LaunchResult{}, err
	}
	if reviewer, ok := l.reviewers.Reviewer(spec.Harness); ok {
		if profile, ok := reviewer.(ports.ReviewerChatProfile); ok && spec.InterfaceMode != domain.ReviewerInterfaceTUI && l.reviewChatSupported(profile) {
			res, err := l.startReviewerChat(ctx, spec, inv, profile, true)
			return l.recordLaunch(ctx, spec, res, err)
		}
	}
	res, err := l.launchReviewerTerminalWithMode(ctx, spec, inv, true)
	return l.recordLaunch(ctx, spec, res, err)
}

func (l *agentLauncher) startReviewerChat(ctx context.Context, spec LaunchSpec, inv ports.ReviewInvocation, profile ports.ReviewerChatProfile, restore bool) (LaunchResult, error) {
	systemPrompt, err := os.ReadFile(inv.SystemPromptFile)
	if err != nil {
		return LaunchResult{}, fmt.Errorf("read reviewer system prompt: %w", err)
	}
	providerID := strings.TrimSpace(spec.ProviderConversationID)
	if providerID == "" {
		providerID = strings.TrimSpace(spec.AgentSessionID)
	}
	prompt := inv.Prompt
	if spec.DeferInitialMessage {
		prompt = ""
	}
	start := ReviewerChatStart{BatchID: spec.BatchID, ReviewID: spec.ReviewSessionID, WorkerID: spec.WorkerID, ProjectID: spec.ProjectID, Harness: profile.ReviewChatHarness(), Model: spec.AgentConfig.Model, Effort: spec.AgentConfig.Effort, DataDir: l.dataDir, WorkspacePath: spec.WorkspacePath, Env: l.runtimeEnv(ctx, spec, nil, nil), Prompt: prompt, SystemPrompt: string(systemPrompt), ProviderConversationID: providerID}
	if restore {
		providerID, err = l.chat.RestoreReviewChat(ctx, start)
	} else {
		providerID, err = l.chat.StartReviewChat(ctx, start)
	}
	if err != nil {
		return LaunchResult{}, agentlaunch.RedactError(err, spec.ProjectEnv)
	}
	return LaunchResult{HandleID: reviewerChatHandlePrefix + spec.ReviewSessionID, LaunchID: strings.TrimSpace(spec.LaunchID), AgentSessionID: providerID}, nil
}

func (l *agentLauncher) launchReviewerTerminalWithMode(ctx context.Context, spec LaunchSpec, inv ports.ReviewInvocation, restoring bool) (LaunchResult, error) {
	reviewer, ok := l.reviewers.Reviewer(spec.Harness)
	if !ok {
		return LaunchResult{}, fmt.Errorf("no reviewer adapter for harness %q", spec.Harness)
	}
	if err := protectstate.Of(inv.Policy).Admit(ports.FencesState(reviewer, reviewerGOOS), string(spec.Harness), reviewerGOOS); err != nil {
		return LaunchResult{}, err
	}
	if gap := protectstate.Of(inv.Policy).Gap(ports.FencesState(reviewer, reviewerGOOS), string(spec.Harness), reviewerGOOS); gap != "" {
		// Reviewer sessions keep no decision record; the daemon log carries the gap.
		slog.Info("agent policy: reviewer not fenced", "harness", string(spec.Harness), "gap", gap)
	}
	// Some adapters deliver the task in argv rather than InitialMessage.
	// A replacement must start idle regardless of the delivery strategy.
	if spec.DeferInitialMessage {
		inv.Prompt = ""
	}
	if pl, ok := reviewer.(preLaunchReviewer); ok {
		if err := pl.PreLaunch(ctx, inv); err != nil {
			return LaunchResult{}, fmt.Errorf("reviewer pre-launch: %w", err)
		}
	}
	var cmd ports.ReviewCommandSpec
	nativeResumed := false
	if restoring {
		if restorer, ok := reviewer.(ports.ReviewerRestorer); ok {
			restoreCmd, restoreOK, err := restorer.ReviewRestoreCommand(ctx, inv)
			if err != nil {
				return LaunchResult{}, fmt.Errorf("reviewer restore command: %w", err)
			}
			if restoreOK {
				cmd = restoreCmd
				nativeResumed = restoreCmd.NativeResumed
			}
		}
	}
	if restoring && spec.RequireNativeHistory && len(cmd.Argv) == 0 {
		return LaunchResult{}, errors.New("reviewer native history resume is unavailable")
	}
	if len(cmd.Argv) == 0 {
		var err error
		cmd, err = reviewer.ReviewCommand(ctx, inv)
		if err != nil {
			return LaunchResult{}, fmt.Errorf("reviewer command: %w", err)
		}
	}
	handleID := reviewerHandleID(spec.WorkerID)
	// The reviewer handle is stable per worker, so a still-live pane from a
	// previous pass would otherwise block `tmux new-session` (duplicate name) or,
	// worse, keep serving under its old harness. Destroy any stale pane on this
	// handle first so the reviewer always (re)launches under spec.Harness's
	// sandbox/permissions/env — which are applied only here at Create, never by
	// Notify. Destroy is idempotent when no pane exists (first spawn / dead pane).
	if err := l.runtime.Destroy(ctx, ports.RuntimeHandle{ID: handleID}); err != nil {
		return LaunchResult{}, fmt.Errorf("reviewer replace stale pane: %w", err)
	}
	// The bare name routes to one backend only. A pane the runtime hosted under
	// a backend-qualified handle (the native PTY host keeps its host, and the
	// name, alive after the reviewer exits) would otherwise block Create, which
	// then falls back to another backend and leaks the old host. Chat handles
	// are not runtime panes; the engine stops that work itself.
	if previous := strings.TrimSpace(spec.PreviousHandleID); previous != "" && previous != handleID {
		if _, chat := reviewerChatID(previous); !chat {
			if err := l.runtime.Destroy(ctx, ports.RuntimeHandle{ID: previous}); err != nil {
				return LaunchResult{}, fmt.Errorf("reviewer replace previous pane: %w", err)
			}
		}
	}
	workingDirectory := cmd.WorkingDirectory
	if workingDirectory == "" {
		workingDirectory = spec.WorkspacePath
	}
	env := l.runtimeEnv(ctx, spec, cmd.Argv, cmd.Env)
	argv := cmd.Argv
	if strings.TrimSpace(spec.LaunchID) != "" {
		executable, resolveErr := l.executable()
		if resolveErr != nil {
			return LaunchResult{}, fmt.Errorf("resolve Tallpine executable: %w", resolveErr)
		}
		env[sessionmanager.EnvSupervisedProcess] = "1"
		supervisorArgv := []string{executable, "agent-process", "supervise", "--session", handleID}
		if reviewID := strings.TrimSpace(spec.ReviewSessionID); reviewID != "" {
			supervisorArgv = append(supervisorArgv, "--activity-review", reviewID)
		}
		supervisorArgv = append(supervisorArgv, "--launch", spec.LaunchID, "--")
		argv = append(supervisorArgv, argv...)
	}
	handle, err := l.runtime.Create(ctx, ports.RuntimeConfig{
		SessionID:     domain.SessionID(handleID),
		WorkspacePath: workingDirectory,
		Argv:          argv,
		Env:           env,
	})
	if err != nil {
		return LaunchResult{}, fmt.Errorf("reviewer runtime: %w", agentlaunch.RedactError(err, spec.ProjectEnv))
	}
	if cmd.InitialMessage != "" && !spec.DeferInitialMessage {
		if err := l.waitForPromptReadiness(ctx, reviewer, handle); err != nil {
			return LaunchResult{}, fmt.Errorf("reviewer prompt readiness: %w", err)
		}
		if err := l.runtime.SendMessage(ctx, handle, cmd.InitialMessage); err != nil {
			return LaunchResult{}, fmt.Errorf("reviewer initial message: %w", err)
		}
	}
	agentSessionID := strings.TrimSpace(cmd.AgentSessionID)
	if agentSessionID == "" {
		agentSessionID = strings.TrimSpace(spec.AgentSessionID)
	}
	return LaunchResult{HandleID: handle.ID, LaunchID: strings.TrimSpace(spec.LaunchID), AgentSessionID: agentSessionID, NativeResumed: nativeResumed}, nil
}

func (l *agentLauncher) waitForPromptReadiness(ctx context.Context, reviewer ports.Reviewer, handle ports.RuntimeHandle) error {
	hints := ports.PromptReadinessHints{InitialDelay: defaultReviewerInitialDelay}
	if provider, ok := reviewer.(ports.ReviewerPromptReadinessProvider); ok {
		provided, err := provider.ReviewPromptReadinessHints(ctx)
		if err != nil {
			return err
		}
		hints = provided
	}
	if hints.InitialDelay > 0 {
		timer := time.NewTimer(hints.InitialDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	if len(hints.Patterns) == 0 || hints.Timeout <= 0 {
		return nil
	}
	poll := hints.PollInterval
	if poll <= 0 {
		poll = 200 * time.Millisecond
	}
	lines := hints.Lines
	if lines <= 0 {
		lines = 80
	}
	deadline := time.NewTimer(hints.Timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		output, err := l.runtime.GetOutput(ctx, handle, lines)
		if err == nil && outputContainsAny(output, hints.Patterns) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			// Readiness is best-effort: never block a review forever when a CLI
			// changes its prompt marker. The startup delay still prevents an
			// immediate write into process initialization.
			return nil
		case <-ticker.C:
		}
	}
}

func outputContainsAny(output string, patterns []string) bool {
	for _, pattern := range patterns {
		if pattern != "" && strings.Contains(output, pattern) {
			return true
		}
	}
	return false
}

func (l *agentLauncher) runtimeEnv(ctx context.Context, spec LaunchSpec, argv []string, base map[string]string) map[string]string {
	env := agentlaunch.MergeEnv(spec.ProjectEnv, base)
	delete(env, sessionmanager.EnvSessionID)
	// Never the worker's credential, nor one the project env names.
	delete(env, sessionmanager.EnvSessionToken)
	delete(env, sessionmanager.EnvSessionTokenRunFile)
	delete(env, sessionmanager.EnvSessionAuth)
	delete(env, sessionmanager.EnvSessionAuthRunFile)
	if l.tokens != nil && strings.TrimSpace(l.runFile) != "" {
		if token := l.tokens.IssueReviewer(spec.ReviewSessionID, spec.WorkerID); token != "" {
			env[sessionmanager.EnvSessionToken] = token
			env[sessionmanager.EnvSessionTokenRunFile] = l.runFile
			env[sessionmanager.EnvSessionAuth] = token
			env[sessionmanager.EnvSessionAuthRunFile] = l.runFile
		}
	}
	env["TALLPINE_REVIEW_SESSION_ID"] = spec.ReviewSessionID
	env["TALLPINE_REVIEW_WORKER_SESSION_ID"] = string(spec.WorkerID)
	env["TALLPINE_REVIEW_HARNESS"] = string(spec.Harness)
	env[brand.Env("REVIEW_PRS")] = reviewPRPaths(spec)
	if strings.TrimSpace(spec.LaunchID) != "" {
		env[sessionmanager.EnvRuntimeLaunchID] = spec.LaunchID
	}
	env[sessionmanager.EnvProjectID] = string(spec.ProjectID)
	env[sessionmanager.EnvDataDir] = l.dataDir
	l.applyReviewerTempEnv(env, spec)
	if strings.TrimSpace(l.runFile) != "" {
		env[EnvRunFile] = l.runFile
	}
	// pinnedDir is whichever directory ends up at the head of PATH here, so the
	// launch-binary prepend below can put it back rather than letting a foreign
	// `tallpine` beside the agent binary win a bare `tallpine` inside the reviewer pane.
	pinnedDir := ""
	path, err := sessionmanager.HookPATH(l.executable, os.Getenv, env, l.dataDir)
	if err == nil {
		env["PATH"] = path
		pinnedDir = agentlaunch.PinnedDir(l.executable, l.dataDir)
	} else if shimDir, shimErr := l.ensureTallpineShimDir(); shimErr == nil {
		env["PATH"] = prependPathDir(shimDir, env["PATH"])
		pinnedDir = shimDir
	} else {
		env[EnvTallpineCommandWarning] = fmt.Sprintf("PATH pin failed: %v; Tallpine shim fallback failed: %v", err, shimErr)
	}
	agentlaunch.AugmentRuntimePATHForLaunchBinary(ctx, env, argv, exec.LookPath, pinnedDir)
	l.applyGitHubAccountEnv(ctx, spec, env)
	l.applyEnvFilter(ctx, spec, env)
	return env
}

func (l *agentLauncher) ensureTallpineShimDir() (string, error) {
	if strings.TrimSpace(l.dataDir) == "" {
		return "", fmt.Errorf("reviewer Tallpine shim data directory is required")
	}
	exe, err := l.executable()
	if err != nil {
		return "", fmt.Errorf("resolve Tallpine executable: %w", err)
	}
	if !filepath.IsAbs(exe) {
		exe, err = filepath.Abs(exe)
		if err != nil {
			return "", fmt.Errorf("make Tallpine executable absolute: %w", err)
		}
	}
	shimDir := filepath.Join(l.dataDir, "reviewer-runtime", "bin")
	if err := os.MkdirAll(shimDir, 0o700); err != nil {
		return "", fmt.Errorf("create reviewer Tallpine shim directory: %w", err)
	}
	shimPath := filepath.Join(shimDir, brand.CLIName)
	if runtime.GOOS == "windows" {
		shimPath += ".cmd"
	}
	if err := os.WriteFile(shimPath, []byte(tallpineShimScript(exe)), 0o600); err != nil {
		return "", fmt.Errorf("write reviewer Tallpine shim: %w", err)
	}
	if err := os.Chmod(shimPath, 0o700); err != nil { // #nosec G302 -- the reviewer shim must be executable by the Tallpine user.
		return "", fmt.Errorf("mark reviewer Tallpine shim executable: %w", err)
	}
	return shimDir, nil
}

func tallpineShimScript(executable string) string {
	if runtime.GOOS == "windows" {
		return "@echo off\r\n\"" + executable + "\" %*\r\n"
	}
	return "#!/bin/sh\nexec " + shellQuote(executable) + ` "$@"` + "\n"
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func prependPathDir(dir, path string) string {
	if strings.TrimSpace(path) == "" {
		return dir
	}
	return dir + string(os.PathListSeparator) + path
}

// reviewerTempKeys are the variables Claude Code and other tools read for temp
// files. Without CLAUDE_CODE_TMPDIR a Claude reviewer writes its per-project
// state into the shared /tmp/claude-<uid>, where every session can see it.
var reviewerTempKeys = []string{"TMPDIR", "TMP", "TEMP", "CLAUDE_CODE_TMPDIR", "CLAUDE_TMPDIR"}

// reviewerTempDir is the reviewer's own temp directory, inside the reviewed
// worker's session temp dir so the worker's teardown removes it, created on
// first use. Empty when the project env sets a temp key (that value is kept)
// or the dir cannot be made. The launch env and the invocation (which a fenced
// reviewer opens for writes) both take it from here.
func (l *agentLauncher) reviewerTempDir(spec LaunchSpec) string {
	for _, key := range reviewerTempKeys {
		if _, ok := spec.ProjectEnv[key]; ok {
			return ""
		}
	}
	base := sessionscratch.TempDir(l.dataDir, spec.WorkerID)
	if base == "" {
		return ""
	}
	dir := filepath.Join(base, "review")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	return dir
}

// applyReviewerTempEnv points every temp key at reviewerTempDir.
func (l *agentLauncher) applyReviewerTempEnv(env map[string]string, spec LaunchSpec) {
	dir := l.reviewerTempDir(spec)
	if dir == "" {
		return
	}
	for _, key := range reviewerTempKeys {
		env[key] = dir
	}
	if _, ok := spec.ProjectEnv[sessionscratch.TempPrefixEnv]; !ok {
		env[sessionscratch.TempPrefixEnv] = sessionscratch.TempPrefix(dir)
	}
}
