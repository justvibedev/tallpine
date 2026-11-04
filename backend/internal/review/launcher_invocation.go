package review

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func (l *agentLauncher) invocation(spec LaunchSpec) ports.ReviewInvocation {
	prompt, systemPrompt := reviewTexts(spec)
	return ports.ReviewInvocation{
		ReviewerID:      reviewerHandleID(spec.WorkerID),
		RunID:           spec.RunID,
		WorkerSessionID: spec.WorkerID,
		AgentSessionID:  spec.AgentSessionID,
		PRURL:           spec.PRURL,
		TargetSHA:       spec.TargetSHA,
		ReviewQueue:     spec.ReviewQueue,
		ReviewIndex:     spec.ReviewIndex,
		Config:          spec.AgentConfig,
		WorkspacePath:   spec.WorkspacePath,
		Env:             spec.ProjectEnv,
		DataDir:         l.dataDir,
		RunFilePath:     l.runFile,
		TempDir:         l.reviewerTempDir(spec),
		Prompt:          prompt,
		SystemPrompt:    systemPrompt,
	}
}

// prepareInvocation stores the full reviewer instructions outside the
// worktree, then replaces the terminal-visible prompt with a short file
// reference.
// Reviewer panes are shared by desktop, mobile, and direct runtime attaches,
// so keeping the full text out of the PTY is the only device-independent way
// to hide it.
func (l *agentLauncher) prepareInvocation(ctx context.Context, spec LaunchSpec) (ports.ReviewInvocation, error) {
	if err := ctx.Err(); err != nil {
		return ports.ReviewInvocation{}, err
	}
	inv := l.invocation(spec)
	inv.Policy = l.decide(ctx, spec)
	if strings.TrimSpace(l.dataDir) == "" {
		return ports.ReviewInvocation{}, fmt.Errorf("reviewer prompt data directory is required")
	}
	if strings.TrimSpace(spec.BatchID) == "" || strings.TrimSpace(spec.RunID) == "" {
		return ports.ReviewInvocation{}, fmt.Errorf("reviewer prompt batch and run ids are required")
	}
	promptRoot := filepath.Join(l.dataDir, "prompts", string(spec.WorkerID), "reviewer")
	requestDir := filepath.Join(promptRoot, "requests", spec.BatchID, spec.RunID)
	if err := os.MkdirAll(requestDir, 0o700); err != nil {
		return ports.ReviewInvocation{}, fmt.Errorf("create reviewer prompt directory: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return ports.ReviewInvocation{}, err
	}
	taskPath := filepath.Join(requestDir, "task.md")
	if err := os.WriteFile(taskPath, []byte(strings.TrimRight(inv.Prompt, "\n")+"\n"), 0o600); err != nil {
		return ports.ReviewInvocation{}, fmt.Errorf("write reviewer task prompt: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return ports.ReviewInvocation{}, err
	}
	systemPath := filepath.Join(promptRoot, "system.md")
	systemPrompt := strings.TrimRight(inv.SystemPrompt, "\n") + "\n\n" +
		"Tallpine stores each review task in an immutable file. Whenever Tallpine asks you to start a review task, " +
		"read the exact file path in that request first and follow it completely.\n"
	if err := os.WriteFile(systemPath, []byte(systemPrompt), 0o600); err != nil {
		return ports.ReviewInvocation{}, fmt.Errorf("write reviewer system prompt: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return ports.ReviewInvocation{}, err
	}
	inv.Prompt = reviewerTaskMessagePrefix + filepath.ToSlash(taskPath) + "`."
	inv.SystemPrompt = ""
	inv.SystemPromptFile = systemPath
	inv.TaskPromptFile = taskPath
	inv.TaskPromptRoot = promptRoot
	return inv, nil
}

func (l *agentLauncher) prepareIdleInvocation(ctx context.Context, spec LaunchSpec) (ports.ReviewInvocation, error) {
	promptRoot := filepath.Join(l.dataDir, "prompts", string(spec.WorkerID), "reviewer")
	systemPath := filepath.Join(promptRoot, "system.md")
	systemPrompt := reviewSystemPrompt() + "\n\n" + localFlowNote + "\n\n" +
		"Tallpine may restore your terminal before a new review task exists. In that state, wait for Tallpine to send a review task file path before reviewing or submitting results.\n"
	if err := os.MkdirAll(promptRoot, 0o700); err != nil {
		return ports.ReviewInvocation{}, fmt.Errorf("create reviewer prompt directory: %w", err)
	}
	if err := os.WriteFile(systemPath, []byte(systemPrompt), 0o600); err != nil {
		return ports.ReviewInvocation{}, fmt.Errorf("write reviewer system prompt: %w", err)
	}
	prompt := fmt.Sprintf("Reviewer terminal restored for worker session %s.\n\n%s\n\nWait for Tallpine to send the next review task file path before submitting a new review.", spec.WorkerID, previousReviewHistoryText(spec.PreviousRuns))
	return ports.ReviewInvocation{
		Policy:           l.decide(ctx, spec),
		ReviewerID:       reviewerHandleID(spec.WorkerID),
		WorkerSessionID:  spec.WorkerID,
		AgentSessionID:   spec.AgentSessionID,
		Config:           spec.AgentConfig,
		WorkspacePath:    spec.WorkspacePath,
		Env:              spec.ProjectEnv,
		DataDir:          l.dataDir,
		RunFilePath:      l.runFile,
		TempDir:          l.reviewerTempDir(spec),
		Prompt:           prompt,
		SystemPrompt:     "",
		SystemPromptFile: systemPath,
		TaskPromptRoot:   promptRoot,
	}, nil
}

func previousReviewHistoryText(runs []domain.ReviewRun) string {
	if len(runs) == 0 {
		return "No previous review runs are recorded for this worker session."
	}
	var b strings.Builder
	b.WriteString("Previous review runs for this worker session, newest first:")
	const maxRuns = 20
	for i, run := range runs {
		if i >= maxRuns {
			fmt.Fprintf(&b, "\n* ... %d older review run(s) omitted.", len(runs)-maxRuns)
			break
		}
		fmt.Fprintf(&b, "\n* %s", run.PRURL)
		if run.TargetSHA != "" {
			fmt.Fprintf(&b, " at %s", run.TargetSHA)
		}
		fmt.Fprintf(&b, " - status: %s", run.Status)
		if run.Verdict != "" {
			fmt.Fprintf(&b, ", verdict: %s", run.Verdict)
		}
		if run.GithubReviewID != "" {
			fmt.Fprintf(&b, ", GitHub review: %s", run.GithubReviewID)
		}
		body := strings.TrimSpace(run.Body)
		if body != "" {
			fmt.Fprintf(&b, "\n  Body: %s", truncateReviewHistoryBody(body))
		}
	}
	return b.String()
}

func truncateReviewHistoryBody(body string) string {
	const maxBody = 1200
	body = strings.Join(strings.Fields(body), " ")
	if len(body) <= maxBody {
		return body
	}
	return strings.TrimSpace(body[:maxBody]) + "..."
}

// reviewPRPaths lists the owner/repo/number of every PR this pass reviews,
// comma-separated, for the reviewer permission hook to bind `gh api` review
// posts to.
func reviewPRPaths(spec LaunchSpec) string {
	urls := make([]string, 0, 1+len(spec.ReviewQueue))
	urls = append(urls, spec.PRURL)
	for _, task := range spec.ReviewQueue {
		urls = append(urls, task.PRURL)
	}
	var paths []string
	for _, raw := range urls {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil {
			continue
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) == 4 && parts[2] == "pull" {
			paths = append(paths, parts[0]+"/"+parts[1]+"/"+parts[3])
		}
	}
	return strings.Join(paths, ",")
}
