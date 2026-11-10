package usage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/pathx"
)

func (c *Collector) validateSourcePath(ctx context.Context, harness domain.AgentHarness, path string) (string, string, int64, error) {
	if err := ctx.Err(); err != nil {
		return "", "", 0, err
	}
	if len(path) > maxUsagePathBytes {
		return "", "", 0, errors.New(domain.UsageErrorArtifactPathRejected)
	}
	if !filepath.IsAbs(path) || strings.ToLower(filepath.Ext(path)) != ".jsonl" {
		return "", "", 0, errors.New(domain.UsageErrorArtifactPathRejected)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", "", 0, errors.New(domain.UsageErrorArtifactMissing)
	}
	if err := ctx.Err(); err != nil {
		return "", "", 0, err
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", "", 0, errors.New(domain.UsageErrorArtifactMissing)
	}
	roots := c.allowedRoots(harness)
	allowed := false
	for _, root := range roots {
		if root == "" {
			continue
		}
		resolvedRoot, rootErr := filepath.EvalSymlinks(filepath.Clean(root))
		if rootErr != nil {
			continue
		}
		rel, relErr := filepath.Rel(resolvedRoot, resolved)
		if relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", "", 0, errors.New(domain.UsageErrorArtifactPathRejected)
	}
	identity, err := SourceIdentity(ctx, resolved)
	if err != nil {
		return "", "", 0, err
	}
	return resolved, identity, info.Size(), nil
}

func validateSourceAttribution(
	binding domain.UsageBindingRecord,
	kind domain.UsageSourceKind,
	nativeSessionID string,
	subagentID string,
	resolved string,
	expectedParentID string,
) error {
	rejected := func() error { return errors.New(domain.UsageErrorArtifactPathRejected) }
	switch kind {
	case domain.UsageSourceCodexRollout:
		if binding.Harness != domain.HarnessCodex {
			return rejected()
		}
		meta, ok := readCodexSessionMeta(resolved)
		if !ok || meta.NativeSessionID != nativeSessionID {
			return rejected()
		}
		if subagentID == "" {
			if meta.ParentThreadID != "" {
				return rejected()
			}
		} else if subagentID != nativeSessionID || expectedParentID == "" || meta.ParentThreadID != expectedParentID {
			return rejected()
		}
	case domain.UsageSourceClaudeMain:
		if binding.Harness != domain.HarnessClaudeCode || nativeSessionID != binding.NativeRootID ||
			filepath.Base(resolved) != nativeSessionID+".jsonl" {
			return rejected()
		}
	case domain.UsageSourceClaudeSubagent:
		subagentsDir := filepath.Dir(resolved)
		rootSessionDir := filepath.Dir(subagentsDir)
		if binding.Harness != domain.HarnessClaudeCode || nativeSessionID != binding.NativeRootID ||
			subagentID == "" || filepath.Base(subagentsDir) != "subagents" ||
			filepath.Base(rootSessionDir) != binding.NativeRootID ||
			filepath.Base(resolved) != "agent-"+subagentID+".jsonl" {
			return rejected()
		}
	default:
		return rejected()
	}
	return nil
}

func (c *Collector) allowedRoots(harness domain.AgentHarness) []string {
	switch harness {
	case domain.HarnessClaudeCode:
		return []string{c.roots.ClaudeProjects}
	case domain.HarnessCodex:
		return []string{c.roots.CodexSessions, c.roots.CodexArchived}
	default:
		return nil
	}
}

func sourceKindForHarness(harness domain.AgentHarness) (domain.UsageSourceKind, bool) {
	switch harness {
	case domain.HarnessClaudeCode:
		return domain.UsageSourceClaudeMain, true
	case domain.HarnessCodex:
		return domain.UsageSourceCodexRollout, true
	default:
		return "", false
	}
}

func pathWithinRoot(ctx context.Context, path, root string) bool {
	if ctx.Err() != nil {
		return false
	}
	if strings.TrimSpace(path) == "" || strings.TrimSpace(root) == "" {
		return false
	}
	within, err := pathx.WithinResolved(filepath.Clean(root), filepath.Clean(path))
	return err == nil && within && ctx.Err() == nil
}

// discoverPath finds the newest transcript for a native session id from a fresh
// view of the provider root. Callers resolving many ids refresh once and use
// discoverIndexedPath; see ReconcileSources.
func (c *Collector) discoverPath(ctx context.Context, harness domain.AgentHarness, nativeID string) (string, error) {
	if harness == domain.HarnessClaudeCode {
		if err := c.claudeProjects.refresh(ctx); err != nil {
			return "", err
		}
	}
	return c.discoverIndexedPath(ctx, harness, nativeID)
}

func (c *Collector) discoverIndexedPath(ctx context.Context, harness domain.AgentHarness, nativeID string) (string, error) {
	if !nativeUsageIDPattern.MatchString(nativeID) {
		return "", nil
	}
	var paths []string
	switch harness {
	case domain.HarnessClaudeCode:
		paths = c.claudeProjects.lookup(nativeID + ".jsonl")
	case domain.HarnessCodex:
		return c.discoverCodexPath(ctx, nativeID, "")
	}
	type candidate struct {
		path string
		mod  time.Time
	}
	if len(paths) > 128 {
		paths = paths[:128]
	}
	var matches []candidate
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			matches = append(matches, candidate{path: path, mod: info.ModTime()})
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].mod.After(matches[j].mod) })
	if len(matches) == 0 {
		return "", nil
	}
	return matches[0].path, nil
}

func nativeIDFromTranscript(path string) string {
	base := strings.TrimSuffix(filepath.Base(strings.TrimSpace(path)), filepath.Ext(path))
	if nativeUsageIDPattern.MatchString(base) {
		return base
	}
	return ""
}
