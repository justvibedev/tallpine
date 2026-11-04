package domain

// PlanningMode says when a project plans before it builds (B205).
type PlanningMode string

// The planning modes of a project.
const (
	PlanningAuto   PlanningMode = "auto"
	PlanningAlways PlanningMode = "always"
	PlanningOff    PlanningMode = "off"
)

// Valid reports whether m is empty or a known mode.
func (m PlanningMode) Valid() bool {
	return m == "" || m == PlanningAuto || m == PlanningAlways || m == PlanningOff
}

// WithDefault returns auto for an empty mode.
func (m PlanningMode) WithDefault() PlanningMode {
	if m == "" {
		return PlanningAuto
	}
	return m
}
