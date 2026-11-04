package store_test

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// sessionFixture builds a store seeded with project "mer" and one session, the
// shared setup of the notification and review store tests.
func sessionFixture(t *testing.T) (*sqlite.Store, context.Context, domain.SessionRecord, time.Time) {
	t.Helper()
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	rec, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return s, ctx, rec, time.Now().UTC().Truncate(time.Second)
}

// needsInputNotification is the canonical unread needs-input record for sess.
func needsInputNotification(sess domain.SessionRecord, now time.Time) domain.NotificationRecord {
	return domain.NotificationRecord{
		ID:        "ntf_1",
		SessionID: sess.ID,
		ProjectID: sess.ProjectID,
		Type:      domain.NotificationNeedsInput,
		Title:     "checkout-flow needs input",
		Status:    domain.NotificationUnread,
		CreatedAt: now,
	}
}

// holdWriter occupies the projection writer until the returned release is
// closed, so later projections queue behind it. It returns once the blocker is
// inside the writer.
func holdWriter(ctx context.Context, s *sqlite.Store) (release chan struct{}, blockerDone chan error) {
	entered, release := make(chan struct{}), make(chan struct{})
	blockerDone = make(chan error, 1)
	go func() {
		_, err := s.GroupCommitProjectionForTest(ctx, func(context.Context, any) (bool, error) {
			close(entered)
			<-release
			return false, nil
		})
		blockerDone <- err
	}()
	<-entered
	return release, blockerDone
}
