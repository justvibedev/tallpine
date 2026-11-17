package settings

import (
	"context"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/service/machineload"
)

// ErrInvalidMachineLoad rejects an out-of-range machine-load limit.
var ErrInvalidMachineLoad = errors.New("invalid machine load setting")

// MachineLoadUpdate changes either machine-load limit; nil keeps the current one.
type MachineLoadUpdate struct {
	MaxReviewers *int
	LoadPercent  *int
}

// SetMachineLoad changes the machine-load gate's limits.
func (s *Service) SetMachineLoad(ctx context.Context, update MachineLoadUpdate) (Snapshot, error) {
	current, err := s.store.GetAppSettings(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	maxReviewers, loadPercent := current.MachineMaxReviewers, current.MachineLoadPercent
	if update.MaxReviewers != nil {
		maxReviewers = *update.MaxReviewers
	}
	if update.LoadPercent != nil {
		loadPercent = *update.LoadPercent
	}
	if maxReviewers < 0 || maxReviewers > machineload.MaxReviewersCeiling || loadPercent < 0 || loadPercent > machineload.LoadPercentCeiling {
		return Snapshot{}, fmt.Errorf("%w: reviewers 0-%d, load percent 0-%d", ErrInvalidMachineLoad, machineload.MaxReviewersCeiling, machineload.LoadPercentCeiling)
	}
	writer, ok := s.store.(interface {
		SetMachineLoad(ctx context.Context, maxReviewers, loadPercent int, now time.Time) error
	})
	if !ok {
		return Snapshot{}, ports.ErrChatUnsupported
	}
	if err := writer.SetMachineLoad(ctx, maxReviewers, loadPercent, s.now()); err != nil {
		return Snapshot{}, err
	}
	return s.Get(ctx)
}
