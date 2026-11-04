package officialimport

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type fakeSettings struct {
	cur   TargetSettings
	calls []string
}

func (s *fakeSettings) Current(context.Context) (TargetSettings, error) { return s.cur, nil }

func (s *fakeSettings) SetDefaultSessionMode(_ context.Context, m domain.SessionMode) error {
	s.calls = append(s.calls, "mode="+string(m))
	s.cur.DefaultSessionMode = m
	return nil
}

func (s *fakeSettings) SetVersionRefresh(_ context.Context, on bool) error {
	s.calls = append(s.calls, "refresh")
	s.cur.VersionRefresh = on
	return nil
}

func (s *fakeSettings) SetHarnessDefault(_ context.Context, h domain.AgentHarness, v domain.HarnessDefault) error {
	s.calls = append(s.calls, "harness="+string(h))
	if s.cur.HarnessDefaults == nil {
		s.cur.HarnessDefaults = map[string]domain.HarnessDefault{}
	}
	s.cur.HarnessDefaults[string(h)] = v
	return nil
}

func settingsFixture(t *testing.T) *fixture {
	f := newFixture(t)
	f.exec(`UPDATE app_settings SET default_session_mode = 'chat', version_refresh = 0,
		harness_defaults = '{"claude-code":{"disabledModels":["claude-opus-4-6"]},"gemini":{"disabledModels":["x"]}}',
		cloud_offering = 1 WHERE id = 1`)
	return f
}

func TestSettingsPlannedButNotAppliedWithoutOptIn(t *testing.T) {
	f := settingsFixture(t)
	st := &fakeSettings{cur: TargetSettings{DefaultSessionMode: domain.SessionModeTUI, VersionRefresh: true}}

	rep, err := RunWithSettings(context.Background(), newTarget(), st, f.opts())
	if err != nil {
		t.Fatal(err)
	}
	if len(st.calls) != 0 {
		t.Fatalf("settings applied without opt-in: %v", st.calls)
	}
	keys := map[string]SettingChange{}
	for _, c := range rep.Settings {
		keys[c.Key] = c
	}
	if keys["defaultSessionMode"].Action != ActionUpdate || keys["defaultSessionMode"].Value != "chat" ||
		keys["versionRefresh"].Action != ActionUpdate || keys["harnessDefaults.claude-code"].Action != ActionUpdate {
		t.Fatalf("settings = %+v", rep.Settings)
	}
	if _, ok := keys["harnessDefaults.gemini"]; ok {
		t.Error("settings for a removed harness must not be imported")
	}
	if !containsSub(rep.Warnings, `"gemini"`) || !containsSub(rep.Warnings, "cloud offering") {
		t.Errorf("warnings = %v", rep.Warnings)
	}
}

func TestSettingsAppliedAndIdempotent(t *testing.T) {
	f := settingsFixture(t)
	st := &fakeSettings{cur: TargetSettings{DefaultSessionMode: domain.SessionModeTUI, VersionRefresh: true}}
	opts := f.opts()
	opts.IncludeSettings = true

	if _, err := RunWithSettings(context.Background(), newTarget(), st, opts); err != nil {
		t.Fatal(err)
	}
	if st.cur.DefaultSessionMode != domain.SessionModeChat || st.cur.VersionRefresh ||
		len(st.cur.HarnessDefaults["claude-code"].DisabledModels) != 1 {
		t.Fatalf("target = %+v", st.cur)
	}
	n := len(st.calls)
	rep, err := RunWithSettings(context.Background(), newTarget(), st, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.calls) != n {
		t.Fatalf("rerun rewrote settings: %v", st.calls[n:])
	}
	for _, c := range rep.Settings {
		if c.Action != ActionUnchanged {
			t.Errorf("%s = %s, want unchanged", c.Key, c.Action)
		}
	}
}

func TestSettingsDryRunWritesNothing(t *testing.T) {
	f := settingsFixture(t)
	st := &fakeSettings{cur: TargetSettings{DefaultSessionMode: domain.SessionModeTUI}}
	opts := f.opts()
	opts.IncludeSettings, opts.DryRun = true, true
	if _, err := RunWithSettings(context.Background(), newTarget(), st, opts); err != nil {
		t.Fatal(err)
	}
	if len(st.calls) != 0 {
		t.Fatalf("dry run applied settings: %v", st.calls)
	}
}
