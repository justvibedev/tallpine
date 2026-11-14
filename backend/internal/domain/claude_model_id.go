package domain

import (
	"regexp"
	"strconv"
	"strings"
)

// claudeModelIDPattern matches a first-party Claude API model ID such as
// "claude-fable-5-2", "claude-opus-4-5-20251101" or "claude-opus-5-5[1m]".
var claudeModelIDPattern = regexp.MustCompile(`^claude-(fable|mythos|opus|sonnet|haiku)-\d+(?:-\d{1,2})?(?:-\d{8})?(?:\[[^\]]+\])?$`)

// IsClaudeModelID reports whether id is shaped like a first-party Claude model
// ID. The account's model list can lag a release (and Claude Code's own chat
// picker offers new models before it does), so callers accept a well-formed
// ID the list lacks and leave the exact version to the provider; a malformed
// one is still treated as a typo.
func IsClaudeModelID(id string) bool {
	return claudeModelIDPattern.MatchString(id)
}

// AcceptsUnlistedClaudeModelID reports whether a catalog of listedIDs uses
// first-party IDs, the only route where a well-formed ID the list lacks is
// plausibly just a release the list has not caught up with. Bedrock
// ("anthropic.claude-...") and Vertex ("claude-...@date") catalogs use their
// own formats, which IsClaudeModelID does not match, so for them an unlisted
// ID stays refused.
func AcceptsUnlistedClaudeModelID(id string, listedIDs []string) bool {
	if !IsClaudeModelID(id) {
		return false
	}
	for _, listed := range listedIDs {
		if IsClaudeModelID(listed) {
			return true
		}
	}
	return false
}

// ClaudeFamilyAliases are the short family names callers type ("haiku") in
// place of an exact model ID. Single source of truth for alias resolution.
var ClaudeFamilyAliases = []string{"fable", "opus", "sonnet", "haiku"}

var (
	claudeVersionNumber = regexp.MustCompile(`\d+`)
	claudeVersionNoise  = regexp.MustCompile(`\(.*?\)|\[.*?\]`)
)

// ClaudeFamilyCandidate is one catalog model offered to NewestClaudeFamilyModel.
// Skip excludes it (for example a model without effort levels).
type ClaudeFamilyCandidate struct {
	ID, Label string
	Skip      bool
}

// NewestClaudeFamilyModel returns the index of the newest candidate in the
// family a bare alias such as "opus" or "opus[1m]" names, or -1 when alias is
// not a family alias or no candidate belongs to the family. Used by both the
// CLI (resolving --model before validation) and the daemon.
func NewestClaudeFamilyModel(candidates []ClaudeFamilyCandidate, alias string) int {
	family := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(alias)), "[1m]")
	known := false
	for _, a := range ClaudeFamilyAliases {
		known = known || a == family
	}
	if !known {
		return -1
	}
	best := -1
	var bestVersion []int
	for i, c := range candidates {
		if c.Skip {
			continue
		}
		version := claudeFamilyVersion(c, family)
		if version == nil {
			continue
		}
		if best < 0 || compareVersions(version, bestVersion) > 0 {
			best, bestVersion = i, version
		}
	}
	return best
}

// claudeFamilyVersion reads major.minor from a model's label or ID, or nil when
// the model is not in the family. Snapshot dates and "v1:0" suffixes are longer
// than two digits or beyond the second number, so they are ignored.
func claudeFamilyVersion(c ClaudeFamilyCandidate, family string) []int {
	for _, text := range []string{c.Label, c.ID} {
		lower := claudeVersionNoise.ReplaceAllString(strings.ToLower(text), "")
		if !strings.Contains(lower, family) {
			continue
		}
		var version []int
		for _, part := range claudeVersionNumber.FindAllString(lower, -1) {
			if len(part) <= 2 && len(version) < 2 {
				n, _ := strconv.Atoi(part)
				version = append(version, n)
			}
		}
		if len(version) > 0 {
			return version
		}
	}
	return nil
}

func compareVersions(a, b []int) int {
	for i := 0; i < len(a) || i < len(b); i++ {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return x - y
		}
	}
	return 0
}
