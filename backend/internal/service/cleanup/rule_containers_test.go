package cleanup

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type fakeDocker struct {
	mu         sync.Mutex
	containers []ports.LabeledResource
	volumes    []ports.LabeledResource
	removed    []string
	volumeErr  error
}

func (f *fakeDocker) SessionContainers(context.Context) ([]ports.LabeledResource, error) {
	return f.containers, nil
}
func (f *fakeDocker) SessionVolumes(context.Context) ([]ports.LabeledResource, error) {
	return f.volumes, nil
}
func (f *fakeDocker) RemoveContainer(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, "c:"+id)
	return nil
}
func (f *fakeDocker) RemoveVolume(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.volumeErr != nil {
		return f.volumeErr
	}
	f.removed = append(f.removed, "v:"+name)
	return nil
}

type fakeProjects []domain.ProjectRecord

func (f fakeProjects) ListProjects(context.Context) ([]domain.ProjectRecord, error) { return f, nil }

// GetSession reports every session as still terminated (as listed).
func (f fakeProjects) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	return domain.SessionRecord{ID: id, IsTerminated: true}, true, nil
}

var runtimeNow = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

func endedAgo(id domain.SessionID, project domain.ProjectID, ago time.Duration) domain.SessionRecord {
	return domain.SessionRecord{ID: id, ProjectID: project, IsTerminated: true, UpdatedAt: runtimeNow.Add(-ago)}
}

func liveNow(id domain.SessionID, project domain.ProjectID) domain.SessionRecord {
	return domain.SessionRecord{ID: id, ProjectID: project, UpdatedAt: runtimeNow}
}

func runRuntimeRules(t *testing.T, store Store, req RunRequest, rules ...Rule) Report {
	t.Helper()
	svc := New(store, rules, Config{Pause: -1, StartDelay: -1, Clock: func() time.Time { return runtimeNow }, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	report, err := svc.Run(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func TestContainerRuleRemovesOnlyEndedSessionsResources(t *testing.T) {
	docker := &fakeDocker{
		containers: []ports.LabeledResource{
			{ID: "c-ended", Name: "pg", Session: "mer-1"},
			{ID: "c-spare", Name: "registry", Session: "mer-1", Spare: true},
			{ID: "c-live", Name: "redis", Session: "mer-2"},
			{ID: "c-unknown", Name: "other", Session: "elsewhere-9"},
			{ID: "c-recent", Name: "pg", Session: "mer-3"},
			{ID: "c-optout", Name: "pg", Session: "opt-1"},
		},
		volumes: []ports.LabeledResource{
			{ID: "v-old", Session: "mer-1"},
			{ID: "v-young", Session: "mer-4"},
			{ID: "v-live", Session: "mer-2"},
		},
	}
	projects := fakeProjects{{ID: "mer"}, {ID: "opt", Config: domain.ProjectConfig{ContainerReap: domain.ContainerReapConfig{Disabled: true}}}}
	store := &fakeStore{policy: `{"containers":{"enabled":true}}`, sessions: []domain.SessionRecord{
		endedAgo("mer-1", "mer", 10*24*time.Hour), liveNow("mer-2", "mer"), endedAgo("mer-3", "mer", time.Minute),
		endedAgo("mer-4", "mer", 24*time.Hour), endedAgo("opt-1", "opt", 10*24*time.Hour),
	}}
	rule := NewContainerRule(docker, projects)

	runRuntimeRules(t, store, RunRequest{DryRun: true}, rule)
	if len(docker.removed) != 0 {
		t.Fatalf("dry run removed %v", docker.removed)
	}
	report := runRuntimeRules(t, store, RunRequest{}, rule)
	sort.Strings(docker.removed)
	if want := []string{"c:c-ended", "v:v-old"}; len(docker.removed) != 2 || docker.removed[0] != want[0] || docker.removed[1] != want[1] {
		t.Fatalf("removed %v, want %v", docker.removed, want)
	}
	kept := map[string]string{}
	for _, item := range report.Categories[0].Items {
		if item.Status == StatusKept {
			kept[item.What] = item.Reason
		}
	}
	// Every resource it leaves is reported, with its reason.
	for what, reason := range map[string]string{
		"container other": "unknown", "container pg": "", "volume v-young": "ended less than",
		"container registry": "tallpine.spare", "container redis": "live", "volume v-live": "live",
	} {
		if got, ok := kept[what]; !ok || !strings.Contains(got, reason) {
			t.Errorf("%s kept=%t reason=%q, want %q", what, ok, got, reason)
		}
	}
}

func TestContainerRuleReportsAVolumeStillInUse(t *testing.T) {
	docker := &fakeDocker{volumes: []ports.LabeledResource{{ID: "v-old", Session: "mer-1"}}, volumeErr: errors.New("volume is in use")}
	store := &fakeStore{policy: `{"containers":{"enabled":true}}`, sessions: []domain.SessionRecord{endedAgo("mer-1", "mer", 30*24*time.Hour)}}
	report := runRuntimeRules(t, store, RunRequest{}, NewContainerRule(docker, fakeProjects{{ID: "mer"}}))
	if cat := report.Categories[0]; cat.Failed != 1 || cat.Freed != 0 {
		t.Fatalf("in-use volume: %+v", cat)
	}
}

// restoredProjects reports one session as live again when it is re-read.
type restoredProjects struct {
	fakeProjects
	restored domain.SessionID
}

func (r restoredProjects) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	return domain.SessionRecord{ID: id, IsTerminated: id != r.restored}, true, nil
}

func TestContainerRuleRechecksTheSessionBeforeRemoval(t *testing.T) {
	docker := &fakeDocker{containers: []ports.LabeledResource{{ID: "c-1", Name: "pg", Session: "mer-1"}}}
	store := &fakeStore{policy: `{"containers":{"enabled":true}}`, sessions: []domain.SessionRecord{endedAgo("mer-1", "mer", 10*24*time.Hour)}}
	report := runRuntimeRules(t, store, RunRequest{}, NewContainerRule(docker, restoredProjects{fakeProjects: fakeProjects{{ID: "mer"}}, restored: "mer-1"}))
	if len(docker.removed) != 0 {
		t.Fatalf("removed %v from a session restored since the snapshot", docker.removed)
	}
	if items := report.Categories[0].Items; len(items) != 1 || items[0].Reason != "its session is live again" {
		t.Fatalf("items = %+v, want the container reported as kept", items)
	}
}

// Off by default: until the user turns it on, a run only reports what it
// would remove.
func TestContainerRuleOnlyReportsUntilTurnedOn(t *testing.T) {
	docker := &fakeDocker{containers: []ports.LabeledResource{{ID: "c-1", Name: "pg", Session: "mer-1"}}}
	store := &fakeStore{policy: "{}", sessions: []domain.SessionRecord{endedAgo("mer-1", "mer", 10*24*time.Hour)}}
	report := runRuntimeRules(t, store, RunRequest{}, NewContainerRule(docker, fakeProjects{{ID: "mer"}}))
	if len(docker.removed) != 0 {
		t.Fatalf("removed %v while the rule is off", docker.removed)
	}
	if cat := report.Categories[0]; cat.Pending != 1 || cat.Items[0].Status != StatusNeedsConfirmation {
		t.Fatalf("while off: %+v, want the container awaiting opt-in", cat)
	}
}
