package modelcatalog

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"tallpine.eu/tallpine/backend/internal/ports"
)

var modelVersionPattern = regexp.MustCompile(`\d+(?:\.\d+)*`)

// sortNewestFirst orders models by the first version number in their label
// (then ID), highest first, so the latest release leads the picker. Models
// sharing a version keep base-before-variant order ("Sol 6" before
// "Sol 6 Astra"); unversioned models sort last, alphabetically.
func sortNewestFirst(models []ports.AgentModelInfo) []ports.AgentModelInfo {
	versions := make(map[string][]int, len(models))
	for _, item := range models {
		versions[item.ID] = modelVersion(item)
	}
	sort.SliceStable(models, func(i, j int) bool {
		if cmp := compareVersions(versions[models[i].ID], versions[models[j].ID]); cmp != 0 {
			return cmp > 0
		}
		return strings.ToLower(models[i].Label) < strings.ToLower(models[j].Label)
	})
	return models
}

func modelVersion(item ports.AgentModelInfo) []int {
	for _, source := range []string{item.Label, item.ID} {
		match := modelVersionPattern.FindString(source)
		if match == "" {
			continue
		}
		parts := strings.Split(match, ".")
		version := make([]int, 0, len(parts))
		for _, part := range parts {
			n, err := strconv.Atoi(part)
			if err != nil {
				break
			}
			version = append(version, n)
		}
		return version
	}
	return nil
}

// compareVersions returns >0 when a is newer than b. Missing components count
// as zero, and any version outranks none.
func compareVersions(a, b []int) int {
	if len(a) == 0 || len(b) == 0 {
		return len(a) - len(b)
	}
	for index := 0; index < max(len(a), len(b)); index++ {
		var left, right int
		if index < len(a) {
			left = a[index]
		}
		if index < len(b) {
			right = b[index]
		}
		if left != right {
			return left - right
		}
	}
	return 0
}

func hasDiscoverySource(agentID string) bool {
	switch agentID {
	// Harnesses whose catalog comes from a daemon-injected surface rather than a
	// command spec: Codex's app-server, Claude's provider probe, and DeepSeek
	// Harness's ACP configuration catalog.
	case "claude-code", "codex", "deepseek-harness":
		return true
	}
	_, hasCommand := commandSpecs[agentID]
	return hasCommand
}
