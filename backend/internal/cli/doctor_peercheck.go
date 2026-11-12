package cli

import (
	"fmt"
	"strings"
)

// silentPeerCheck is the daemon's report on which callers it did not start.
type silentPeerCheck struct {
	Mode        string           `json:"mode"`
	ModeReason  string           `json:"modeReason"`
	Admitted    map[string]int64 `json:"admitted"`
	WouldRefuse []struct {
		Executable string `json:"executable"`
		TokenClass string `json:"tokenClass"`
		Route      string `json:"route"`
		Reason     string `json:"reason"`
		Count      int64  `json:"count"`
	} `json:"wouldRefuse"`
	Overflow int64 `json:"overflow"`
	Dropped  int64 `json:"dropped"`
}

// peerCheckDoctorCheck states the effective mode and what it refused, or in
// report mode would refuse. A refusal is the check working, so it never raises
// doctor's level.
func peerCheckDoctorCheck(pc silentPeerCheck) doctorCheck {
	check := doctorCheck{Level: doctorPass, Section: doctorSectionSilent, Name: "peer-check"}
	if pc.Mode == "off" {
		check.Message = "peer check off: " + pc.ModeReason
		return check
	}
	var refused int64
	parts := make([]string, 0, len(pc.WouldRefuse))
	for _, rec := range pc.WouldRefuse {
		refused += rec.Count
		parts = append(parts, fmt.Sprintf("%dx %s %s from %s (%s)", rec.Count, rec.TokenClass, rec.Route, rec.Executable, rec.Reason))
	}
	verb := "would refuse"
	if pc.Mode == "enforce" {
		verb = "refused"
	}
	check.Message = fmt.Sprintf("peer check %s: %s; admitted desktop=%d agent=%d desktop_shell=%d; %s %d",
		pc.Mode, pc.ModeReason, pc.Admitted["desktop"], pc.Admitted["agent"], pc.Admitted["desktop_shell"], verb, refused)
	if len(parts) > 0 {
		check.Message += ": " + strings.Join(parts, "; ")
	}
	if pc.Overflow > 0 || pc.Dropped > 0 {
		check.Message += fmt.Sprintf(" (%d past the record limit, %d not classified)", pc.Overflow, pc.Dropped)
	}
	return check
}
