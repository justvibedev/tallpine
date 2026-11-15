package daemon

import (
	"context"
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/process"
)

// skipCommits lists the first-parent commits in base..ref except skip, oldest first.
func (b planBase) skipCommits(ctx context.Context, projectID domain.ProjectID, base string, skip []string) (repo string, commits []string, err error) {
	project, ok, err := b.store.GetProject(ctx, string(projectID))
	if err != nil {
		return "", nil, err
	}
	if !ok {
		return "", nil, fmt.Errorf("project %s not found", projectID)
	}
	res, err := b.resolver.Inspect(ctx, project.Path)
	if err != nil {
		return "", nil, err
	}
	out, err := process.CommandContext(ctx, "git", "-C", project.Path, "rev-list", "--first-parent", "--reverse", base+".."+res.Ref).Output()
	if err != nil {
		return "", nil, err
	}
	skipped := map[string]bool{}
	for _, s := range skip {
		skipped[s] = true
	}
	for _, c := range strings.Fields(string(out)) {
		if !skipped[c] {
			commits = append(commits, c)
		}
	}
	return project.Path, commits, nil
}

// ChangedSinceSkipping lists the files changed by the first-parent commits in
// base..default other than skip.
func (b planBase) ChangedSinceSkipping(ctx context.Context, projectID domain.ProjectID, base string, skip []string) ([]string, error) {
	repo, commits, err := b.skipCommits(ctx, projectID, base, skip)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var files []string
	for _, c := range commits {
		fs, err := gitChangedFiles(ctx, repo, c+"^1", c)
		if err != nil {
			return nil, err
		}
		for _, f := range fs {
			if !seen[f] {
				seen[f] = true
				files = append(files, f)
			}
		}
	}
	return files, nil
}

// ChangedLinesSkipping is ChangedLines over the commits ChangedSinceSkipping
// reads. A path any commit adds, deletes or changes in binary has no entry, so
// callers treat it as touching.
func (b planBase) ChangedLinesSkipping(ctx context.Context, projectID domain.ProjectID, base string, skip, paths []string) (map[string][]string, error) {
	repo, commits, err := b.skipCommits(ctx, projectID, base, skip)
	if err != nil {
		return nil, err
	}
	res := map[string][]string{}
	touching := map[string]bool{}
	for _, c := range commits {
		args := append([]string{"-C", repo, "diff", "-U0", "--no-renames", c + "^1", c, "--"}, paths...)
		out, err := process.CommandContext(ctx, "git", args...).Output()
		if err != nil {
			return nil, err
		}
		got := parseDiffLines(string(out))
		names, err := gitChangedFiles(ctx, repo, c+"^1", c)
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			if _, ok := got[n]; !ok {
				touching[n] = true
			}
		}
		for f, ls := range got {
			res[f] = append(res[f], ls...)
		}
	}
	for f := range touching {
		delete(res, f)
	}
	return res, nil
}
