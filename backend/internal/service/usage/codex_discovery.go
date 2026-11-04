package usage

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func (c *Collector) registerDiscoveredCodexChildren(
	ctx context.Context,
	binding domain.UsageBindingRecord,
	now time.Time,
) error {
	sources, err := c.store.ListUsageSourcesForBinding(ctx, binding.ID)
	if err != nil {
		return err
	}
	return c.registerDiscoveredCodexChildrenWithInventory(
		ctx,
		binding,
		now,
		newBindingSourceInventory(sources),
	)
}

func (c *Collector) registerDiscoveredCodexChildrenWithInventory(
	ctx context.Context,
	binding domain.UsageBindingRecord,
	now time.Time,
	inventory *bindingSourceInventory,
) error {
	sources := latestCodexSourcesByNativeSession(inventory.snapshot())
	seen := make(map[string]struct{})
	var errs []error
	for _, source := range sources {
		if source.Kind != domain.UsageSourceCodexRollout || source.NativeSessionID == "" {
			continue
		}
		for _, childID := range discoveredCodexChildIDs(source.ParserStateJSON) {
			key := source.NativeSessionID + "\x00" + childID
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			path, err := c.discoverCodexPath(ctx, childID, source.NativeSessionID)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if path == "" {
				continue
			}
			if _, err := c.registerSourceWithInventory(
				ctx,
				binding,
				domain.UsageSourceCodexRollout,
				childID,
				childID,
				path,
				now,
				false,
				inventory,
				source.NativeSessionID,
			); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func latestCodexSourcesByNativeSession(sources []domain.UsageSourceRecord) []domain.UsageSourceRecord {
	latest := make(map[string]domain.UsageSourceRecord)
	for _, source := range sources {
		if source.Kind != domain.UsageSourceCodexRollout || source.NativeSessionID == "" {
			continue
		}
		current, ok := latest[source.NativeSessionID]
		if !ok || source.Generation > current.Generation ||
			(source.Generation == current.Generation && source.ID > current.ID) {
			latest[source.NativeSessionID] = source
		}
	}
	result := make([]domain.UsageSourceRecord, 0, len(latest))
	for _, source := range latest {
		result = append(result, source)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Generation != result[j].Generation {
			return result[i].Generation < result[j].Generation
		}
		return result[i].ID < result[j].ID
	})
	return result
}

func discoveredCodexChildIDs(raw string) []string {
	var state struct {
		Version    int                    `json:"version"`
		SourceKind domain.UsageSourceKind `json:"source_kind"`
		Codex      *struct {
			DiscoveredChildIDs []string `json:"discovered_child_ids"`
		} `json:"codex"`
	}
	if json.Unmarshal([]byte(raw), &state) != nil || state.Version != 1 ||
		state.SourceKind != domain.UsageSourceCodexRollout || state.Codex == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(state.Codex.DiscoveredChildIDs))
	result := make([]string, 0, len(state.Codex.DiscoveredChildIDs))
	for _, childID := range state.Codex.DiscoveredChildIDs {
		if len(result) >= maxCodexChildIDs {
			break
		}
		parsed, err := uuid.Parse(childID)
		if err != nil || parsed.String() != childID || len(childID) > maxUsageMetadataBytes {
			continue
		}
		if _, ok := seen[childID]; ok {
			continue
		}
		seen[childID] = struct{}{}
		result = append(result, childID)
	}
	return result
}

func (c *Collector) codexDiscoveryStillPending(ctx context.Context, event, hookPath, discoveredPath string) bool {
	if strings.TrimSpace(hookPath) != "" {
		return false
	}
	if strings.TrimSpace(discoveredPath) == "" {
		return true
	}
	return event == "session-start" && !pathWithinRoot(ctx, discoveredPath, c.roots.CodexSessions)
}

func (c *Collector) discoverCodexPath(ctx context.Context, nativeID, parentID string) (string, error) {
	if !nativeUsageIDPattern.MatchString(nativeID) {
		return "", nil
	}
	patterns := []string{
		filepath.Join(c.roots.CodexSessions, "*", "*", "*", "*"+nativeID+"*.jsonl"),
		filepath.Join(c.roots.CodexArchived, "*"+nativeID+"*.jsonl"),
	}
	type candidate struct {
		path string
		mod  time.Time
	}
	var matches []candidate
	for _, pattern := range patterns {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		paths, _ := filepath.Glob(pattern)
		if len(paths) > 128 {
			paths = paths[:128]
		}
		for _, path := range paths {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			resolved, _, _, err := c.validateSourcePath(ctx, domain.HarnessCodex, path)
			if err != nil || !codexSessionMetaMatches(resolved, nativeID, parentID) {
				continue
			}
			if info, err := os.Stat(resolved); err == nil {
				matches = append(matches, candidate{path: resolved, mod: info.ModTime()})
			}
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].mod.After(matches[j].mod) })
	if len(matches) == 0 {
		return "", nil
	}
	return matches[0].path, nil
}

func codexSessionMetaMatches(path, nativeID, parentID string) bool {
	meta, ok := readCodexSessionMeta(path)
	if !ok || meta.NativeSessionID != nativeID {
		return false
	}
	return parentID == "" || meta.ParentThreadID == parentID
}

type codexSessionMeta struct {
	NativeSessionID string
	ParentThreadID  string
}

func readCodexSessionMeta(path string) (codexSessionMeta, bool) {
	file, err := os.Open(path) //nolint:gosec // candidate passed the provider-root boundary.
	if err != nil {
		return codexSessionMeta{}, false
	}
	defer func() { _ = file.Close() }()
	data, err := bufio.NewReaderSize(file, maxCodexSessionMeta+1).ReadSlice('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return codexSessionMeta{}, false
	}
	data = bytes.TrimSuffix(data, []byte{'\n'})
	data = bytes.TrimSuffix(data, []byte{'\r'})
	if len(data) > maxCodexSessionMeta {
		return codexSessionMeta{}, false
	}
	var envelope struct {
		Type    string `json:"type"`
		Payload struct {
			ID     string          `json:"id"`
			Source json.RawMessage `json:"source"`
		} `json:"payload"`
	}
	if json.Unmarshal(data, &envelope) != nil || envelope.Type != "session_meta" || envelope.Payload.ID == "" {
		return codexSessionMeta{}, false
	}
	parentThreadID, ok := codexParentThreadID(envelope.Payload.Source)
	if !ok {
		return codexSessionMeta{}, false
	}
	return codexSessionMeta{
		NativeSessionID: envelope.Payload.ID,
		ParentThreadID:  parentThreadID,
	}, true
}

func codexParentThreadID(raw json.RawMessage) (string, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", true
	}
	if raw[0] == '"' {
		var source string
		return "", json.Unmarshal(raw, &source) == nil
	}
	if raw[0] != '{' {
		return "", false
	}
	var source struct {
		Subagent struct {
			ThreadSpawn struct {
				ParentThreadID string `json:"parent_thread_id"`
			} `json:"thread_spawn"`
		} `json:"subagent"`
	}
	if json.Unmarshal(raw, &source) != nil {
		return "", false
	}
	return source.Subagent.ThreadSpawn.ParentThreadID, true
}

func validCanonicalUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value && len(value) <= maxUsageMetadataBytes
}
