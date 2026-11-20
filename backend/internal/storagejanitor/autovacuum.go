package storagejanitor

import (
	"context"
	"time"

	sqlitestore "tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// maybeConvert switches an old database to incremental auto-vacuum once the
// archive backlog is gone (no point rewriting rows about to be deleted; a pass
// that finishes its work within budget only has the steady trickle of sessions
// crossing the retention window left) and the daemon has been quiet for
// QuietPasses intervals in a row.
func (j *Janitor) maybeConvert(ctx context.Context, stats *Stats) error {
	if j.converter == nil || j.converted {
		return nil
	}
	quiet := stats.ForegroundWrites >= 0 && stats.ForegroundWrites <= int64(j.cfg.QuietWrites)
	drained := !stats.OutOfBudget
	if !quiet || !drained {
		j.quietPasses = 0
		return nil
	}
	j.quietPasses++
	now := j.cfg.Clock()
	if j.quietPasses < j.cfg.QuietPasses || (!j.lastConversion.IsZero() && now.Sub(j.lastConversion) < j.cfg.ConversionRetry) {
		return nil
	}
	done, err := j.converter.IncrementalAutoVacuum(ctx)
	if err != nil {
		return err
	}
	if done {
		j.converted = true
		return nil
	}
	j.lastConversion = now
	j.cfg.Logger.Info("converting the database to incremental auto-vacuum; a write that waits longer than writeGrace interrupts it, after the VACUUM phase in progress",
		"writeGrace", j.cfg.ConversionWriteGrace, "maxLiveBytes", j.cfg.ConversionMaxLive)
	result, err := j.converter.ConvertToIncrementalAutoVacuum(ctx, sqlitestore.AutoVacuumConversionOptions{
		MaxLiveBytes: j.cfg.ConversionMaxLive,
		WriteGrace:   j.cfg.ConversionWriteGrace,
		Progress: func(p sqlitestore.AutoVacuumProgress) {
			j.cfg.Logger.Info("auto-vacuum conversion in progress",
				"elapsed", p.Elapsed.Round(time.Second), "liveBytes", p.LiveBytes, "walBytes", p.WALBytes)
		},
	})
	stats.Conversion = &result
	switch {
	case err != nil:
		return err
	case result.Converted:
		j.converted = true
		j.cfg.Logger.Info("database converted to incremental auto-vacuum",
			"elapsed", result.Elapsed.Round(time.Millisecond), "bytesBefore", result.FileBytes, "bytesAfter", result.AfterBytes)
	case result.Interrupted:
		j.cfg.Logger.Info("auto-vacuum conversion gave way to a write; the database is unchanged and the conversion will retry",
			"elapsed", result.Elapsed.Round(time.Millisecond), "retryAfter", j.cfg.ConversionRetry)
	case result.Skipped != "":
		// Too large or too little disk rarely changes within the hour; say
		// so once rather than every retry.
		if !j.skipLogged {
			j.skipLogged = true
			j.cfg.Logger.Warn("auto-vacuum conversion skipped; freed pages stay in the file until it can run",
				"reason", result.Skipped, "liveBytes", result.LiveBytes, "fileBytes", result.FileBytes)
		}
	}
	j.quietPasses = 0
	return nil
}
