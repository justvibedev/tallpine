package settingswrite

import (
	"context"
	"encoding/json"
	"time"

	"tallpine.eu/tallpine/backend/internal/ports"
	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
	"tallpine.eu/tallpine/backend/internal/settingsreg"
)

// appBatch gathers the app settings whose owner takes several keys in one
// write, so a batch that sets two of them writes once and keeps the others.
type appBatch struct {
	hibernation   settingssvc.ChatHibernationUpdate
	compactionOn  *bool
	retention     *int
	machine       settingssvc.MachineLoadUpdate
	setHibernate  bool
	setCompaction bool
	setMachine    bool
}

// add records key's value and reports whether the batch owns the key.
func (b *appBatch) add(key settingsreg.Key, raw json.RawMessage) bool {
	switch key {
	case "app.chatHibernation.enabled":
		var on bool
		_ = json.Unmarshal(raw, &on)
		b.hibernation.Enabled, b.setHibernate = &on, true
	case "app.chatHibernation.workerIdleSeconds", "app.chatHibernation.orchestratorIdleSeconds":
		var seconds int64
		_ = json.Unmarshal(raw, &seconds)
		d := time.Duration(seconds) * time.Second
		if key == "app.chatHibernation.workerIdleSeconds" {
			b.hibernation.WorkerIdle = &d
		} else {
			b.hibernation.OrchestratorIdle = &d
		}
		b.setHibernate = true
	case "app.storageCompaction":
		var on bool
		_ = json.Unmarshal(raw, &on)
		b.compactionOn, b.setCompaction = &on, true
	case "app.providerEventRetentionDays":
		var days int
		_ = json.Unmarshal(raw, &days)
		b.retention, b.setCompaction = &days, true
	case "app.machineLoad.maxReviewers", "app.machineLoad.loadPercent":
		var n int
		_ = json.Unmarshal(raw, &n)
		if key == "app.machineLoad.maxReviewers" {
			b.machine.MaxReviewers = &n
		} else {
			b.machine.LoadPercent = &n
		}
		b.setMachine = true
	default:
		return false
	}
	return true
}

// flush writes what the batch gathered, one owner call per group.
func (b *appBatch) flush(ctx context.Context, w *Writer) error {
	if b.setMachine {
		if err := w.setMachineLoad(ctx, b.machine); err != nil {
			return err
		}
	}
	if b.setHibernate {
		if _, err := w.AppSettings.SetChatHibernation(ctx, b.hibernation); err != nil {
			return appError(err)
		}
	}
	if b.setCompaction {
		if _, err := w.AppSettings.SetStorageCompaction(ctx, b.compactionOn, b.retention); err != nil {
			return appError(err)
		}
	}
	return nil
}

// setMachineLoad writes the limits a batch changed, keeping the other one.
func (w *Writer) setMachineLoad(ctx context.Context, update settingssvc.MachineLoadUpdate) error {
	setter, ok := w.AppSettings.(interface {
		SetMachineLoad(ctx context.Context, update settingssvc.MachineLoadUpdate) (settingssvc.Snapshot, error)
	})
	if !ok {
		return appError(ports.ErrChatUnsupported)
	}
	if _, err := setter.SetMachineLoad(ctx, update); err != nil {
		return appError(err)
	}
	return nil
}
