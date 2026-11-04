package store_test

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	sqlitestore "tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// openLegacyStore opens a store over a database created before incremental
// auto-vacuum (auto_vacuum=NONE), holding about mb MiB of disposable rows.
func openLegacyStore(t *testing.T, mb int) (*sqlitestore.Store, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ao.db")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		"PRAGMA auto_vacuum = NONE",
		"CREATE TABLE ballast (id INTEGER PRIMARY KEY, body BLOB)",
		"WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?) INSERT INTO ballast (body) SELECT randomblob(65536) FROM n",
	} {
		args := []any{}
		if strings.Contains(stmt, "?") {
			args = append(args, mb*16)
		}
		if _, err := raw.Exec(stmt, args...); err != nil {
			t.Fatal(err)
		}
	}
	_ = raw.Close()
	s, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func autoVacuumMode(t *testing.T, path string) (int, string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var mode int
	var check string
	if err := db.QueryRow("PRAGMA auto_vacuum").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("PRAGMA quick_check").Scan(&check); err != nil {
		t.Fatal(err)
	}
	return mode, check
}

func TestConvertToIncrementalAutoVacuumRebuildsALegacyDatabase(t *testing.T) {
	s, path := openLegacyStore(t, 4)
	ctx := context.Background()
	seedProject(t, s, "vac")
	rec, err := s.CreateSession(ctx, sampleRecord("vac"))
	if err != nil {
		t.Fatal(err)
	}
	if done, err := s.IncrementalAutoVacuum(ctx); err != nil || done {
		t.Fatalf("legacy database reports incremental=%v err=%v", done, err)
	}
	// Over the live-data bound nothing is rewritten.
	if big, err := s.ConvertToIncrementalAutoVacuum(ctx, sqlitestore.AutoVacuumConversionOptions{MaxLiveBytes: 1 << 20}); err != nil || big.Converted || big.Skipped == "" || big.LiveBytes <= 1<<20 {
		t.Fatalf("bounded conversion = %+v err=%v, want skipped as too large", big, err)
	}
	if mode, _ := autoVacuumMode(t, path); mode != 0 {
		t.Fatalf("skipped conversion changed auto_vacuum to %d", mode)
	}
	result, err := s.ConvertToIncrementalAutoVacuum(ctx, sqlitestore.AutoVacuumConversionOptions{})
	if err != nil || !result.Converted || result.Interrupted || result.Skipped != "" {
		t.Fatalf("conversion = %+v, err=%v", result, err)
	}
	if mode, check := autoVacuumMode(t, path); mode != 2 || check != "ok" {
		t.Fatalf("after conversion auto_vacuum=%d quick_check=%s", mode, check)
	}
	if _, found, err := s.GetSession(ctx, rec.ID); err != nil || !found {
		t.Fatalf("session lost by conversion: found=%v err=%v", found, err)
	}
	again, err := s.ConvertToIncrementalAutoVacuum(ctx, sqlitestore.AutoVacuumConversionOptions{})
	if err != nil || !again.Converted || again.Elapsed != 0 {
		t.Fatalf("second conversion = %+v err=%v, want an immediate no-op", again, err)
	}
}

// A write that arrives during the VACUUM interrupts it: the write lands
// promptly and the database is left exactly as it was, still unconverted.
func TestConvertToIncrementalAutoVacuumGivesWayToAWrite(t *testing.T) {
	s, path := openLegacyStore(t, 64)
	ctx := context.Background()
	seedProject(t, s, "vac")
	rec, err := s.CreateSession(ctx, sampleRecord("vac"))
	if err != nil {
		t.Fatal(err)
	}
	before := s.WriterAcquisitions()
	type outcome struct {
		result sqlitestore.AutoVacuumConversion
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := s.ConvertToIncrementalAutoVacuum(ctx, sqlitestore.AutoVacuumConversionOptions{})
		done <- outcome{result, err}
	}()
	for s.WriterAcquisitions() == before {
		time.Sleep(time.Millisecond)
	}
	const name = "written during conversion"
	started := time.Now()
	if ok, err := s.RenameSession(ctx, rec.ID, name, time.Now()); err != nil || !ok {
		t.Fatalf("rename during conversion: ok=%v err=%v", ok, err)
	}
	waited := time.Since(started)
	got := <-done
	if got.err != nil || !got.result.Interrupted || got.result.Converted {
		t.Fatalf("conversion = %+v err=%v, want interrupted", got.result, got.err)
	}
	if waited > 2*time.Second {
		t.Fatalf("the write waited %v behind the conversion", waited)
	}
	if pages, err := s.WriterAutocheckpointForTest(ctx); err != nil || pages != 1000 {
		t.Fatalf("writer wal_autocheckpoint = %d err=%v after an interrupted conversion, want 1000", pages, err)
	}
	if mode, check := autoVacuumMode(t, path); mode != 0 || check != "ok" {
		t.Fatalf("after interruption auto_vacuum=%d quick_check=%s, want the untouched legacy database", mode, check)
	}
	if stored, _, err := s.GetSession(ctx, rec.ID); err != nil || stored.DisplayName != name {
		t.Fatalf("write lost: %q err=%v", stored.DisplayName, err)
	}
	t.Logf("write waited %v; conversion gave way after %v", waited, got.result.Elapsed)
}

// Within WriteGrace a write just waits: a small database finishes converting
// and the write lands right after it.
func TestConvertToIncrementalAutoVacuumLetsAWriteWaitOutAShortVacuum(t *testing.T) {
	s, path := openLegacyStore(t, 4)
	ctx := context.Background()
	seedProject(t, s, "vac")
	rec, err := s.CreateSession(ctx, sampleRecord("vac"))
	if err != nil {
		t.Fatal(err)
	}
	before := s.WriterAcquisitions()
	done := make(chan sqlitestore.AutoVacuumConversion, 1)
	go func() {
		result, err := s.ConvertToIncrementalAutoVacuum(ctx, sqlitestore.AutoVacuumConversionOptions{WriteGrace: time.Minute})
		if err != nil {
			t.Error(err)
		}
		done <- result
	}()
	for s.WriterAcquisitions() == before {
		time.Sleep(time.Millisecond)
	}
	const name = "written after the conversion"
	if ok, err := s.RenameSession(ctx, rec.ID, name, time.Now()); err != nil || !ok {
		t.Fatalf("rename: ok=%v err=%v", ok, err)
	}
	if result := <-done; !result.Converted {
		t.Fatalf("conversion = %+v, want converted within the grace", result)
	}
	if mode, check := autoVacuumMode(t, path); mode != 2 || check != "ok" {
		t.Fatalf("auto_vacuum=%d quick_check=%s", mode, check)
	}
	if stored, _, err := s.GetSession(ctx, rec.ID); err != nil || stored.DisplayName != name {
		t.Fatalf("write lost: %q err=%v", stored.DisplayName, err)
	}
}

// walFullyCheckpointed reports whether every frame in the WAL has been copied
// into the database, from the wal-index header in the -shm file (mxFrame at
// byte 16, nBackfill at byte 96, native byte order). Running a checkpoint to
// find out would hide whether the code under test ran one.
func walFullyCheckpointed(t *testing.T, path string) bool {
	t.Helper()
	shm, err := os.ReadFile(path + "-shm")
	if err != nil || len(shm) < 100 {
		t.Fatalf("read wal-index: %v (len %d)", err, len(shm))
	}
	mxFrame := binary.NativeEndian.Uint32(shm[16:20])
	nBackfill := binary.NativeEndian.Uint32(shm[96:100])
	return mxFrame > 0 && nBackfill == mxFrame
}

// SQLite does not check for an interrupt in every VACUUM phase, so a
// cancelled VACUUM can commit and still return "context canceled". The
// conversion must report what the database says, not the error, and still
// finish: checkpoint the rebuilt file off the writer and restore the
// writer's autocheckpoint.
func TestConvertToIncrementalAutoVacuumTrustsTheDatabaseOverACancelAfterCommit(t *testing.T) {
	s, path := openLegacyStore(t, 8)
	ctx := context.Background()
	s.SetConversionVacuumForTest(func(ctx context.Context, exec func(context.Context) error) error {
		if err := exec(context.WithoutCancel(ctx)); err != nil {
			return err
		}
		return context.Canceled
	})
	result, err := s.ConvertToIncrementalAutoVacuum(ctx, sqlitestore.AutoVacuumConversionOptions{})
	if err != nil || !result.Converted || result.Interrupted {
		t.Fatalf("conversion = %+v err=%v, want converted despite the late cancel", result, err)
	}
	if mode, check := autoVacuumMode(t, path); mode != 2 || check != "ok" {
		t.Fatalf("auto_vacuum=%d quick_check=%s", mode, check)
	}
	if pages, err := s.WriterAutocheckpointForTest(ctx); err != nil || pages != 1000 {
		t.Fatalf("writer wal_autocheckpoint = %d err=%v, want 1000", pages, err)
	}
	if !walFullyCheckpointed(t, path) {
		t.Fatal("the rebuilt file was left in the WAL for the next write to checkpoint on the writer")
	}
}

// A VACUUM that fails before committing leaves the database unconverted, is
// reported as an error, and still gives the writer its autocheckpoint back.
func TestConvertToIncrementalAutoVacuumRestoresTheWriterWhenVacuumFails(t *testing.T) {
	s, path := openLegacyStore(t, 1)
	ctx := context.Background()
	boom := errors.New("disk full")
	s.SetConversionVacuumForTest(func(context.Context, func(context.Context) error) error { return boom })
	result, err := s.ConvertToIncrementalAutoVacuum(ctx, sqlitestore.AutoVacuumConversionOptions{})
	if !errors.Is(err, boom) || result.Converted || result.Interrupted {
		t.Fatalf("conversion = %+v err=%v, want the VACUUM error", result, err)
	}
	if mode, _ := autoVacuumMode(t, path); mode != 0 {
		t.Fatalf("auto_vacuum = %d after a failed VACUUM, want 0", mode)
	}
	if pages, err := s.WriterAutocheckpointForTest(ctx); err != nil || pages != 1000 {
		t.Fatalf("writer wal_autocheckpoint = %d err=%v, want 1000", pages, err)
	}
}
