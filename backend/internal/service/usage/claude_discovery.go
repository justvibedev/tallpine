package usage

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// maxClaudeContinuationScanBytes bounds the size of one transcript record that
// is parsed while extracting its session id and cwd.
const maxClaudeContinuationScanBytes = 256 << 10

// discoverClaudeContinuations registers resume transcripts that Claude Code
// wrote beside an already-registered transcript without ever emitting a Tallpine
// hook: `claude --resume` continues the conversation in a new native session
// file inside the same project directory, and nothing else in the pipeline
// learns about it, so all post-resume usage would stay invisible forever. Tallpine
// gives every session its own worktree, so a transcript's recorded cwd
// identifies its Tallpine session; two live Tallpine sessions never share one workspace by
// design. If that invariant ever changes, cwd alone no longer attributes a
// continuation and this scan needs a stronger discriminator.
func (c *Collector) discoverClaudeContinuations(
	ctx context.Context,
	binding domain.UsageBindingRecord,
	session domain.SessionRecord,
	mainPath string,
	now time.Time,
) error {
	workspace := strings.TrimSpace(session.Metadata.WorkspacePath)
	if workspace == "" {
		return nil
	}
	candidates, err := filepath.Glob(filepath.Join(filepath.Dir(mainPath), "*.jsonl"))
	if err != nil {
		return err
	}
	if len(candidates) > maxClaudeContinuationScanRecords {
		candidates = candidates[:maxClaudeContinuationScanRecords]
	}
	known, err := c.store.ListUsageSourcesForBinding(ctx, binding.ID)
	if err != nil {
		return err
	}
	knownPaths := make(map[string]struct{}, len(known))
	for _, source := range known {
		knownPaths[source.ArtifactPath] = struct{}{}
	}
	var errs []error
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, ok := knownPaths[candidate]; ok {
			continue
		}
		// registerSource re-validates authoritatively; this pre-check only
		// keeps one unreadable candidate from aborting the whole scan.
		resolved, _, _, err := c.validateSourcePath(ctx, domain.HarnessClaudeCode, candidate)
		if err != nil {
			continue
		}
		claimed, err := c.store.HasUsageSourceByPath(ctx, resolved)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if claimed {
			continue
		}
		nativeID, cwd, ok := readClaudeTranscriptMeta(ctx, resolved)
		if !ok || !nativeUsageIDPattern.MatchString(nativeID) || !sameWorkspacePath(cwd, workspace) {
			continue
		}
		if filepath.Base(resolved) != nativeID+".jsonl" {
			// validateSourceAttribution rejects a claude_main source whose file
			// name does not carry the native id, so upserting the binding first
			// would strand a zero-source binding and fail every reconcile pass.
			continue
		}
		newBinding, err := c.upsertContinuationBinding(ctx, session, binding.Harness, nativeID, now)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if _, err := c.registerSource(
			ctx,
			newBinding,
			domain.UsageSourceClaudeMain,
			nativeID,
			"",
			resolved,
			now,
			false,
		); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := c.registerDiscoveredClaudeSubagents(ctx, newBinding, resolved, now, false); err != nil {
			errs = append(errs, err)
			continue
		}
		if newBinding.State == domain.UsageBindingFinalizing {
			if err := c.settleFinalizingBinding(ctx, newBinding.ID, now); err != nil {
				errs = append(errs, err)
			}
		}
		c.logger.Info("usage continuation source registered",
			"session", session.ID, "native_root_id", nativeID, "path", resolved)
	}
	return errors.Join(errs...)
}

func (c *Collector) upsertContinuationBinding(
	ctx context.Context,
	session domain.SessionRecord,
	harness domain.AgentHarness,
	nativeID string,
	now time.Time,
) (domain.UsageBindingRecord, error) {
	existing, exists, err := c.store.GetUsageBinding(ctx, session.ID, harness, nativeID)
	if err != nil {
		return domain.UsageBindingRecord{}, err
	}
	if exists {
		// Preserve state and provider evidence already recorded for this
		// native session; only the source registration below matters here.
		return existing, nil
	}
	state := domain.UsageBindingActive
	if session.IsTerminated || session.Activity.State == domain.ActivityExited {
		state = domain.UsageBindingFinalizing
	}
	return c.store.UpsertUsageBinding(ctx, domain.UsageBindingRecord{
		SessionID:    session.ID,
		Harness:      harness,
		NativeRootID: nativeID,
		State:        state,
		UpdatedAt:    now,
	})
}

type claudeTranscriptMetaRecord struct {
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
}

// readClaudeTranscriptMeta extracts the native session id and working directory
// from a bounded window of leading records. sessionId is present on every
// transcript record but cwd first appears on user/assistant records, so the
// first line alone is not enough. A record larger than maxClaudeContinuationScanBytes
// is drained and skipped instead of aborting the scan: a transcript that opens
// with a large paste or tool result must not become permanently undiscoverable.
func readClaudeTranscriptMeta(ctx context.Context, path string) (string, string, bool) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", false
	}
	defer func() { _ = file.Close() }()
	reader := bufio.NewReader(io.LimitReader(file, maxClaudeContinuationScanTotalBytes))
	sessionID := ""
	cwd := ""
	for scanned := 0; scanned < maxClaudeContinuationScanRecords; {
		if err := ctx.Err(); err != nil {
			return "", "", false
		}
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 && len(line) <= maxClaudeContinuationScanBytes {
			scanned++
			var record claudeTranscriptMetaRecord
			if json.Unmarshal(line, &record) == nil {
				if sessionID == "" {
					sessionID = strings.TrimSpace(record.SessionID)
				}
				if cwd == "" {
					cwd = strings.TrimSpace(record.Cwd)
				}
			}
		}
		if sessionID != "" && cwd != "" {
			return sessionID, cwd, true
		}
		if readErr != nil {
			break
		}
	}
	return "", "", false
}

// sameWorkspacePath compares the transcript's recorded cwd with the session
// workspace after cleaning, falling back to symlink resolution when the direct
// comparison misses.
func sameWorkspacePath(cwd, workspace string) bool {
	cwd = strings.TrimSpace(cwd)
	workspace = strings.TrimSpace(workspace)
	if cwd == "" || workspace == "" {
		return false
	}
	if filepath.Clean(cwd) == filepath.Clean(workspace) {
		return true
	}
	resolvedCwd, cwdErr := filepath.EvalSymlinks(filepath.Clean(cwd))
	resolvedWorkspace, workspaceErr := filepath.EvalSymlinks(filepath.Clean(workspace))
	return cwdErr == nil && workspaceErr == nil && resolvedCwd == resolvedWorkspace
}

func (c *Collector) registerDiscoveredClaudeSubagents(
	ctx context.Context,
	binding domain.UsageBindingRecord,
	mainPath string,
	now time.Time,
	reactivateExisting bool,
) error {
	var errs []error
	paths, err := discoverClaudeSubagentPaths(ctx, mainPath)
	if err != nil {
		return err
	}
	for _, path := range paths {
		name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		subagentID := strings.TrimPrefix(name, "agent-")
		if _, err := c.registerSource(
			ctx,
			binding,
			domain.UsageSourceClaudeSubagent,
			binding.NativeRootID,
			boundedUsageMetadata(subagentID),
			path,
			now,
			reactivateExisting,
		); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func discoverClaudeSubagentPaths(ctx context.Context, mainPath string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	base := strings.TrimSuffix(mainPath, filepath.Ext(mainPath))
	pattern := filepath.Join(base, "subagents", "agent-*.jsonl")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		path string
		mod  time.Time
	}
	candidates := make([]candidate, 0, len(paths))
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if info, statErr := os.Stat(path); statErr == nil && info.Mode().IsRegular() {
			candidates = append(candidates, candidate{path: path, mod: info.ModTime()})
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].mod.Before(candidates[j].mod) })
	result := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		result = append(result, candidate.path)
	}
	return result, nil
}
