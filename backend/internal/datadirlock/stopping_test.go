package datadirlock

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// MarkStopping keeps the holder's PID readable and adds the stopping mark with
// its stop-by time; a fresh holder starts unmarked (B564).
func TestMarkStoppingIsReadableAndClearedByNextHolder(t *testing.T) {
	dir := t.TempDir()
	lock, err := Acquire(context.Background(), dir, 0)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if got := ReadHolder(dir); got != (Holder{PID: os.Getpid()}) {
		t.Fatalf("ReadHolder before the mark = %+v, want pid %d unmarked", got, os.Getpid())
	}
	stopBy := time.UnixMilli(time.Now().Add(time.Minute).UnixMilli())
	if err := lock.MarkStopping(stopBy); err != nil {
		t.Fatalf("MarkStopping: %v", err)
	}
	if got := ReadHolder(dir); got.PID != os.Getpid() || !got.StopBy.Equal(stopBy) {
		t.Fatalf("ReadHolder after the mark = %+v, want pid %d stopping by %s", got, os.Getpid(), stopBy)
	}
	if _, err := Acquire(context.Background(), dir, 0); !errors.Is(err, ErrLocked) || !strings.Contains(err.Error(), "pid "+strconv.Itoa(os.Getpid())) {
		t.Fatalf("contender error should still name the holder pid: %v", err)
	}
	_ = lock.Release()

	next, err := Acquire(context.Background(), dir, 0)
	if err != nil {
		t.Fatalf("re-Acquire: %v", err)
	}
	defer func() { _ = next.Release() }()
	if got := ReadHolder(dir); !got.StopBy.IsZero() {
		t.Fatalf("a new holder inherited the stopping mark: %+v", got)
	}
}

func TestReadHolderUnknownWithoutLockFile(t *testing.T) {
	if got := ReadHolder(t.TempDir()); got != (Holder{}) {
		t.Fatalf("ReadHolder = %+v, want zero", got)
	}
	var nilLock *Lock
	if err := nilLock.MarkStopping(time.Now()); err != nil {
		t.Fatalf("nil MarkStopping: %v", err)
	}
}

// A stopping line without a usable time is not a mark: the reader must not
// treat such a holder as one with a deadline.
func TestReadHolderIgnoresMalformedMark(t *testing.T) {
	for _, text := range []string{"42\nstopping\n", "42\nstopping soon\n", "42\nstopping -5\n", "42\nstopp"} {
		dir := t.TempDir()
		if err := os.WriteFile(dir+"/"+lockFileName, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := ReadHolder(dir); got != (Holder{PID: 42}) {
			t.Errorf("ReadHolder(%q) = %+v, want pid 42 unmarked", text, got)
		}
	}
}
