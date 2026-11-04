package ports_test

import (
	"reflect"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// TestMetadataKeyAgentSessionIDMatchesDomainJSONTag pins the hand-maintained
// invariant documented on ports.MetadataKeyAgentSessionID: a silent rename on
// either side would break session restore.
func TestMetadataKeyAgentSessionIDMatchesDomainJSONTag(t *testing.T) {
	field, ok := reflect.TypeOf(domain.SessionMetadata{}).FieldByName("AgentSessionID")
	if !ok {
		t.Fatalf("domain.SessionMetadata has no AgentSessionID field")
	}
	name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
	if name != ports.MetadataKeyAgentSessionID {
		t.Fatalf("json tag %q != ports.MetadataKeyAgentSessionID %q", name, ports.MetadataKeyAgentSessionID)
	}
}

type modeBlindFencer struct{}

func (modeBlindFencer) FencesState(goos string) bool { return goos == "darwin" }

type modeFencer struct{ modeBlindFencer }

func (modeFencer) FencesLaunch(_ string, mode ports.PermissionMode) bool {
	return mode == ports.PermissionModeBypassPermissions
}

func TestFencesLaunchFallsBackToFencesState(t *testing.T) {
	if !ports.FencesLaunch(modeBlindFencer{}, "darwin", ports.PermissionModeAuto) || ports.FencesLaunch(modeBlindFencer{}, "linux", ports.PermissionModeBypassPermissions) {
		t.Fatal("an adapter without LaunchStateFencer must answer FencesState")
	}
	if ports.FencesLaunch(modeFencer{}, "darwin", ports.PermissionModeAuto) || !ports.FencesLaunch(modeFencer{}, "darwin", ports.PermissionModeBypassPermissions) {
		t.Fatal("LaunchStateFencer must take precedence over FencesState")
	}
	if ports.FencesLaunch(nil, "darwin", ports.PermissionModeBypassPermissions) {
		t.Fatal("nil adapter fenced")
	}
}
