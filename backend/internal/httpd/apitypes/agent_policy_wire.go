package apitypes

// AgentPolicyCondition shows a row only while another field of the rule has a value.
type AgentPolicyCondition struct {
	Field  string `json:"field"`
	Equals string `json:"equals"`
}

// AgentPolicyField is one setting row. Value and Default are JSON values of the
// field's kind (a string for enum, true/false, a list of strings, a number).
type AgentPolicyField struct {
	Name           string   `json:"name"`
	Kind           string   `json:"kind" enum:"enum,bool,stringList,int,string"`
	Enum           []string `json:"enum,omitempty"`
	LabelKey       string   `json:"labelKey"`
	DescriptionKey string   `json:"descriptionKey"`
	// OptionLabelKeys maps each enum value to the i18n key of its label.
	OptionLabelKeys map[string]string `json:"optionLabelKeys,omitempty"`
	Default         any               `json:"default"`
	Value           any               `json:"value"`
	Source          string            `json:"source" enum:"default,user,project,env,managed,managed_profile,security_mode"`
	Locked          bool              `json:"locked"`
	ProjectOverride string            `json:"projectOverride" enum:"any,tightenOnly,never"`
	// OptionSource makes a string field a harness or model picker; SourceField
	// names the harness field a model field depends on.
	OptionSource string                `json:"optionSource,omitempty" enum:"harness,model"`
	SourceField  string                `json:"sourceField,omitempty"`
	VisibleWhen  *AgentPolicyCondition `json:"visibleWhen,omitempty"`
	Advanced     bool                  `json:"advanced,omitempty"`
}

// AgentPolicyResponse describes the agent policy of one scope (global, or one
// project's effective view) for a client to render and edit without knowing any
// rule: sections and rows come from rule metadata.
type AgentPolicyResponse struct {
	SchemaVersion int `json:"schemaVersion"`
	// Project is set on a per-project view.
	Project     string            `json:"project,omitempty"`
	Rules       []AgentPolicyRule `json:"rules"`
	ManagedPath string            `json:"managedPath"`
	// Profile is the managed profile in force ("hardened"), absent when none.
	// It applies to running sessions at their next relaunch or restore.
	Profile string `json:"profile,omitempty" enum:"hardened"`
	// SecurityMode is the effective Security mode switch for this scope.
	SecurityMode AgentPolicySecurityMode `json:"securityMode"`
	Warnings     []string                `json:"warnings"`
}

// AgentPolicySecurityMode is the resolved Security mode: on or off, the layer
// that decided it, and whether that layer locks it (managed, env or profile).
type AgentPolicySecurityMode struct {
	Enabled bool   `json:"enabled"`
	Source  string `json:"source"`
	Locked  bool   `json:"locked"`
}

// AgentPolicyRule is one rule's section.
type AgentPolicyRule struct {
	Name     string             `json:"name"`
	TitleKey string             `json:"titleKey"`
	Fields   []AgentPolicyField `json:"fields"`
}
