package controllers

import (
	"context"

	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
	"tallpine.eu/tallpine/backend/internal/service/settingsaudit"
)

// appSettingsValues flattens the app settings snapshot into its app.* keys.
func appSettingsValues(s settingssvc.Snapshot) settingsaudit.Values {
	return settingsaudit.AppSettings(s)
}

// appSnapshotForAudit reads the settings before a write, for the change log.
// ok is false when nothing will be recorded.
func (c *SettingsController) appSnapshotForAudit(ctx context.Context) (settingssvc.Snapshot, bool) {
	if c.Audit == nil {
		return settingssvc.Snapshot{}, false
	}
	s, err := c.Svc.Get(ctx)
	return s, err == nil
}

// recordApp logs the app settings a write changed.
func (c *SettingsController) recordApp(ctx context.Context, before settingssvc.Snapshot, ok bool, after settingssvc.Snapshot) {
	if ok {
		c.Audit.Record(ctx, "", appSettingsValues(before), appSettingsValues(after))
	}
}

// cleanupPolicyValues flattens a cleanup policy into its app.cleanup.* keys.
func cleanupPolicyValues(policy any) settingsaudit.Values {
	return settingsaudit.JSONObject("app.cleanup.", policy)
}
