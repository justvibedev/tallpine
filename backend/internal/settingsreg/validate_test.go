package settingsreg

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
)

// Every declared default is a value its own setting accepts.
func TestEveryDefaultIsValid(t *testing.T) {
	for _, s := range Default().All() {
		if err := s.ValidateValue(s.Default); err != nil {
			t.Errorf("%s default %s: %v", s.Key, s.Default, err)
		}
	}
}

func TestValidateAcceptsAndRejectsByOwnerRules(t *testing.T) {
	claude := string(domain.HarnessClaudeCode)
	cases := []struct {
		key   Key
		value string
		ok    bool
	}{
		{"app.providerEventRetentionDays", "1", true},
		{"app.providerEventRetentionDays", "3650", true},
		{"app.providerEventRetentionDays", "0", false},
		{"app.providerEventRetentionDays", "3651", false},
		{"app.providerEventRetentionDays", `"7"`, false},
		{"app.providerEventRetentionDays", "1.5", false},
		{"app.chatHibernation.workerIdleSeconds", "60", true},
		{"app.chatHibernation.workerIdleSeconds", "59", false},
		{"app.chatHibernation.orchestratorIdleSeconds", "604801", false},
		{"app.defaultSessionMode", `"tui"`, true},
		{"app.defaultSessionMode", `"web"`, false},
		{"app.versionRefresh", "false", true},
		{"app.versionRefresh", `"off"`, false},
		{"app.harnessDefaults", `{"` + claude + `":{"autoCompactWindow":"1m","disabledModels":["a"]}}`, true},
		{"app.harnessDefaults", `{"nope":{}}`, false},
		{"app.harnessDefaults", `{"` + claude + `":{"autoCompactWindow":"7%"}}`, false},
		{"app.harnessDefaults", `[]`, false},
		{"app.cleanup.automatic", "false", true},
		{"app.cleanup.automatic", `"no"`, false},
		{"app.cleanup.worktrees", `{"retentionDays":3}`, true},
		{"app.cleanup.worktrees", `{"retentionDays":-1}`, false},
		{"app.cleanup.worktrees", `{"bogus":1}`, false},
		{"project.defaultBranch", `"main"`, true},
		{"project.defaultBranch", `1`, false},
		{"project.agentRules", strconv.Quote(strings.Repeat("x", domain.MaxAgentRulesBytes)), true},
		{"project.agentRules", strconv.Quote(strings.Repeat("x", domain.MaxAgentRulesBytes+1)), false},
		{"project.orchestratorRules", strconv.Quote(strings.Repeat("x", domain.MaxAgentRulesBytes+1)), false},
		{"project.agentConfig", `{"permissions":"auto"}`, true},
		{"project.agentConfig", `{"permissions":"yolo"}`, false},
		{"project.agentConfig", `{"permissionz":"auto"}`, false},
		{"project.worker", `{"agentConfig":{"modelz":"x"}}`, false},
		{"project.reviewers", `[{"harnessz":"claude-code"}]`, false},
		{"project.orchestrator.autoCompactWindow", `"1m"`, true},
		{"project.noCI", `true true`, false},
		{"project.agentConfig", `{"permissions":"auto"} {}`, false},
		{"project.orchestrator.autoCompactWindow", `"huge"`, false},
		{"project.env", `{"A":"b"}`, true},
		{"project.env", `{"A":1}`, false},
		{"project.defaultSessionMode", `"chat"`, true},
		{"project.defaultSessionMode", `"web"`, false},
		{"project.noCI", "true", true},
		{"project.worker", `null`, true},
		{"project.noCI", `null`, true},
		{"app.versionRefresh", `null`, false},
		{"app.providerEventRetentionDays", `null`, false},
		{"app.defaultSessionMode", `null`, false},
		{"app.harnessDefaults", `null`, false},
		{"app.cleanup.automatic", `null`, false},
		{"app.cleanup.worktrees", `null`, false},
		{"policy.permissionCap.max", `null`, false},
		{"policy.envFilter.passthrough", `null`, false},
		{"app.versionRefresh", ``, false},
		{"policy.permissionCap.max", `"auto"`, true},
		{"policy.permissionCap.max", `"nope"`, false},
		{"policy.envFilter.mode", `true`, false},
		{"policy.envFilter.passthrough", `["A"]`, true},
		{"policy.envFilter.passthrough", `[` + strings.TrimSuffix(strings.Repeat(`"A",`, 101), ",") + `]`, false},
	}
	for _, c := range cases {
		err := Default().Validate(c.key, json.RawMessage(c.value))
		if (err == nil) != c.ok {
			t.Errorf("Validate(%s, %s) = %v, want ok=%v", c.key, c.value, err, c.ok)
		}
		if err != nil && !errors.Is(err, ErrInvalidValue) {
			t.Errorf("Validate(%s) error %v is not ErrInvalidValue", c.key, err)
		}
	}
	if err := Default().Validate("app.nope", json.RawMessage("1")); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("unknown key err = %v", err)
	}
}

// memStore is a settings.Store that accepts every write.
type memStore struct{}

func (memStore) GetAppSettings(context.Context) (settingssvc.Snapshot, error) {
	return settingssvc.Snapshot{ChatHibernation: domain.DefaultChatHibernationPolicy(), ProviderEventRetentionDays: 14}, nil
}
func (memStore) SetDefaultSessionMode(context.Context, domain.SessionMode, time.Time) error {
	return nil
}
func (memStore) SetHarnessDefault(context.Context, domain.AgentHarness, domain.HarnessDefault, time.Time) error {
	return nil
}
func (memStore) SetVersionRefresh(context.Context, bool, time.Time) error { return nil }
func (memStore) SetStorageCompaction(context.Context, bool, int, time.Time) error {
	return nil
}
func (memStore) SetChatHibernation(context.Context, domain.ChatHibernationPolicy, time.Time) error {
	return nil
}

// Golden agreement: the registry accepts exactly the integers the settings
// service accepts, around and inside every bound.
func TestIntBoundsAgreeWithSettingsService(t *testing.T) {
	svc := settingssvc.New(memStore{}, nil, settingssvc.Offering{}, nil)
	ctx := context.Background()
	for _, n := range []int64{-1, 0, 1, 2, 59, 60, 61, 14, 3649, 3650, 3651, 604799, 604800, 604801} {
		raw := json.RawMessage(strconv.FormatInt(n, 10))
		days := int(n)
		_, svcErr := svc.SetStorageCompaction(ctx, nil, &days)
		if regErr := Default().Validate("app.providerEventRetentionDays", raw); (regErr == nil) != (svcErr == nil) {
			t.Errorf("retention %d: registry %v, service %v", n, regErr, svcErr)
		}
		idle := time.Duration(n) * time.Second
		_, svcErr = svc.SetChatHibernation(ctx, settingssvc.ChatHibernationUpdate{WorkerIdle: &idle})
		if regErr := Default().Validate("app.chatHibernation.workerIdleSeconds", raw); (regErr == nil) != (svcErr == nil) {
			t.Errorf("worker idle %d: registry %v, service %v", n, regErr, svcErr)
		}
	}
}

// A legacy oversized rules value may be replaced by one that does not grow,
// as the owners allow; growing past the cap is refused.
func TestValidateChangeAllowsShrinkingOversizedRules(t *testing.T) {
	big := func(n int) json.RawMessage { return json.RawMessage(strconv.Quote(strings.Repeat("x", n))) }
	limit := domain.MaxAgentRulesBytes
	for _, k := range []Key{"project.agentRules", "project.orchestratorRules"} {
		if err := Default().ValidateChange(k, big(limit+100), big(limit+50)); err != nil {
			t.Errorf("%s shrink: %v", k, err)
		}
		if err := Default().ValidateChange(k, big(limit+100), big(limit+100)); err != nil {
			t.Errorf("%s same size: %v", k, err)
		}
		if err := Default().ValidateChange(k, big(limit+100), big(limit+101)); err == nil {
			t.Errorf("%s grow past cap accepted", k)
		}
		if err := Default().ValidateChange(k, json.RawMessage("null"), big(limit+1)); err == nil {
			t.Errorf("%s over cap from unset accepted", k)
		}
		if err := Default().Validate(k, big(limit+1)); err == nil {
			t.Errorf("%s Validate over cap accepted", k)
		}
	}
	if err := Default().ValidateChange("app.nope", nil, json.RawMessage("1")); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("unknown key err = %v", err)
	}
}
