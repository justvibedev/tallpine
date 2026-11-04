package preview

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

const testDaemonOrigin = "http://127.0.0.1:3101"

// newPollerFor wraps svc in a Poller rooted at the test daemon origin.
func newPollerFor(svc *fakePreviewSessions) *Poller {
	return NewPoller(svc, svc, testDaemonOrigin, PollerConfig{Logger: discardLogger()})
}

// newWorkerPoller returns a fake service holding one worker session tp-1 in
// workspace with the given stored preview URL, and a Poller over it.
func newWorkerPoller(workspace, previewURL string) (*fakePreviewSessions, *Poller) {
	svc := &fakePreviewSessions{sessions: []domain.SessionRecord{workerSession("tp-1", workspace, previewURL)}}
	return svc, newPollerFor(svc)
}

// mustPoll runs one Poll and fails the test, labelled, on error.
func mustPoll(t *testing.T, poller *Poller, label string) {
	t.Helper()
	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("%s: %v", label, err)
	}
}

// assertOnlySet asserts exactly one set: tp-1 to the file URL for rel.
func assertOnlySet(t *testing.T, svc *fakePreviewSessions, rel string) {
	t.Helper()
	assertSets(t, svc.sets, previewSet{id: "tp-1", url: mustFileURL(t, testDaemonOrigin, "tp-1", rel)})
}
