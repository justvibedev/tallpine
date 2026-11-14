//go:build windows

package migratevo

import (
	"context"
	"errors"
	"testing"
)

func TestCheckPreconditionsRefusesOnWindows(t *testing.T) {
	for _, resuming := range []bool{false, true} {
		e := &Env{Source: t.TempDir(), Target: t.TempDir()}
		if err := e.CheckPreconditions(context.Background(), resuming); !errors.Is(err, errUnsupported) {
			t.Fatalf("resuming=%v: got %v, want errUnsupported", resuming, err)
		}
	}
}
