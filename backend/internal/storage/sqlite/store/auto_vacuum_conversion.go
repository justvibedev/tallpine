package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

// conversionDiskMargin is free space kept beyond what a conversion needs.
const conversionDiskMargin int64 = 1 << 30

// errForegroundWrite cancels a conversion when another write needs the writer.
var errForegroundWrite = errors.New("a foreground write needed the database")

// AutoVacuumConversion reports one attempt to switch a database created before
// incremental auto-vacuum to it.
type AutoVacuumConversion struct {
	// Converted is set once the database uses auto_vacuum=INCREMENTAL, whether
	// this attempt converted it or it already did.
	Converted bool
	// Skipped names why the attempt did not start, e.g. too little free disk.
	Skipped string
	// Interrupted is set when a foreground write arrived: the VACUUM was rolled
	// back and the database is exactly as it was.
	Interrupted bool
	Elapsed     time.Duration
	// FileBytes is the database size before; LiveBytes what a VACUUM keeps;
	// AfterBytes the size once converted.
	FileBytes, LiveBytes, AfterBytes int64
}

// AutoVacuumConversionOptions tunes one conversion attempt.
type AutoVacuumConversionOptions struct {
	// MaxLiveBytes bounds the live data the VACUUM may rewrite; 0 means no
	// bound. A daemon is never write-free for minutes, even idle, so a VACUUM
	// that long would only ever be interrupted; a larger database is skipped.
	MaxLiveBytes int64
	// WriteGrace is how long another write may wait for the VACUUM before it
	// is interrupted. A small database rebuilds within it, so the write is only
	// delayed; a large one gives way. It does not bound the delay on its own:
	// SQLite checks for an interrupt only between steps of some VACUUM phases,
	// so a write can also wait out the phase in progress, about half a second
	// per 200 MB of live data measured. MaxLiveBytes is what bounds that.
	WriteGrace time.Duration
	// Progress, if set, is called every 15 seconds while the VACUUM runs.
	Progress func(AutoVacuumProgress)
}

// AutoVacuumProgress is reported periodically while a conversion runs.
type AutoVacuumProgress struct {
	Elapsed   time.Duration
	LiveBytes int64
	// WALBytes grows toward LiveBytes while VACUUM copies the rebuilt
	// database back through the write-ahead log.
	WALBytes int64
}

// IncrementalAutoVacuum reports whether the database already returns freed
// pages to the OS a few at a time. It asks the writer connection: a
// connection caches the mode when it opens the file, so a pooled reader can
// still report the old one after a VACUUM changed it.
func (s *Store) IncrementalAutoVacuum(ctx context.Context) (bool, error) {
	var mode int64
	if err := s.writeDB.QueryRowContext(ctx, "PRAGMA auto_vacuum").Scan(&mode); err != nil {
		return false, fmt.Errorf("read auto_vacuum: %w", err)
	}
	const incremental = 2
	return mode == incremental, nil
}

// ConvertToIncrementalAutoVacuum rebuilds a database created before
// incremental auto-vacuum (new databases get it at creation) with one VACUUM,
// so the archive janitor can finally hand freed pages back to the OS.
//
// VACUUM rewrites the whole file and holds the writer for as long as that
// takes, minutes on a multi-gigabyte history. So it gives way: once another
// write has waited WriteGrace for the writer lock or the writer connection,
// the VACUUM is interrupted and rolled back, leaving the database exactly as it
// was, and the caller retries at a later quiet moment. It needs free disk for a
// temporary copy and for the write-ahead log, about twice the live data, and
// skips the attempt without one.
func (s *Store) ConvertToIncrementalAutoVacuum(ctx context.Context, opts AutoVacuumConversionOptions) (AutoVacuumConversion, error) {
	var result AutoVacuumConversion
	if done, err := s.IncrementalAutoVacuum(ctx); err != nil || done {
		result.Converted = done
		return result, err
	}
	var path string
	var pages, free, pageSize int64
	if err := s.readDB.QueryRowContext(ctx, "SELECT file FROM pragma_database_list WHERE name = 'main'").Scan(&path); err != nil {
		return result, fmt.Errorf("locate database file: %w", err)
	}
	for pragma, dst := range map[string]*int64{"page_count": &pages, "freelist_count": &free, "page_size": &pageSize} {
		if err := s.readDB.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(dst); err != nil {
			return result, fmt.Errorf("read %s: %w", pragma, err)
		}
	}
	result.FileBytes, result.LiveBytes = pages*pageSize, (pages-free)*pageSize
	if opts.MaxLiveBytes > 0 && result.LiveBytes > opts.MaxLiveBytes {
		result.Skipped = fmt.Sprintf("%d bytes of live data is more than an online conversion may rewrite (%d): its VACUUM would hold the writer for minutes", result.LiveBytes, opts.MaxLiveBytes)
		return result, nil
	}
	if reason := conversionDiskShortfall(path, result.LiveBytes); reason != "" {
		result.Skipped = reason
		return result, nil
	}

	// Counted from before taking the lock, so a write that queues behind it
	// at any point, even before the VACUUM starts, cancels the attempt.
	base := s.WriterContention()
	prepared, vacuumErr := s.vacuumForConversion(ctx, base, opts, path, &result)
	if !prepared {
		return result, vacuumErr
	}

	// Whatever the VACUUM returned, the writer gets its autocheckpoint back
	// and anything it committed is folded into the main file from a reader,
	// where that delays no write. Skipping this after a commit would leave the
	// rebuilt file in the WAL for the next write's checkpoint on the writer.
	finish := context.WithoutCancel(ctx)
	_, _ = s.readDB.ExecContext(finish, "PRAGMA wal_checkpoint(PASSIVE)")
	_, _ = s.writeDB.ExecContext(finish, "PRAGMA wal_autocheckpoint = 1000")

	// The error does not say whether the VACUUM committed: SQLite does not
	// check for an interrupt in every phase, so a cancelled VACUUM can run to
	// its commit and still return "context canceled". The database decides.
	converted, err := s.IncrementalAutoVacuum(finish)
	if err != nil {
		return result, err
	}
	if converted {
		result.Converted = true
		if err := s.readDB.QueryRowContext(finish, "PRAGMA page_count").Scan(&pages); err == nil {
			result.AfterBytes = pages * pageSize
		}
		return result, nil
	}
	switch {
	case vacuumErr == nil:
		return result, errors.New("vacuum: finished without switching to incremental auto_vacuum")
	case errors.Is(vacuumErr, errForegroundWrite):
		result.Interrupted = true
		return result, nil
	case ctx.Err() != nil:
		return result, ctx.Err()
	default:
		return result, fmt.Errorf("vacuum: %w", vacuumErr)
	}
}

// vacuumForConversion holds the writer for the VACUUM itself. prepared
// reports that autocheckpoint was paused, so the caller must restore it. The
// error wraps errForegroundWrite when a waiting write cancelled the VACUUM.
func (s *Store) vacuumForConversion(ctx context.Context, base uint64, opts AutoVacuumConversionOptions, path string, result *AutoVacuumConversion) (prepared bool, err error) {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return false, err
	}
	defer s.writeMu.Unlock()

	// The VACUUM commit would otherwise checkpoint the whole rebuilt file on
	// the writer connection before returning.
	if _, err := s.writeDB.ExecContext(ctx, "PRAGMA wal_autocheckpoint = 0"); err != nil {
		return false, fmt.Errorf("pause autocheckpoint: %w", err)
	}
	if _, err := s.writeDB.ExecContext(ctx, "PRAGMA auto_vacuum = INCREMENTAL"); err != nil {
		return true, fmt.Errorf("request incremental auto_vacuum: %w", err)
	}

	vacuumCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	started := time.Now()
	watched := make(chan struct{})
	go s.watchConversion(vacuumCtx, cancel, watched, base, opts, path, started, result.LiveBytes)
	exec := func(ctx context.Context) error {
		_, err := s.writeDB.ExecContext(ctx, "VACUUM")
		return err
	}
	if s.conversionVacuum != nil {
		err = s.conversionVacuum(vacuumCtx, exec)
	} else {
		err = exec(vacuumCtx)
	}
	cancel(nil)
	<-watched
	result.Elapsed = time.Since(started)
	if err != nil && errors.Is(context.Cause(vacuumCtx), errForegroundWrite) {
		err = fmt.Errorf("%w: %w", errForegroundWrite, err)
	}
	return true, err
}

// watchConversion cancels the VACUUM once another write has waited
// WriteGrace and reports progress every 15 seconds. It closes done when it
// returns.
func (s *Store) watchConversion(ctx context.Context, cancel context.CancelCauseFunc, done chan<- struct{}, base uint64, opts AutoVacuumConversionOptions, path string, started time.Time, live int64) {
	defer close(done)
	check := time.NewTicker(10 * time.Millisecond)
	defer check.Stop()
	report := time.NewTicker(15 * time.Second)
	defer report.Stop()
	var waitingSince time.Time
	for {
		if waitingSince.IsZero() && s.WriterContention() != base {
			waitingSince = time.Now()
		}
		if !waitingSince.IsZero() && time.Since(waitingSince) >= opts.WriteGrace {
			cancel(errForegroundWrite)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-check.C:
		case <-report.C:
			if opts.Progress == nil {
				continue
			}
			var wal int64
			if info, err := os.Stat(path + "-wal"); err == nil {
				wal = info.Size()
			}
			opts.Progress(AutoVacuumProgress{Elapsed: time.Since(started), LiveBytes: live, WALBytes: wal})
		}
	}
}

// conversionDiskShortfall explains why there is not enough free disk for a
// VACUUM of live bytes, or returns "". SQLite builds the new database in a
// temporary file and then writes all of it through the write-ahead log, so
// both need room for the live data.
func conversionDiskShortfall(dbPath string, live int64) string {
	need := live + conversionDiskMargin
	tmp := os.TempDir()
	dbFree, dbVolume, ok := diskFree(dbPath)
	if !ok {
		return "free disk space is unknown on this platform"
	}
	tmpFree, tmpVolume, ok := diskFree(tmp)
	if !ok {
		return "free disk space of the temporary directory is unknown"
	}
	if dbVolume == tmpVolume {
		if dbFree < 2*live+conversionDiskMargin {
			return fmt.Sprintf("needs %d bytes free for a temporary copy and the write-ahead log, has %d", 2*live+conversionDiskMargin, dbFree)
		}
		return ""
	}
	if dbFree < need {
		return fmt.Sprintf("needs %d bytes free beside the database, has %d", need, dbFree)
	}
	if tmpFree < need {
		return fmt.Sprintf("needs %d bytes free in %s, has %d", need, tmp, tmpFree)
	}
	return ""
}
