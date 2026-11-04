package session

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
)

// B541: the 409 names the holding operation and its age.
func TestOperationInProgressNamesHolder(t *testing.T) {
	busy := &sessionmanager.OperationInProgressError{Holder: "reconcile", HeldFor: 42 * time.Second}
	var e *apierr.Error
	if !errors.As(toAPIError(fmt.Errorf("kill w: %w", busy)), &e) || e.Kind != apierr.KindConflict || e.Code != "SESSION_OPERATION_IN_PROGRESS" {
		t.Fatalf("mapped = %#v", e)
	}
	if !strings.Contains(e.Message, "reconcile, running for 42s") || e.Details["operation"] != "reconcile" {
		t.Fatalf("message %q details %v", e.Message, e.Details)
	}
	if !errors.As(toAPIError(sessionmanager.ErrOperationInProgress), &e) || e.Message != "Another operation is already running on this session" {
		t.Fatalf("bare sentinel mapped = %#v", e)
	}
}
