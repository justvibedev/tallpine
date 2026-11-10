package cli

import (
	"strings"
	"testing"
)

func peerCheckLine(t *testing.T, resp silentFailuresResponse) doctorCheck {
	t.Helper()
	for _, check := range silentFailureChecks(resp) {
		if check.Name == "peer-check" {
			return check
		}
	}
	t.Fatal("no peer-check line")
	return doctorCheck{}
}

func TestDoctorPeerCheckStatesEffectiveMode(t *testing.T) {
	check := peerCheckLine(t, silentFailuresResponse{PeerCheck: &silentPeerCheck{Mode: "off", ModeReason: "unsigned or development build"}})
	if check.Level != doctorPass || check.Message != "peer check off: unsigned or development build" {
		t.Fatalf("check = %+v", check)
	}
}

func TestDoctorPeerCheckListsRecordsWithoutRaisingLevel(t *testing.T) {
	pc := &silentPeerCheck{
		Mode: "report", ModeReason: "signed build, recording only",
		Admitted: map[string]int64{"desktop": 3, "agent": 7},
	}
	pc.WouldRefuse = append(pc.WouldRefuse, struct {
		Executable string `json:"executable"`
		TokenClass string `json:"tokenClass"`
		Route      string `json:"route"`
		Reason     string `json:"reason"`
		Count      int64  `json:"count"`
	}{"/usr/bin/curl", "install", "GET /api/v1/sessions", "install_token_outside_desktop", 2})
	check := peerCheckLine(t, silentFailuresResponse{PeerCheck: pc})
	if check.Level != doctorPass {
		t.Fatalf("records are expected while recording; level = %v", check.Level)
	}
	for _, want := range []string{"peer check report", "desktop=3", "agent=7", "would refuse 2", "/usr/bin/curl"} {
		if !strings.Contains(check.Message, want) {
			t.Errorf("message %q lacks %q", check.Message, want)
		}
	}
}

func TestDoctorPeerCheckAbsentFromOlderDaemon(t *testing.T) {
	for _, check := range silentFailureChecks(silentFailuresResponse{}) {
		if check.Name == "peer-check" {
			t.Fatalf("no field means no line: %+v", check)
		}
	}
}
