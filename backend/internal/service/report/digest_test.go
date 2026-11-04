package report

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type flushStore struct {
	fakeStore
	calls []int
}

func (f *flushStore) FlushQuietReports(_ context.Context, _ domain.ProjectID, n int, _ time.Time) (int, error) {
	f.calls = append(f.calls, n)
	return 0, nil
}

func TestQuietReportRequestsDigestFlushOnlyWhenEnabled(t *testing.T) {
	for _, tc := range []struct {
		name  string
		count int
		quiet bool
		want  []int
	}{
		{"off by default", 0, true, nil},
		{"enabled quiet report", 3, true, []int{3}},
		{"loud report never flushes", 3, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &flushStore{fakeStore: fakeStore{ok: true, session: domain.SessionRecord{ID: "tp-7", ProjectID: "tp", Kind: domain.KindWorker}}}
			s := New(Deps{Store: st, DigestCount: func(context.Context, domain.ProjectID) int { return tc.count }})
			in := CreateInput{SessionID: "tp-7", State: domain.ReportCheckpoint, Note: "x", Quiet: tc.quiet}
			if !tc.quiet {
				in.State = domain.ReportNeedsInput
			}
			if _, err := s.Create(context.Background(), in); err != nil {
				t.Fatal(err)
			}
			if len(st.calls) != len(tc.want) || (len(tc.want) > 0 && st.calls[0] != tc.want[0]) {
				t.Fatalf("flush calls=%v want %v", st.calls, tc.want)
			}
		})
	}
}
