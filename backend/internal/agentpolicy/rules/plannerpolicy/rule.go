// Package plannerpolicy is the managed lock of the planner (B205): the planning
// mode and the planner's harness and model. Every field is empty by default,
// which locks nothing: the project's own setting (planningMode, planner) and the
// inherited orchestrator/worker roles decide. Nothing in this rule turns
// planning off by default; a managed policy file may set a field to pin it for
// every project.
package plannerpolicy

import (
	"encoding/json"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
)

// Name is the rule's key in settings documents and the managed policy file.
const Name = "planner"

// Field names.
const (
	FieldMode    = "mode"
	FieldHarness = "harness"
	FieldModel   = "model"
	// FieldAutoApprove: "off" locks ProjectConfig.PlanAutoApprove off in every
	// project; empty leaves it to the project.
	FieldAutoApprove = "autoApprove"
	// FieldInPlace: "off" locks ProjectConfig.PlanInPlace off in every project
	// (B335); empty leaves it to the project.
	FieldInPlace = "inPlace"
)

// AutoApproveOff is the one locking value of FieldAutoApprove.
const AutoApproveOff = "off"

// Rule implements agentpolicy.Rule.
type Rule struct{}

// Name implements agentpolicy.Rule.
func (Rule) Name() string { return Name }

// Schema implements agentpolicy.Rule.
func (Rule) Schema() agentpolicy.Schema {
	field := func(name string, kind agentpolicy.FieldKind) agentpolicy.Field {
		return agentpolicy.Field{
			Name:           name,
			Kind:           kind,
			Default:        json.RawMessage(`""`),
			LabelKey:       "settings.agentPolicy.planner." + name + ".label",
			DescriptionKey: "settings.agentPolicy.planner." + name + ".description",
		}
	}
	mode := field(FieldMode, agentpolicy.KindEnum)
	mode.ManagedOnly = true
	mode.Enum = []string{"", string(domain.PlanningOff), string(domain.PlanningAuto), string(domain.PlanningAlways)}
	mode.OptionLabelKeys = map[string]string{
		"":       "settings.agentPolicy.planner.mode.unlocked",
		"off":    "settings.agentPolicy.planner.mode.off",
		"auto":   "settings.agentPolicy.planner.mode.auto",
		"always": "settings.agentPolicy.planner.mode.always",
	}
	autoApprove := field(FieldAutoApprove, agentpolicy.KindEnum)
	autoApprove.Enum = []string{"", AutoApproveOff}
	autoApprove.ManagedOnly = true
	// An invalid managed value locks it off rather than leaving it open.
	autoApprove.OnInvalid = agentpolicy.InvalidStrictest
	autoApprove.OptionLabelKeys = map[string]string{
		"":             "settings.agentPolicy.planner.autoApprove.unlocked",
		AutoApproveOff: "settings.agentPolicy.planner.autoApprove.off",
	}
	inPlace := field(FieldInPlace, agentpolicy.KindEnum)
	inPlace.Enum = []string{"", AutoApproveOff}
	inPlace.OnInvalid = agentpolicy.InvalidStrictest
	inPlace.OptionLabelKeys = map[string]string{
		"":             "settings.agentPolicy.planner.inPlace.unlocked",
		AutoApproveOff: "settings.agentPolicy.planner.inPlace.off",
	}
	harness := field(FieldHarness, agentpolicy.KindString)
	harness.OptionSource = agentpolicy.SourceHarness
	model := field(FieldModel, agentpolicy.KindString)
	model.OptionSource = agentpolicy.SourceModel
	model.SourceField = FieldHarness
	return agentpolicy.Schema{
		TitleKey: "settings.agentPolicy.title",
		Fields: []agentpolicy.Field{
			mode,
			autoApprove,
			inPlace,
			harness,
			model,
		},
	}
}

// Validate implements agentpolicy.Rule.
func (Rule) Validate(s agentpolicy.Settings) error {
	if h := domain.AgentHarness(s.String(FieldHarness)); h != "" && (!h.IsKnown() || h.IsRemoved() || h.IsUnsupported()) {
		return fmt.Errorf("%s: %q is not a supported agent harness", FieldHarness, h)
	}
	return nil
}

// Decide implements agentpolicy.Rule.
func (Rule) Decide(_ agentpolicy.Input, s agentpolicy.Settings) agentpolicy.Outcome {
	return Outcome{
		Mode:              domain.PlanningMode(s.String(FieldMode)),
		Harness:           domain.AgentHarness(s.String(FieldHarness)),
		Model:             s.String(FieldModel),
		AutoApproveLocked: s.String(FieldAutoApprove) == AutoApproveOff,
		InPlaceLocked:     s.String(FieldInPlace) == AutoApproveOff,
	}
}

// Outcome is the locked planner settings. The zero Outcome locks nothing.
type Outcome struct {
	// Mode is the locked planning mode, empty when the project decides.
	Mode domain.PlanningMode
	// Harness and Model are the locked planner agent, empty when not locked.
	Harness domain.AgentHarness
	Model   string
	// AutoApproveLocked: plan auto-approval is locked off in every project.
	AutoApproveLocked bool
	// InPlaceLocked: plan-in-place is locked off in every project (B335).
	InPlaceLocked bool
}

// Record implements agentpolicy.Outcome; the settings are already in the
// decision record.
func (Outcome) Record() map[string]any { return nil }

// ResolvePlanningMode is the one planning-mode resolver (B525): a managed
// lock, else the project's own mode, else the app default, else auto. The
// backlog plan service and the session prompt both call it.
func ResolvePlanningMode(lock Outcome, cfg domain.ProjectConfig, d domain.WorkflowDefaults) domain.PlanningMode {
	if lock.Mode != "" {
		return lock.Mode
	}
	return cfg.EffectivePlanningMode(d).WithDefault()
}

// ModelLock returns the locked planner agent; ok is false when neither the
// harness nor the model is locked.
func (o Outcome) ModelLock() (domain.RoleOverride, bool) {
	if o.Harness == "" && o.Model == "" {
		return domain.RoleOverride{}, false
	}
	return domain.RoleOverride{Harness: o.Harness, AgentConfig: domain.AgentConfig{Model: o.Model}}, true
}

// Of returns the planner outcome of a decision.
func Of(d agentpolicy.Decision) Outcome {
	if out, ok := d.Outcome(Name); ok {
		if typed, ok := out.(Outcome); ok {
			return typed
		}
	}
	return Outcome{}
}
