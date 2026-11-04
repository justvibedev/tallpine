package agentpolicy

import "encoding/json"

// SchemaVersion is the version of the View shape served to clients.
const SchemaVersion = 2

// View is the metadata-driven description of the policy: everything a client
// needs to render and edit it without knowing any rule.
type View struct {
	SchemaVersion int        `json:"schemaVersion"`
	Project       string     `json:"project,omitempty"`
	Rules         []RuleView `json:"rules"`
	// ManagedPath is the managed policy file in effect, empty when none exists.
	ManagedPath string `json:"managedPath"`
	// Profile is the managed profile in force ("hardened"), empty when none.
	// Running sessions keep the decision they launched with; the profile
	// applies at their next relaunch or restore.
	Profile string `json:"profile,omitempty"`
	// SecurityMode is the effective Security mode switch for the scope.
	SecurityMode SecurityModeView `json:"securityMode"`
	// Warnings are problems with the managed file, a TALLPINE_AGENT_* variable or a
	// project override. Never contains values.
	Warnings []string `json:"warnings"`
}

// RuleView is one rule's section.
type RuleView struct {
	Name     string      `json:"name"`
	TitleKey string      `json:"titleKey"`
	Fields   []FieldView `json:"fields"`
}

// FieldView is one setting row.
type FieldView struct {
	Name            string            `json:"name"`
	Kind            FieldKind         `json:"kind"`
	Enum            []string          `json:"enum,omitempty"`
	LabelKey        string            `json:"labelKey"`
	DescriptionKey  string            `json:"descriptionKey"`
	OptionLabelKeys map[string]string `json:"optionLabelKeys,omitempty"`
	Default         json.RawMessage   `json:"default"`
	Value           json.RawMessage   `json:"value"`
	Source          Source            `json:"source"`
	Locked          bool              `json:"locked"`
	ProjectOverride ProjectOverride   `json:"projectOverride"`
	OptionSource    OptionSource      `json:"optionSource,omitempty"`
	SourceField     string            `json:"sourceField,omitempty"`
	VisibleWhen     *Condition        `json:"visibleWhen,omitempty"`
	Advanced        bool              `json:"advanced,omitempty"`
}

// Update is a partial change to one layer: rule -> field -> JSON value.
type Update map[string]map[string]json.RawMessage

// SecurityModeView is the resolved Security mode for a scope: whether it is on,
// which layer decided, and whether that layer locks it (managed, environment or
// the hardened profile).
type SecurityModeView struct {
	Enabled bool   `json:"enabled"`
	Source  Source `json:"source"`
	Locked  bool   `json:"locked"`
}
