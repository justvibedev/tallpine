package agentpolicy_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
)

// enableSecurityMode writes securityMode.enabled for the install ("") or a project.
func enableSecurityMode(h *harness, project string, value string) error {
	return h.engine().Update(ctx, project, agentpolicy.Update{"securityMode": {"enabled": json.RawMessage(value)}}, time.Now())
}

// assertUnavailable checks that turning Security mode on is refused with msg
// at both layers, nothing is stored, and turning it off still works.
func assertUnavailable(t *testing.T, h *harness, msg string) {
	t.Helper()
	for _, project := range []string{"", "p"} {
		err := enableSecurityMode(h, project, "true")
		var verr *agentpolicy.ValidationError
		if !errors.As(err, &verr) || verr.Message != msg {
			t.Fatalf("project %q: enabling Security mode returned %v, want ValidationError %q", project, err, msg)
		}
		if f := sm(t, h, project); string(f.Value) != "false" {
			t.Fatalf("project %q: refused write was stored: %+v", project, f)
		}
		for _, off := range []string{"false", "null"} {
			if err := enableSecurityMode(h, project, off); err != nil {
				t.Fatalf("project %q: writing %s returned %v", project, off, err)
			}
		}
	}
}

func TestSecurityMode_UnavailablePlatformRefusesOn(t *testing.T) {
	defer agentpolicy.SetSecurityModeUnavailableForTest("Security mode is not available on Windows")()
	assertUnavailable(t, newHarness(t), "Security mode is not available on Windows")
}

func TestSecurityMode_SupportedPlatformAllowsOn(t *testing.T) {
	defer agentpolicy.SetSecurityModeUnavailableForTest("")()
	h := newHarness(t)
	if err := enableSecurityMode(h, "", "true"); err != nil {
		t.Fatal(err)
	}
	if f := sm(t, h, ""); string(f.Value) != "true" {
		t.Fatalf("%+v", f)
	}
}
