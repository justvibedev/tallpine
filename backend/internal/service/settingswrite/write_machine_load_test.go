package settingswrite

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
	"tallpine.eu/tallpine/backend/internal/service/settingsview"
	"tallpine.eu/tallpine/backend/internal/settingsreg"
)

// machineLoadRow is an app_settings row holding only the two limits.
type machineLoadRow struct {
	settingssvc.Store
	max, percent int
}

func (r *machineLoadRow) GetAppSettings(context.Context) (settingssvc.Snapshot, error) {
	return settingssvc.Snapshot{MachineMaxReviewers: r.max, MachineLoadPercent: r.percent}, nil
}

func (r *machineLoadRow) SetMachineLoad(_ context.Context, maxReviewers, loadPercent int, _ time.Time) error {
	r.max, r.percent = maxReviewers, loadPercent
	return nil
}

func machineLoadWriter() (*Writer, *machineLoadRow) {
	row := &machineLoadRow{max: 3, percent: 100}
	app := settingssvc.New(row, nil, settingssvc.Offering{}, nil)
	return &Writer{View: &settingsview.Service{AppSettings: app}, AppSettings: app}, row
}

func setInt(ctx context.Context, w *Writer, key settingsreg.Key, n int) error {
	raw, _ := json.Marshal(n)
	return w.Apply(ctx, Change{Set: map[settingsreg.Key]json.RawMessage{key: raw}})
}

func TestMachineLoadSettingsAreWrittenAndValidated(t *testing.T) {
	w, row := machineLoadWriter()
	ctx := context.Background()
	if err := setInt(ctx, w, "app.machineLoad.maxReviewers", 0); err != nil {
		t.Fatal(err)
	}
	if err := setInt(ctx, w, "app.machineLoad.loadPercent", 250); err != nil {
		t.Fatal(err)
	}
	if row.max != 0 || row.percent != 250 {
		t.Fatalf("row = %d/%d, want 0/250 (each write keeps the other limit)", row.max, row.percent)
	}
	for key, n := range map[settingsreg.Key]int{"app.machineLoad.maxReviewers": 101, "app.machineLoad.loadPercent": -1} {
		if err := setInt(ctx, w, key, n); err == nil {
			t.Fatalf("%s = %d was accepted", key, n)
		}
	}
	if row.max != 0 || row.percent != 250 {
		t.Fatalf("a refused write changed the row: %d/%d", row.max, row.percent)
	}
	if err := w.Apply(ctx, Change{Unset: []settingsreg.Key{"app.machineLoad.maxReviewers"}}); err != nil {
		t.Fatal(err)
	}
	if row.max != 3 {
		t.Fatalf("reset left maxReviewers at %d, want the default 3", row.max)
	}
}
