// Package settingswrite changes settings by registry key (B202). A change is
// validated as a whole first (known key, scope, lock, the registry's owner
// validators against the current value), then applied with one write per
// owner: the project config, the app settings, the cleanup policy and the
// agent policy. Every write lands in the settings change log.
package settingswrite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/agentsettings"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	"tallpine.eu/tallpine/backend/internal/service/cleanup"
	projectsvc "tallpine.eu/tallpine/backend/internal/service/project"
	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
	"tallpine.eu/tallpine/backend/internal/service/settingsaudit"
	"tallpine.eu/tallpine/backend/internal/service/settingsview"
	"tallpine.eu/tallpine/backend/internal/settingsreg"
)

// Change is one request: values to set and keys to reset, for the app (empty
// Project) or one project.
type Change struct {
	Project domain.ProjectID
	Set     map[settingsreg.Key]json.RawMessage
	Unset   []settingsreg.Key
}

// Error codes.
const (
	CodeInvalid = "SETTINGS_INVALID"
	CodeLocked  = "SETTING_LOCKED"
	// CodeRetired refuses a setting that no longer exists and says where its
	// value lives now.
	CodeRetired = "SETTING_RETIRED"
	// CodeNotPermitted refuses an agent: a worker or reviewer, an
	// orchestrator outside its own project, a key only the user writes, or a
	// loosened agent policy.
	CodeNotPermitted = "AGENT_NOT_PERMITTED"
)

// Error refuses a change before anything is written. Keys maps each offending
// key to why.
type Error struct {
	Code    string
	Message string
	Keys    map[string]string
}

func (e *Error) Error() string { return e.Message }

// Owners written by the writer.
type (
	// Projects reads and replaces a project's config.
	Projects interface {
		Get(ctx context.Context, id domain.ProjectID) (projectsvc.GetResult, error)
		SetConfig(ctx context.Context, id domain.ProjectID, in projectsvc.SetConfigInput) (projectsvc.Project, error)
	}
	// AppSettings changes app_settings.
	AppSettings interface {
		Get(ctx context.Context) (settingssvc.Snapshot, error)
		SetDefaultSessionMode(ctx context.Context, mode domain.SessionMode) (settingssvc.Snapshot, error)
		SetHarnessDefault(ctx context.Context, agent domain.AgentHarness, value domain.HarnessDefault) (settingssvc.Snapshot, error)
		SetChatHibernation(ctx context.Context, update settingssvc.ChatHibernationUpdate) (settingssvc.Snapshot, error)
		SetVersionRefresh(ctx context.Context, enabled bool) (settingssvc.Snapshot, error)
		SetStorageCompaction(ctx context.Context, enabled *bool, retentionDays *int) (settingssvc.Snapshot, error)
	}
	// CleanupPolicy changes the cleanup policy.
	CleanupPolicy interface {
		Status(ctx context.Context) (cleanup.Status, error)
		UpdatePolicy(ctx context.Context, patch []byte) (cleanup.Policy, error)
	}
	// AgentPolicy changes the user or a project layer of the agent policy, and
	// decides the agentSettings rule for agent writes.
	AgentPolicy interface {
		Describe(ctx context.Context, project string) agentpolicy.View
		Update(ctx context.Context, project string, update agentpolicy.Update, now time.Time) error
		Decide(ctx context.Context, session agentpolicy.SessionRef) agentpolicy.Decision
	}
)

// Writer applies changes. Nil owners refuse their keys.
type Writer struct {
	Registry      *settingsreg.Registry
	View          *settingsview.Service
	Projects      Projects
	AppSettings   AppSettings
	CleanupPolicy CleanupPolicy
	AgentPolicy   AgentPolicy
	Audit         *settingsaudit.Recorder
	Now           func() time.Time
}

// planned is one validated key: its new value (nil to clear a project field).
type planned struct {
	def   settingsreg.Setting
	value json.RawMessage
}

// Apply validates ch as a whole and then writes it.
func (w *Writer) Apply(ctx context.Context, ch Change) error {
	plan, err := w.validate(ctx, ch)
	if err != nil {
		return err
	}
	byGroup := map[string][]planned{}
	for _, p := range plan {
		byGroup[group(p.def.Key)] = append(byGroup[group(p.def.Key)], p)
	}
	for _, g := range []string{"policy", "app", "cleanup", "project"} {
		if len(byGroup[g]) == 0 {
			continue
		}
		var err error
		switch g {
		case "policy":
			err = w.applyPolicy(ctx, string(ch.Project), byGroup[g])
		case "app":
			err = w.applyApp(ctx, byGroup[g])
		case "cleanup":
			err = w.applyCleanup(ctx, byGroup[g])
		case "project":
			err = w.applyProject(ctx, ch.Project, byGroup[g])
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func group(k settingsreg.Key) string {
	switch {
	case strings.HasPrefix(string(k), "policy."):
		return "policy"
	case strings.HasPrefix(string(k), "app.cleanup."):
		return "cleanup"
	case strings.HasPrefix(string(k), "app."):
		return "app"
	default:
		return "project"
	}
}

func (w *Writer) registry() *settingsreg.Registry {
	if w.Registry != nil {
		return w.Registry
	}
	return settingsreg.Default()
}

// validate checks every key before anything is written.
func (w *Writer) validate(ctx context.Context, ch Change) ([]planned, error) {
	if len(ch.Set) == 0 && len(ch.Unset) == 0 {
		return nil, &Error{Code: CodeInvalid, Message: "set or unset at least one setting"}
	}
	retired := map[string]string{}
	for _, k := range append(slices.Clone(ch.Unset), mapKeys(ch.Set)...) {
		if why, ok := retiredSettings[k]; ok {
			retired[string(k)] = why
		}
	}
	if len(retired) > 0 {
		return nil, &Error{Code: CodeRetired, Message: "retired settings: " + joinKeys(retired), Keys: retired}
	}
	agent, isAgent := requestscope.AgentFrom(ctx)
	var agentRule agentsettings.Outcome
	if isAgent {
		if err := agentMayChange(agent, ch.Project); err != nil {
			return nil, err
		}
		// No engine means no organization switch to consult: refuse rather
		// than let agents write unchecked.
		if w.AgentPolicy == nil {
			return nil, &Error{Code: CodeNotPermitted, Message: "agent setting changes need the agent policy, which this daemon lacks"}
		}
		agentRule = agentsettings.Of(w.AgentPolicy.Decide(ctx, agentpolicy.SessionRef{Project: ch.Project, Kind: domain.KindOrchestrator, ID: agent.SessionID}))
	}
	var current []settingsview.Value
	var err error
	if ch.Project == "" {
		current, err = w.View.App(ctx)
	} else {
		current, err = w.View.Project(ctx, ch.Project)
	}
	if err != nil {
		return nil, err
	}
	now := map[settingsreg.Key]settingsview.Value{}
	for _, v := range current {
		now[v.Key] = v
	}
	invalid, locked, denied := map[string]string{}, map[string]string{}, map[string]string{}
	var plan []planned
	check := func(k settingsreg.Key, value json.RawMessage, unset bool) {
		def, ok := w.registry().Lookup(k)
		if !ok {
			invalid[string(k)] = "unknown setting"
			return
		}
		if inScope := def.Scope == settingsreg.ScopePolicy || (ch.Project == "") == (def.Scope == settingsreg.ScopeApp); !inScope {
			if ch.Project == "" {
				invalid[string(k)] = "is a project setting; give a project"
			} else {
				invalid[string(k)] = "is an app-wide setting; omit the project"
			}
			return
		}
		cur, known := now[k]
		if !known || !w.owns(def) {
			invalid[string(k)] = "cannot be changed here"
			return
		}
		if cur.Locked {
			locked[string(k)] = "locked by " + cur.LockedBy
			return
		}
		if k == "app.mobile.enabled" {
			invalid[string(k)] = "is set by your organization's managed config only; use Settings > Mobile"
			return
		}
		if unset {
			switch {
			case def.Scope == settingsreg.ScopePolicy:
				value = json.RawMessage("null") // drops the stored value: the layer inherits
			case def.Nullable:
				value = nil
			default:
				value = def.Default
			}
		} else if err := w.registry().ValidateChange(k, cur.Value, value); err != nil {
			invalid[string(k)] = strings.TrimPrefix(err.Error(), settingsreg.ErrInvalidValue.Error()+": ")
			return
		}
		if isAgent {
			if why := agentRule.Refusal(string(k)); why != "" && !agent.DevMode {
				denied[string(k)] = why
				return
			}
			if why := agentMayWrite(agent, def, cur.Value, value); why != "" {
				denied[string(k)] = why
				return
			}
		}
		plan = append(plan, planned{def: def, value: value})
	}
	for _, k := range sortedKeys(ch.Set) {
		if slices.Contains(ch.Unset, k) {
			invalid[string(k)] = "is both set and unset"
			continue
		}
		check(k, ch.Set[k], false)
	}
	for _, k := range ch.Unset {
		check(k, nil, true)
	}
	if len(locked) > 0 {
		return nil, &Error{Code: CodeLocked, Message: "managed config or the environment locks " + joinKeys(locked), Keys: locked}
	}
	if len(denied) > 0 {
		return nil, &Error{Code: CodeNotPermitted, Message: "agents may not change " + joinKeys(denied), Keys: denied}
	}
	if len(invalid) > 0 {
		return nil, &Error{Code: CodeInvalid, Message: "invalid settings: " + joinKeys(invalid), Keys: invalid}
	}
	return plan, nil
}

// owns reports whether the writer has the owner of def.
func (w *Writer) owns(def settingsreg.Setting) bool {
	switch group(def.Key) {
	case "policy":
		return w.AgentPolicy != nil
	case "cleanup":
		return w.CleanupPolicy != nil
	case "app":
		return w.AppSettings != nil
	default:
		return w.Projects != nil
	}
}

func (w *Writer) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *Writer) applyPolicy(ctx context.Context, project string, plan []planned) error {
	update := agentpolicy.Update{}
	for _, p := range plan {
		rule, field, _ := strings.Cut(strings.TrimPrefix(string(p.def.Key), "policy."), ".")
		if update[rule] == nil {
			update[rule] = map[string]json.RawMessage{}
		}
		update[rule][field] = p.value
	}
	layer := agentpolicy.SourceUser
	if project != "" {
		layer = agentpolicy.SourceProject
	}
	before := settingsaudit.PolicyLayer(w.AgentPolicy.Describe(ctx, project), layer)
	err := w.AgentPolicy.Update(ctx, project, update, w.now())
	var lockedErr *agentpolicy.LockedError
	var invalid *agentpolicy.ValidationError
	switch {
	case errors.As(err, &lockedErr):
		return &Error{Code: CodeLocked, Message: "managed config locks " + strings.Join(lockedErr.Fields, ", ")}
	case errors.As(err, &invalid):
		return &Error{Code: CodeInvalid, Message: invalid.Message}
	case err != nil:
		return err
	}
	w.Audit.Record(ctx, domain.ProjectID(project), before, settingsaudit.PolicyLayer(w.AgentPolicy.Describe(ctx, project), layer))
	return nil
}

func (w *Writer) applyApp(ctx context.Context, plan []planned) error {
	before, err := w.AppSettings.Get(ctx)
	if err != nil {
		return err
	}
	var hibernation settingssvc.ChatHibernationUpdate
	var compactionOn *bool
	var retention *int
	setHibernation, setCompaction := false, false
	for _, p := range plan {
		raw := p.value
		switch p.def.Key {
		case "app.defaultSessionMode":
			var mode string
			_ = json.Unmarshal(raw, &mode)
			if _, err := w.AppSettings.SetDefaultSessionMode(ctx, domain.SessionMode(mode)); err != nil {
				return appError(err)
			}
		case "app.dynamicOrchestratorContext":
			if err := w.setDynamicContext(ctx, raw); err != nil {
				return err
			}
		case "app.workflowDefaults.autoReview", "app.workflowDefaults.planningMode", "app.workflowDefaults.planAutoApprove":
			if err := w.setWorkflowDefault(ctx, p.def.Key, raw); err != nil {
				return err
			}
		case "app.versionRefresh":
			var on bool
			_ = json.Unmarshal(raw, &on)
			if _, err := w.AppSettings.SetVersionRefresh(ctx, on); err != nil {
				return appError(err)
			}
		case "app.harnessDefaults":
			var next map[string]domain.HarnessDefault
			_ = json.Unmarshal(raw, &next)
			for _, h := range sortedNames(next, before.HarnessDefaults) {
				if _, err := w.AppSettings.SetHarnessDefault(ctx, domain.AgentHarness(h), next[h]); err != nil {
					return appError(err)
				}
			}
		case "app.chatHibernation.enabled":
			var on bool
			_ = json.Unmarshal(raw, &on)
			hibernation.Enabled, setHibernation = &on, true
		case "app.chatHibernation.workerIdleSeconds", "app.chatHibernation.orchestratorIdleSeconds":
			var seconds int64
			_ = json.Unmarshal(raw, &seconds)
			d := time.Duration(seconds) * time.Second
			if p.def.Key == "app.chatHibernation.workerIdleSeconds" {
				hibernation.WorkerIdle = &d
			} else {
				hibernation.OrchestratorIdle = &d
			}
			setHibernation = true
		case "app.storageCompaction":
			var on bool
			_ = json.Unmarshal(raw, &on)
			compactionOn, setCompaction = &on, true
		case "app.providerEventRetentionDays":
			var days int
			_ = json.Unmarshal(raw, &days)
			retention, setCompaction = &days, true
		default:
			return fmt.Errorf("settingswrite: no writer for %s", p.def.Key)
		}
	}
	if setHibernation {
		if _, err := w.AppSettings.SetChatHibernation(ctx, hibernation); err != nil {
			return appError(err)
		}
	}
	if setCompaction {
		if _, err := w.AppSettings.SetStorageCompaction(ctx, compactionOn, retention); err != nil {
			return appError(err)
		}
	}
	after, err := w.AppSettings.Get(ctx)
	if err != nil {
		return err
	}
	w.Audit.Record(ctx, "", settingsaudit.AppSettings(before), settingsaudit.AppSettings(after))
	return nil
}

// appError turns an owner's validation sentinel into a refusal.
func appError(err error) error {
	for _, sentinel := range []error{settingssvc.ErrInvalidChatHibernation, settingssvc.ErrInvalidStorageCompaction,
		settingssvc.ErrInvalidHarnessDefault, settingssvc.ErrUnknownHarness} {
		if errors.Is(err, sentinel) {
			return &Error{Code: CodeInvalid, Message: err.Error()}
		}
	}
	return err
}

func (w *Writer) applyCleanup(ctx context.Context, plan []planned) error {
	status, err := w.CleanupPolicy.Status(ctx)
	if err != nil {
		return err
	}
	patch := map[string]json.RawMessage{}
	for _, p := range plan {
		patch[strings.TrimPrefix(string(p.def.Key), "app.cleanup.")] = p.value
	}
	doc, _ := json.Marshal(patch)
	policy, err := w.CleanupPolicy.UpdatePolicy(ctx, doc)
	if errors.Is(err, cleanup.ErrInvalidPolicy) {
		return &Error{Code: CodeInvalid, Message: err.Error()}
	}
	if err != nil {
		return err
	}
	w.Audit.Record(ctx, "", settingsaudit.JSONObject("app.cleanup.", status.Policy), settingsaudit.JSONObject("app.cleanup.", policy))
	return nil
}

// applyProject patches the stored config by key (dotted keys are nested) and
// replaces it through the project service, which validates and logs it.
func (w *Writer) applyProject(ctx context.Context, id domain.ProjectID, plan []planned) error {
	res, err := w.Projects.Get(ctx, id)
	if err != nil {
		return err
	}
	var cfg domain.ProjectConfig
	if res.Project != nil && res.Project.Config != nil {
		cfg = *res.Project.Config
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return err
	}
	for _, p := range plan {
		path := strings.Split(strings.TrimPrefix(string(p.def.Key), "project."), ".")
		var value any
		if p.value != nil {
			if err := json.Unmarshal(p.value, &value); err != nil {
				return err
			}
		}
		setPath(doc, path, value, p.value == nil)
	}
	patched, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(patched))
	dec.DisallowUnknownFields()
	var next domain.ProjectConfig
	if err := dec.Decode(&next); err != nil {
		return &Error{Code: CodeInvalid, Message: err.Error()}
	}
	_, err = w.Projects.SetConfig(ctx, id, projectsvc.SetConfigInput{Config: next})
	return err
}

// setPath sets or deletes a nested field, creating parent objects.
func setPath(doc map[string]any, path []string, value any, remove bool) {
	for _, name := range path[:len(path)-1] {
		child, ok := doc[name].(map[string]any)
		if !ok {
			child = map[string]any{}
			doc[name] = child
		}
		doc = child
	}
	last := path[len(path)-1]
	if remove {
		delete(doc, last)
		return
	}
	doc[last] = value
}

func sortedKeys(m map[settingsreg.Key]json.RawMessage) []settingsreg.Key {
	out := make([]settingsreg.Key, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// sortedNames lists the harnesses of both maps, so a harness dropped from the
// new value is reset.
func sortedNames(a, b map[string]domain.HarnessDefault) []string {
	seen := map[string]bool{}
	for k := range a {
		seen[k] = true
	}
	for k := range b {
		seen[k] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func joinKeys(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

// retiredSettings are keys that no longer exist, with where their value lives
// now. A write names the new home instead of failing as an unknown key.
var retiredSettings = map[settingsreg.Key]string{
	"app.reviewDefaults": "retired; review settings are per project: set project.autoReview, project.terminateOnPrMerge and project.reviewRisk",
}

func mapKeys(m map[settingsreg.Key]json.RawMessage) []settingsreg.Key {
	out := make([]settingsreg.Key, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
