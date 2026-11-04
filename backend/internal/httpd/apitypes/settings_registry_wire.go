package apitypes

// SettingValue is the effective state of one setting.
type SettingValue struct {
	Key      string   `json:"key"`
	Value    any      `json:"value"`
	Source   string   `json:"source" enum:"default,user,project,env,managed,managed_profile,security_mode"`
	Locked   bool     `json:"locked"`
	LockedBy string   `json:"lockedBy,omitempty" description:"\"managed\", \"env\", or the environment variable that locks the value."`
	Writable []string `json:"writable" description:"Actors that may change the value now."`
}

// SettingsValuesResponse lists effective values: app.* and policy.* without a
// project; project.* and the project's policy.* with one.
type SettingsValuesResponse struct {
	Project string         `json:"project,omitempty"`
	Values  []SettingValue `json:"values"`
}

// UpdateSettingsValuesRequest changes settings by registry key.
type UpdateSettingsValuesRequest struct {
	Project string         `json:"project,omitempty" description:"Project whose settings to change; empty changes app-wide settings."`
	Set     map[string]any `json:"set,omitempty" description:"Values to set, by registry key."`
	Unset   []string       `json:"unset,omitempty" description:"Keys to reset: project fields are cleared, app settings return to their default. Agent policy keys cannot be unset."`
}
