package agentpolicy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"
)

// managedTTL bounds how stale the managed file read may be, so an MDM push
// applies to new sessions without a daemon restart.
const managedTTL = 30 * time.Second

// Engine resolves the layered settings and decides per session. Build one per
// daemon with New; it is safe for concurrent use.
type Engine struct {
	rules   []Rule
	byName  map[string]Rule
	user    UserStore
	lookup  func(string) (string, bool)
	path    string
	convert func(string) ([]byte, error)
	now     func() time.Time

	mu       sync.Mutex
	cached   managedResult
	cachedAt time.Time
}

// Option configures an Engine; the defaults are the production sources.
type Option func(*Engine)

// WithManagedFile overrides the managed file location and reader (tests).
func WithManagedFile(path string, convert func(string) ([]byte, error)) Option {
	return func(e *Engine) { e.path, e.convert = path, convert }
}

// WithEnv overrides the environment lookup (tests).
func WithEnv(lookup func(string) (string, bool)) Option {
	return func(e *Engine) { e.lookup = lookup }
}

// WithClock overrides the clock used for the managed file cache (tests).
func WithClock(now func() time.Time) Option { return func(e *Engine) { e.now = now } }

// New builds an Engine over rules. user may be nil (empty user layer). It fails
// on a rule whose schema cannot work, so a bad rule stops at startup.
func New(rules []Rule, user UserStore, opts ...Option) (*Engine, error) {
	e := &Engine{
		rules: rules, byName: map[string]Rule{}, user: user,
		lookup: os.LookupEnv, path: managedPath(), convert: loadManagedFile, now: time.Now,
	}
	for _, r := range rules {
		if _, dup := e.byName[r.Name()]; dup {
			return nil, fmt.Errorf("agent policy: rule %q registered twice", r.Name())
		}
		if err := checkSchema(r.Name(), r.Schema()); err != nil {
			return nil, err
		}
		e.byName[r.Name()] = r
	}
	for _, opt := range opts {
		opt(e)
	}
	return e, nil
}

func (e *Engine) managed() managedResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.cachedAt.IsZero() && e.now().Sub(e.cachedAt) < managedTTL {
		return e.cached
	}
	e.cached = readManaged(e.rules, e.path, e.convert)
	e.cachedAt = e.now()
	return e.cached
}

func (e *Engine) userDoc(ctx context.Context) userDoc {
	if e.user == nil {
		return newUserDoc()
	}
	data, err := e.user.AgentPolicy(ctx)
	if err != nil {
		return newUserDoc() // managed and env layers still apply
	}
	return parseUserDoc(e.rules, data)
}

// resolved is the merged state of every rule for one scope.
type resolved struct {
	states      map[string]ruleState
	warnings    []string
	managedPath string
	profile     string
}

func (e *Engine) resolve(ctx context.Context, project string) resolved {
	doc := e.userDoc(ctx)
	user := doc.layerFor(e.rules, "")
	var proj layer
	if project != "" {
		proj = doc.layerFor(e.rules, project)
	}
	env := readEnv(e.rules, e.lookup)
	managed := e.managed()
	out := resolved{states: map[string]ruleState{}, managedPath: managed.path, profile: managed.layer.profile}
	out.warnings = append(out.warnings, env.warnings...)
	out.warnings = append(out.warnings, managed.layer.warnings...)
	for _, r := range e.rules {
		state, warnings := merge(r, user, proj, env, managed.layer)
		if err := r.Validate(state.settings); err != nil {
			out.warnings = append(out.warnings, fmt.Sprintf("%s settings are inconsistent (%v); the user and project values are dropped", r.Name(), err))
			state = fallbackState(r, env, managed.layer)
		}
		out.states[r.Name()] = state
		out.warnings = append(out.warnings, warnings...)
	}
	applySecurityMode(e.rules, &out)
	return out
}

// Decide computes the immutable decision for a session. Call it once per
// spawn, relaunch or restore and pass the result to the seams.
func (e *Engine) Decide(ctx context.Context, session SessionRef) Decision {
	res := e.resolve(ctx, string(session.Project))
	data := &decisionData{
		session:  session,
		profile:  res.profile,
		outcomes: map[string]Outcome{},
		fields:   map[string]map[string]FieldState{},
		warnings: res.warnings,
	}
	devMode := res.states[DevModeRule].settings.Bool("enabled")
	for _, r := range e.rules {
		state := res.states[r.Name()]
		data.outcomes[r.Name()] = r.Decide(Input{Session: session, Profile: res.profile, DevMode: devMode}, state.settings.clone())
		data.fields[r.Name()] = state.fields
	}
	project := string(session.Project)
	data.live = func(rule, field string) (json.RawMessage, bool) {
		st, ok := e.resolve(context.Background(), project).states[rule].fields[field]
		return st.Value, ok
	}
	return Decision{d: data}
}

// ManagedProfile is the managed profile currently in force ("" for none).
func (e *Engine) ManagedProfile() string { return e.managed().layer.profile }

// DecideStatic is Decide for code that runs outside any session (validating a
// remembered project setting, shaping a capability error). It uses the global
// layers only.
func (e *Engine) DecideStatic(ctx context.Context) Decision { return e.Decide(ctx, SessionRef{}) }

// Describe returns the metadata-driven view of a scope; project=="" is global.
func (e *Engine) Describe(ctx context.Context, project string) View {
	res := e.resolve(ctx, project)
	view := View{SchemaVersion: SchemaVersion, Project: project, ManagedPath: res.managedPath, Profile: res.profile, Warnings: res.warnings, Rules: []RuleView{}, SecurityMode: securityModeView(res)}
	if view.Warnings == nil {
		view.Warnings = []string{}
	}
	for _, r := range e.rules {
		schema := r.Schema()
		rv := RuleView{Name: r.Name(), TitleKey: schema.TitleKey, Fields: []FieldView{}}
		for _, f := range schema.Fields {
			st := res.states[r.Name()].fields[f.Name]
			override := f.ProjectOverride
			if override == "" {
				override = ProjectNever
			}
			rv.Fields = append(rv.Fields, FieldView{
				Name: f.Name, Kind: f.Kind, Enum: f.Enum, LabelKey: f.LabelKey, DescriptionKey: f.DescriptionKey, OptionLabelKeys: f.OptionLabelKeys,
				Default: f.Default, Value: st.Value, Source: st.Source, Locked: st.Locked,
				ProjectOverride: override, OptionSource: f.OptionSource, SourceField: f.SourceField, VisibleWhen: f.VisibleWhen, Advanced: f.Advanced,
			})
		}
		view.Rules = append(view.Rules, rv)
	}
	return view
}

// Update merges a partial change into the user layer (project=="") or one
// project's override, refusing locked fields, invalid values and project
// overrides that would loosen the user's setting.
func (e *Engine) Update(ctx context.Context, project string, update Update, now time.Time) error {
	if e.user == nil {
		return &ValidationError{Message: "agent policy storage is not available"}
	}
	if len(update) == 0 {
		return &ValidationError{Message: "provide at least one policy field"}
	}
	current := e.resolve(ctx, project)
	global := e.resolve(ctx, "")
	doc := e.userDoc(ctx)
	target := doc.Rules
	if project != "" {
		if doc.Projects[project] == nil {
			doc.Projects[project] = map[string]Settings{}
		}
		target = doc.Projects[project]
	}
	var locked []string
	for _, ruleName := range sortedKeys(update) {
		rule, ok := e.byName[ruleName]
		if !ok {
			return &ValidationError{Message: fmt.Sprintf("unknown rule %q", ruleName)}
		}
		for _, field := range sortedKeys(update[ruleName]) {
			f, ok := rule.Schema().field(field)
			if !ok {
				return &ValidationError{Message: fmt.Sprintf("unknown setting %s.%s", ruleName, field)}
			}
			if bytes.Equal(bytes.TrimSpace(update[ruleName][field]), []byte("null")) {
				// null resets the field in this layer to inherit (settings unset).
				delete(target[ruleName], field)
				continue
			}
			if f.ManagedOnly {
				return &ValidationError{Message: fmt.Sprintf("%s.%s is set by the managed policy only; use the app defaults on the Reviews tab", ruleName, field)}
			}
			if project == "" && ruleName == SecurityModeRule && field == SecurityModeAllowDocker {
				return &ValidationError{Message: "securityMode.allowDocker is set per project; it cannot be set install-wide"}
			}
			value, err := f.Parse(update[ruleName][field])
			if err != nil {
				return &ValidationError{Message: err.Error()}
			}
			cur := current.states[ruleName].fields[field]
			if cur.Locked && string(cur.Value) != string(value) {
				locked = append(locked, ruleName+"."+field)
				continue
			}
			if project != "" {
				if f.ProjectOverride != ProjectAny && f.ProjectOverride != ProjectTightenOnly {
					return &ValidationError{Message: fmt.Sprintf("%s.%s cannot be overridden per project", ruleName, field)}
				}
				if f.ProjectOverride == ProjectTightenOnly && f.strictness(value) < f.strictness(global.states[ruleName].fields[field].Value) {
					return &ValidationError{Message: fmt.Sprintf("%s.%s: %v", ruleName, field, ErrLoosens)}
				}
			}
			if target[ruleName] == nil {
				target[ruleName] = Settings{}
			}
			target[ruleName][field] = value
		}
	}
	if len(locked) > 0 {
		return &LockedError{Fields: locked}
	}
	// Validated against the real env and managed layers too: a value that is fine
	// alone but inconsistent with an administrator's setting must not be stored.
	envLayer, managedLayer := readEnv(e.rules, e.lookup), e.managed().layer
	for _, r := range e.rules {
		var projectLayer layer
		if project != "" {
			projectLayer = doc.layerFor(e.rules, project)
		}
		merged, _ := merge(r, doc.layerFor(e.rules, ""), projectLayer, envLayer, managedLayer)
		if err := r.Validate(merged.settings); err != nil {
			return &ValidationError{Message: fmt.Sprintf("%s: %v", r.Name(), err)}
		}
	}
	if project == "" && touchesModeSwitch(update) && current.profile != ProfileHardened {
		if on := func(rule string) bool {
			r, ok := e.byName[rule]
			if !ok {
				return false
			}
			m, _ := merge(r, doc.layerFor(e.rules, ""), layer{}, envLayer, managedLayer)
			return boolValue(m.fields["enabled"].Value)
		}; on(SecurityModeRule) && on(DevModeRule) {
			return ErrModeConflict
		}
	}
	data, err := doc.marshal()
	if err != nil {
		return err
	}
	return e.user.SetAgentPolicy(ctx, data, now)
}

// touchesModeSwitch reports whether an update writes securityMode.enabled or
// devMode.enabled, the only writes the exclusivity check applies to.
func touchesModeSwitch(u Update) bool {
	_, sm := u[SecurityModeRule][SecurityModeEnabled]
	_, dm := u[DevModeRule]["enabled"]
	return sm || dm
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// fallbackState is the rule's state when the merged settings fail Validate. The
// administrator's managed and environment values must survive (dropping them
// would silently void a lock), so only the user and project layers are dropped.
// If those alone are still inconsistent, a setting that declares a strictest
// value takes it and the rest take their defaults, so the result is never
// looser than what was asked.
func fallbackState(r Rule, env, managed layer) ruleState {
	state, _ := merge(r, layer{}, layer{}, env, managed)
	if r.Validate(state.settings) == nil {
		return state
	}
	for _, f := range r.Schema().Fields {
		cur := state.fields[f.Name]
		if cur.Source != SourceManaged && cur.Source != SourceManagedProfile && cur.Source != SourceEnv && f.OnInvalid == InvalidStrictest {
			cur = FieldState{Value: f.strictest(), Source: SourceDefault}
		}
		state.fields[f.Name] = cur
		state.settings[f.Name] = cur.Value
	}
	return state
}
