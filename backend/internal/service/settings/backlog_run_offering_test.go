package settings_test

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/service/settings"
)

// The settings endpoint is how the UI learns whether to show run mode at all.
func TestOfferingReportsBacklogRunSwitch(t *testing.T) {
	if settings.OfferingFromConfig(config.Config{}).BacklogRunEnabled {
		t.Fatal("run mode reported on with the switch off")
	}
	if !settings.OfferingFromConfig(config.Config{BacklogRun: true}).BacklogRunEnabled {
		t.Fatal("run mode reported off with the switch on")
	}
}
