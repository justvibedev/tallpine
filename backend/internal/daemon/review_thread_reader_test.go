package daemon

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type recordingThreadFetcher struct{ refs []ports.SCMPRRef }

func (f *recordingThreadFetcher) FetchReviewThreads(_ context.Context, ref ports.SCMPRRef) (ports.SCMReviewObservation, error) {
	f.refs = append(f.refs, ref)
	return ports.SCMReviewObservation{}, nil
}

func TestReviewThreadReaderAddressesTheTrackedPRRepository(t *testing.T) {
	fetcher := &recordingThreadFetcher{}
	reader := reviewThreadReader{provider: fetcher}
	pr := domain.PullRequest{URL: "https://github.com/o/r/pull/7", Number: 7, Provider: "github", Host: "github.com", Repo: "o/r"}
	if _, err := reader.ReadReviewThreads(context.Background(), pr); err != nil {
		t.Fatal(err)
	}
	want := ports.SCMPRRef{Repo: ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "o", Name: "r", Repo: "o/r"}, Number: 7, URL: pr.URL}
	if len(fetcher.refs) != 1 || fetcher.refs[0] != want {
		t.Fatalf("refs = %+v, want %+v", fetcher.refs, want)
	}
	for _, bad := range []domain.PullRequest{
		{URL: "u", Number: 7, Provider: "github", Host: "github.com", Repo: "no-owner"},
		{URL: "u", Number: 7, Repo: "o/r"},
		{URL: "u", Provider: "github", Host: "github.com", Repo: "o/r"},
	} {
		if _, err := reader.ReadReviewThreads(context.Background(), bad); err == nil {
			t.Fatalf("PR without a provider repository was read: %+v", bad)
		}
	}
	if len(fetcher.refs) != 1 {
		t.Fatalf("provider called for an unaddressable PR: %+v", fetcher.refs)
	}
}
