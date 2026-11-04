// Package contextwindow resolves what Tallpine knows about a model's context window.
//
// Three quantities stay apart: capacity (the model's real window, resolved
// here), the compaction threshold (domain.AutoCompactTokenCount), and the usage
// an agent reports (display only). An agent's reported window is never a source
// of capacity: once Tallpine sets a compaction env, Claude Code reports that limit as
// its window, so feeding it back compounds a percent on every restart.
package contextwindow

import (
	"regexp"
	"strings"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// familyWindows is the window of each Claude model family. The ACP model list
// Claude Code advertises carries no window, so the catalog cannot answer for
// family aliases such as "opus"; this table is the last step before unknown.
var familyWindows = map[string]int64{
	"fable":  1_000_000,
	"opus":   1_000_000,
	"sonnet": 1_000_000,
	"haiku":  200_000,
}

var familyAlias = regexp.MustCompile(`^(fable|opus|sonnet|haiku)(\[[^\]]+\])?$`)

// fullIDAlias matches a full Claude id such as claude-sonnet-5-5 or
// claude-opus-5-5[1m]; the model list gives these no window of their own.
var fullIDAlias = regexp.MustCompile(`^claude-(fable|opus|sonnet|haiku)-[a-z0-9.-]*(\[[^\]]+\])?$`)

func fullIDFamily(model string) string {
	match := fullIDAlias.FindStringSubmatch(strings.ToLower(strings.TrimSpace(model)))
	if match == nil {
		return ""
	}
	return match[1]
}

// Capacity is the model's context window in tokens, 0 when unknown. In order:
// the catalog entry for the exact id, the catalog's newest windowed model of the
// family a bare alias names, then the static family table. An empty model means
// the catalog default (see AgentModelCatalog.ContextWindowFor).
func Capacity(catalog ports.AgentModelCatalog, model string) int64 {
	if window := catalog.ContextWindowFor(model); window > 0 {
		return window
	}
	family := familyOf(model)
	if family == "" {
		family = fullIDFamily(model)
	}
	if family == "" {
		return 0
	}
	var newest string
	var window int64
	for _, item := range catalog.Models {
		id := strings.ToLower(item.ID)
		if item.ContextWindow > 0 && strings.Contains(id, family) && !familyAlias.MatchString(id) && id > newest {
			newest, window = id, item.ContextWindow
		}
	}
	if window > 0 {
		return window
	}
	return familyWindows[family]
}

// Label is the name the catalog shows for the model, else the capitalised
// family for a bare alias, else "": the caller then keeps its own label.
func Label(catalog ports.AgentModelCatalog, model string) string {
	for _, item := range catalog.Models {
		if item.ID == model && strings.TrimSpace(item.Label) != "" && !strings.EqualFold(item.Label, model) {
			return item.Label
		}
	}
	if family := familyOf(model); family != "" {
		return strings.ToUpper(family[:1]) + family[1:]
	}
	return ""
}

func familyOf(model string) string {
	match := familyAlias.FindStringSubmatch(strings.ToLower(strings.TrimSpace(model)))
	if match == nil {
		return ""
	}
	return match[1]
}
