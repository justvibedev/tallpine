package daemon

import (
	"context"
	"os"

	"tallpine.eu/tallpine/backend/internal/service/machineload"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// newMachineLoadGate builds the gate native reviewers and spawn warnings share
// (B752). Its limits are the stored settings with the TALLPINE_MACHINE_*
// variables laid over them, read on every decision so a change applies at once.
func newMachineLoadGate(store *sqlite.Store) *machineload.Gate {
	return machineload.New(machineload.Deps{
		Limits: func(ctx context.Context) (machineload.Limits, error) {
			row, err := store.GetAppSettings(ctx)
			if err != nil {
				return machineload.Limits{}, err
			}
			stored := machineload.Limits{MaxReviewers: row.MachineMaxReviewers, LoadPercent: row.MachineLoadPercent}
			return stored.WithEnv(os.LookupEnv), nil
		},
		Reviewers: store.CountRunningReviewers,
	})
}
