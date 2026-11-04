// Package merges builds the cross-lane merge digest: which PRs merged
// recently, which are in flight per lane, and which of them touch the same
// files. It reads shared state only; orchestrators still merge with gh.
package merges

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

const (
	// DefaultWindow is how far back "recently merged" looks.
	DefaultWindow = 24 * time.Hour
	// maxMerged bounds the recently-merged list.
	maxMerged = 10
	// maxSharedPaths bounds the shared paths named in one overlap.
	maxSharedPaths = 5
)

// Store is the persistence surface the digest needs.
type Store interface {
	MergeDigestPRs(ctx context.Context, project domain.ProjectID, mergedSince time.Time) ([]domain.MergeDigestPR, error)
}

// Deps wires a Service. LaneName turns a lane item id into a short label such
// as "B12"; nil or an empty result falls back to the id.
type Deps struct {
	Store    Store
	LaneName func(ctx context.Context, itemID string) string
	Now      func() time.Time
}

// Service builds merge digests.
type Service struct{ d Deps }

// New returns a Service.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{d: d}
}

// MergeEntry is one PR in the digest.
type MergeEntry struct {
	Number   int        `json:"number"`
	URL      string     `json:"url"`
	Title    string     `json:"title"`
	State    string     `json:"state"`
	Lane     string     `json:"lane,omitempty"`
	HeadSHA  string     `json:"headSha,omitempty"`
	Updated  time.Time  `json:"updatedAt"`
	MergedAt *time.Time `json:"mergedAt,omitempty"`
	// Paths are the changed files as last observed (the provider caps them).
	Paths []string `json:"paths"`
}

// MergeOverlap says two PRs touch the same files, with merge-order advice.
type MergeOverlap struct {
	// Number is the PR the advice is about (the one that should wait or rebase).
	Number int `json:"number"`
	// With is the other PR.
	With int `json:"with"`
	// WithMerged is true when With already merged.
	WithMerged bool     `json:"withMerged"`
	CrossLane  bool     `json:"crossLane"`
	Paths      []string `json:"paths"`
	Advice     string   `json:"advice"`
}

// Digest is the answer to `tallpine merges`.
type Digest struct {
	RecentlyMerged []MergeEntry   `json:"recentlyMerged"`
	InFlight       []MergeEntry   `json:"inFlight"`
	Overlaps       []MergeOverlap `json:"overlaps"`
}

// Digest builds the digest for a project.
func (s *Service) Digest(ctx context.Context, project domain.ProjectID) (Digest, error) {
	since := s.d.Now().Add(-DefaultWindow)
	prs, err := s.d.Store.MergeDigestPRs(ctx, project, since)
	if err != nil {
		return Digest{}, err
	}
	names := map[string]string{}
	laneOf := func(id string) string {
		if id == "" {
			return ""
		}
		if n, ok := names[id]; ok {
			return n
		}
		n := id
		if s.d.LaneName != nil {
			if got := s.d.LaneName(ctx, id); got != "" {
				n = got
			}
		}
		names[id] = n
		return n
	}
	out := Digest{RecentlyMerged: []MergeEntry{}, InFlight: []MergeEntry{}, Overlaps: []MergeOverlap{}}
	var merged, flight []domain.MergeDigestPR
	for _, p := range prs {
		if p.State == domain.PRStateMerged {
			merged = append(merged, p)
		} else {
			flight = append(flight, p)
		}
	}
	mergedAt := func(p domain.MergeDigestPR) time.Time {
		if !p.MergedAt.IsZero() {
			return p.MergedAt
		}
		return p.UpdatedAt
	}
	sort.Slice(merged, func(i, j int) bool { return mergedAt(merged[i]).After(mergedAt(merged[j])) })
	if len(merged) > maxMerged {
		merged = merged[:maxMerged]
	}
	// In flight: lane, then PR number (the order to merge same-area PRs in).
	sort.Slice(flight, func(i, j int) bool {
		if li, lj := laneOf(flight[i].LaneItemID), laneOf(flight[j].LaneItemID); li != lj {
			return li < lj
		}
		return flight[i].Number < flight[j].Number
	})
	entry := func(p domain.MergeDigestPR) MergeEntry {
		e := MergeEntry{Number: p.Number, URL: p.URL, Title: p.Title, State: string(p.State), Lane: laneOf(p.LaneItemID),
			HeadSHA: p.HeadSHA, Updated: p.UpdatedAt, Paths: append([]string{}, p.Paths...)}
		if p.State == domain.PRStateMerged {
			t := mergedAt(p)
			e.MergedAt = &t
		}
		return e
	}
	for _, p := range merged {
		out.RecentlyMerged = append(out.RecentlyMerged, entry(p))
	}
	for _, p := range flight {
		out.InFlight = append(out.InFlight, entry(p))
	}
	for i, a := range flight {
		for _, b := range flight[i+1:] {
			shared := sharedPaths(a.Paths, b.Paths)
			if len(shared) == 0 {
				continue
			}
			first, second := a, b // merge the older PR (lower number) first
			if second.Number < first.Number {
				first, second = second, first
			}
			out.Overlaps = append(out.Overlaps, MergeOverlap{
				Number: second.Number, With: first.Number, CrossLane: a.LaneItemID != b.LaneItemID, Paths: shared,
				Advice: "merge #" + strconv.Itoa(first.Number) + " first, then rebase #" + strconv.Itoa(second.Number) + " (same files: " + strings.Join(shared, ", ") + ")",
			})
		}
		for _, m := range merged {
			if shared := sharedPaths(a.Paths, m.Paths); len(shared) > 0 {
				out.Overlaps = append(out.Overlaps, MergeOverlap{
					Number: a.Number, With: m.Number, WithMerged: true, CrossLane: a.LaneItemID != m.LaneItemID, Paths: shared,
					Advice: "#" + strconv.Itoa(m.Number) + " merged touching the same files: rebase #" + strconv.Itoa(a.Number) + " before merging (" + strings.Join(shared, ", ") + ")",
				})
			}
		}
	}
	return out, nil
}

func sharedPaths(a, b []string) []string {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	in := make(map[string]struct{}, len(a))
	for _, p := range a {
		in[p] = struct{}{}
	}
	var shared []string
	for _, p := range b {
		if _, ok := in[p]; ok {
			shared = append(shared, p)
		}
	}
	sort.Strings(shared)
	if len(shared) > maxSharedPaths {
		shared = shared[:maxSharedPaths]
	}
	return shared
}
