package officialimport

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// TargetSettings is the part of this fork's daemon preferences the import maps
// onto.
type TargetSettings struct {
	DefaultSessionMode domain.SessionMode
	VersionRefresh     bool
	HarnessDefaults    map[string]domain.HarnessDefault
}

// SettingsTarget reads and writes this fork's daemon-owned preferences.
type SettingsTarget interface {
	Current(ctx context.Context) (TargetSettings, error)
	SetDefaultSessionMode(ctx context.Context, mode domain.SessionMode) error
	SetVersionRefresh(ctx context.Context, enabled bool) error
	SetHarnessDefault(ctx context.Context, harness domain.AgentHarness, value domain.HarnessDefault) error
}

// planSettings lists the global settings that map 1:1. Each entry is only a
// change when the target differs; the closures are run by applySettings.
func planSettings(ctx context.Context, target SettingsTarget, src sourceSettings, rep *Report) error {
	if !src.Present {
		return nil
	}
	cur, err := target.Current(ctx)
	if err != nil {
		return fmt.Errorf("read target settings: %w", err)
	}
	add := func(c SettingChange) { rep.Settings = append(rep.Settings, c) }

	if mode := domain.SessionMode(src.DefaultSessionMode); mode.Valid() {
		c := SettingChange{Key: "defaultSessionMode", Value: string(mode), Current: string(cur.DefaultSessionMode), Action: ActionUpdate}
		if mode == cur.DefaultSessionMode {
			c.Action = ActionUnchanged
		}
		c.apply = func() error { return target.SetDefaultSessionMode(ctx, mode) }
		add(c)
	}
	if src.VersionRefresh != nil {
		want := *src.VersionRefresh
		c := SettingChange{Key: "versionRefresh", Value: strconv.FormatBool(want), Current: strconv.FormatBool(cur.VersionRefresh), Action: ActionUpdate}
		if want == cur.VersionRefresh {
			c.Action = ActionUnchanged
		}
		c.apply = func() error { return target.SetVersionRefresh(ctx, want) }
		add(c)
	}

	var defaults map[string]domain.HarnessDefault
	if strings.TrimSpace(src.HarnessDefaults) != "" {
		if err := json.Unmarshal([]byte(src.HarnessDefaults), &defaults); err != nil {
			rep.Warnings = append(rep.Warnings, "harness model preferences could not be read and were not imported")
		}
	}
	for _, name := range sortedKeys(defaults) {
		harness := domain.AgentHarness(name)
		if !harness.IsKnown() {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("model preferences for harness %q were not imported: the harness is not available in Tallpine", name))
			continue
		}
		want := defaults[name]
		have := cur.HarnessDefaults[name]
		c := SettingChange{
			Key:     "harnessDefaults." + name,
			Value:   fmt.Sprintf("%d disabled models", len(want.DisabledModels)),
			Current: fmt.Sprintf("%d disabled models", len(have.DisabledModels)),
			Action:  ActionUpdate,
		}
		if slices.Equal(want.DisabledModels, have.DisabledModels) {
			c.Action = ActionUnchanged
		}
		c.apply = func() error { return target.SetHarnessDefault(ctx, harness, want) }
		add(c)
	}
	if src.CloudOffering {
		rep.Warnings = append(rep.Warnings, "cloud offering is enabled in Agent Orchestrator; Tallpine has no equivalent and it was not imported")
	}
	return nil
}

func applySettings(rep *Report) {
	for i := range rep.Settings {
		c := &rep.Settings[i]
		if c.Action != ActionUpdate || c.apply == nil {
			continue
		}
		if err := c.apply(); err != nil {
			c.Error = err.Error()
			continue
		}
		c.Applied = true
	}
}
