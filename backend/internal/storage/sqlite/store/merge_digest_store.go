package store

import (
	"context"
	"fmt"
	"sort"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// MergeDigestPRs returns a project's open and draft PRs plus those merged
// since the given time, each with its lane and changed paths.
func (s *Store) MergeDigestPRs(ctx context.Context, project domain.ProjectID, mergedSince time.Time) ([]domain.MergeDigestPR, error) {
	rows, err := s.qr.ListMergeDigestPRs(ctx, gen.ListMergeDigestPRsParams{ProjectID: &project, MergedSince: mergedSince.UTC()})
	if err != nil {
		return nil, fmt.Errorf("list merge digest prs for %s: %w", project, err)
	}
	paths, err := s.mergeDigestPaths(ctx, rows)
	if err != nil {
		return nil, fmt.Errorf("list merge digest paths for %s: %w", project, err)
	}
	out := make([]domain.MergeDigestPR, 0, len(rows))
	for _, r := range rows {
		p := domain.MergeDigestPR{
			URL: r.URL, Number: int(r.Number), State: r.PRState, Title: r.Title, HeadSHA: r.HeadSha, MergeCommitSHA: r.MergeCommitSha,
			SessionID: r.SessionID, LaneItemID: r.LaneItemID, UpdatedAt: r.UpdatedAt, Paths: paths[r.URL],
		}
		if r.MergedAtProvider.Valid {
			p.MergedAt = r.MergedAtProvider.Time
		}
		sort.Strings(p.Paths)
		out = append(out, p)
	}
	return out, nil
}

// mergeDigestPathChunk keeps one IN list far below SQLite's bound-variable limit.
const mergeDigestPathChunk = 500

// mergeDigestPaths fetches the changed paths of exactly the PRs the digest
// query returned, so the PR filter is written once.
func (s *Store) mergeDigestPaths(ctx context.Context, prs []gen.ListMergeDigestPRsRow) (map[string][]string, error) {
	paths := map[string][]string{}
	for start := 0; start < len(prs); start += mergeDigestPathChunk {
		end := min(start+mergeDigestPathChunk, len(prs))
		urls := make([]string, 0, end-start)
		for _, r := range prs[start:end] {
			urls = append(urls, r.URL)
		}
		rows, err := s.qr.ListPRPathsByURLs(ctx, urls)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			paths[r.PRURL] = append(paths[r.PRURL], r.Path)
		}
	}
	return paths, nil
}
