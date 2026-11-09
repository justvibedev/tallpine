package scm

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/observe"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// dispatchOrder returns observation keys in a deterministic order so lifecycle
// notifications for a session are stable across polls.
func dispatchOrder(observations map[string]ports.SCMObservation, subjectsByPR map[string]*subject) []string {
	keys := make([]string, 0, len(observations))
	for key := range observations {
		keys = append(keys, key)
	}
	sessionOf := func(key string) string {
		if s := subjectsByPR[key]; s != nil {
			return string(s.session.ID)
		}
		return ""
	}
	sort.Slice(keys, func(i, j int) bool {
		if si, sj := sessionOf(keys[i]), sessionOf(keys[j]); si != sj {
			return si < sj
		}
		if ni, nj := observations[keys[i]].PR.Number, observations[keys[j]].PR.Number; ni != nj {
			return ni < nj
		}
		return keys[i] < keys[j]
	})
	return keys
}

func (o *Observer) checkCredentials(ctx context.Context) (bool, error) {
	var probe observe.CredentialProbe
	if checker, ok := o.provider.(credentialChecker); ok {
		probe = checker.SCMCredentialsAvailable
	}
	return observe.CheckCredentialsOnce(ctx, probe, &o.credentialsChecked, &o.disabled, o.logger, "scm observer")
}

// discoverSubjects builds the per-PR refresh subjects (normally one per open
// tracked PR)
// and the per-session repo list used for branch-prefix discovery of new PRs. A
// session may own several PRs, so each open tracked PR becomes its own subject.
// Terminal PRs stay eligible only for an opted-in live session, allowing an
// unacknowledged teardown failure to be delivered again on a later poll.
func (o *Observer) discoverSubjects(ctx context.Context) (map[string]*subject, []sessionRepo, error) {
	sessions, err := o.store.ListLiveSessions(ctx)
	if err != nil {
		return nil, nil, err
	}
	// ended marks sessions kept only so their open PRs are refreshed: they
	// never discover new PRs and never keep terminal PRs tracked.
	ended := map[domain.SessionID]bool{}
	if lister, ok := o.store.(endedPRSessionLister); ok {
		extra, err := lister.ListEndedSessionsWithOpenBacklogPRs(ctx)
		if err != nil {
			return nil, nil, err
		}
		for _, sess := range extra {
			ended[sess.ID] = true
			sessions = append(sessions, sess)
		}
	}
	projects := map[domain.ProjectID]domain.ProjectRecord{}
	scanRepos := map[domain.ProjectID][]ports.SCMRepo{}
	out := map[string]*subject{}
	var sessionRepos []sessionRepo
	for _, sess := range sessions {
		branch := strings.TrimSpace(sess.Metadata.Branch)
		if branch == "" {
			continue
		}
		proj, ok := projects[sess.ProjectID]
		if !ok {
			p, found, err := o.store.GetProject(ctx, string(sess.ProjectID))
			if err != nil {
				return nil, nil, err
			}
			if !found || !p.ArchivedAt.IsZero() {
				continue
			}
			// Backfill an empty origin and follow a renamed/moved one: the
			// checkout's live origin wins over a stale stored URL so PR URLs
			// from the new repo name keep linking to the project.
			if p.Path != "" {
				if url := resolveGitOriginURL(p.Path); url != "" && url != p.RepoOriginURL && canAdoptOrigin(p.RepoOriginURL, url) {
					if p.RepoOriginURL != "" {
						o.logger.Info("scm observer: project origin URL followed rename", "project", p.ID, "old", p.RepoOriginURL, "new", url)
					}
					p.RepoOriginURL = url
					if err := o.store.UpsertProject(ctx, p); err != nil {
						o.logger.Warn("scm observer: backfill origin URL persist failed", "project", p.ID, "err", err)
					}
				}
			}
			projects[sess.ProjectID] = p
			proj = p
			// Prefer the tracked (possibly moved) repo, but a canonical URL no
			// provider handles must not silence a healthy origin.
			origin, ok := o.provider.ParseRepository(p.Config.TrackedRepoURL(p.RepoOriginURL))
			if !ok {
				origin, ok = o.provider.ParseRepository(p.RepoOriginURL)
			}
			if ok {
				scanRepos[sess.ProjectID] = o.resolveScanRepos(ctx, p, origin)
			}
		}
		repos := append([]ports.SCMRepo(nil), scanRepos[sess.ProjectID]...)
		for _, sr := range checkoutSessionRepos(sess, branch, proj.Kind.WithDefault() == domain.ProjectKindWorkspace, repos) {
			sr.trackOnly = ended[sess.ID]
			sessionRepos = append(sessionRepos, sr)
		}
		childRepos, err := o.workspaceSCMSessionRepos(ctx, proj, sess, branch)
		if err != nil {
			return nil, nil, err
		}
		for _, child := range childRepos {
			child.trackOnly = ended[sess.ID]
			sessionRepos = append(sessionRepos, child)
			repos = append(repos, child.repo)
		}
		if len(repos) == 0 {
			o.logger.Debug("scm observer: project has no supported SCM origins", "project", proj.ID)
			continue
		}
		prs, err := o.store.ListPRsBySession(ctx, sess.ID)
		if err != nil {
			return nil, nil, err
		}
		tracked := trackedPRsForSession(sess, prs)
		if ended[sess.ID] {
			tracked = openTrackedPRs(prs)
		}
		for _, pr := range tracked {
			prRepo, ok := repoForTrackedPR(pr, repos)
			if !ok {
				o.logger.Warn("scm observer: tracked PR repo no longer belongs to project", "session", sess.ID, "pr", pr.URL, "repo", pr.Repo)
				continue
			}
			// Provider-native identity keeps this subject's key stable across
			// repository renames; legacy id-less rows retain the name-based key
			// until their first successful detail fetch stamps an identity.
			key := keyForTrackedPR(pr, prRepo)
			if existing, ok := out[key]; ok {
				o.logger.Warn("scm observer: duplicate tracked PR subject skipped", "pr", key, "kept_session", existing.session.ID, "skipped_session", sess.ID)
				continue
			}
			out[key] = &subject{session: sess, repo: prRepo, branch: branch, known: pr, hasPR: true}
		}
	}
	return out, sessionRepos, nil
}

// The stored origin remains usable when the checkout is temporarily unavailable.
func (o *Observer) resolveScanRepos(ctx context.Context, proj domain.ProjectRecord, origin ports.SCMRepo) []ports.SCMRepo {
	repos := []ports.SCMRepo{origin}
	if strings.TrimSpace(proj.Path) == "" {
		return repos
	}
	seen := map[string]bool{strings.ToLower(prKey(origin, 0)): true}
	for _, url := range gitRemoteURLsFunc(ctx, proj.Path) {
		repo, ok := o.provider.ParseRepository(url)
		if !ok {
			continue
		}
		key := strings.ToLower(prKey(repo, 0))
		if seen[key] {
			continue
		}
		seen[key] = true
		repos = append(repos, repo)
	}
	return repos
}

func checkoutSessionRepos(sess domain.SessionRecord, branch string, workspace bool, checkouts ...[]ports.SCMRepo) []sessionRepo {
	var result []sessionRepo
	byBase := make(map[string]int)
	for _, checkout := range checkouts {
		for _, base := range checkout {
			key := strings.ToLower(prKey(base, 0))
			position, exists := byBase[key]
			if !exists {
				position = len(result)
				byBase[key] = position
				result = append(result, sessionRepo{
					session: sess, repo: base, branch: branch, workspace: workspace,
					headNames: make(map[string]struct{}),
				})
			}
			for _, destination := range checkout {
				if !strings.EqualFold(base.Provider, destination.Provider) || !strings.EqualFold(base.Host, destination.Host) {
					continue
				}
				result[position].headNames[strings.ToLower(repoFullName(destination))] = struct{}{}
			}
		}
	}
	return result
}

func (o *Observer) workspaceSCMSessionRepos(ctx context.Context, proj domain.ProjectRecord, sess domain.SessionRecord, branch string) ([]sessionRepo, error) {
	if proj.Kind.WithDefault() != domain.ProjectKindWorkspace {
		return nil, nil
	}
	childRepos, err := o.store.ListWorkspaceRepos(ctx, proj.ID)
	if err != nil {
		return nil, err
	}
	var checkouts [][]ports.SCMRepo
	for _, child := range childRepos {
		if strings.TrimSpace(child.RepoOriginURL) == "" {
			continue
		}
		repo, ok := o.provider.ParseRepository(child.RepoOriginURL)
		if !ok {
			o.logger.Debug("scm observer: unsupported SCM origin", "project", proj.ID, "repo", child.Name, "origin", child.RepoOriginURL)
			continue
		}
		childPath := filepath.Join(proj.Path, filepath.FromSlash(child.RelativePath))
		checkouts = append(checkouts, o.resolveScanRepos(ctx, domain.ProjectRecord{Path: childPath}, repo))
	}
	return checkoutSessionRepos(sess, branch, true, checkouts...), nil
}

func repoForTrackedPR(pr domain.PullRequest, repos []ports.SCMRepo) (ports.SCMRepo, bool) {
	if pr.Provider != "" && pr.Host != "" && pr.Repo != "" {
		owner, name, ok := strings.Cut(pr.Repo, "/")
		if !ok || owner == "" || name == "" {
			return ports.SCMRepo{}, false
		}
		return ports.SCMRepo{Provider: pr.Provider, Host: pr.Host, Owner: owner, Name: name, Repo: pr.Repo}, true
	}
	if pr.Repo != "" {
		for _, repo := range repos {
			if matchesTrackedPRRepo(pr, repo) {
				return repo, true
			}
		}
		return ports.SCMRepo{}, false
	}
	if len(repos) == 1 {
		return repos[0], true
	}
	for _, repo := range repos {
		if strings.EqualFold(repo.Repo, repos[0].Repo) {
			return repo, true
		}
	}
	return repos[0], len(repos) > 0
}

func matchesTrackedPRRepo(pr domain.PullRequest, repo ports.SCMRepo) bool {
	if pr.Provider != "" && !strings.EqualFold(pr.Provider, repo.Provider) {
		return false
	}
	if pr.Host != "" && !strings.EqualFold(pr.Host, repo.Host) {
		return false
	}
	if pr.Repo != "" && !domain.SameRepoPath(pr.Repo, repoFullName(repo)) {
		return false
	}
	return true
}

func openTrackedPRs(prs []domain.PullRequest) []domain.PullRequest {
	out := make([]domain.PullRequest, 0, len(prs))
	for _, pr := range prs {
		if pr.Number > 0 && !pr.Merged && !pr.Closed {
			out = append(out, pr)
		}
	}
	return out
}

// trackedPRsForSession keeps terminal PRs discoverable only while an opted-in
// session remains live. If merge-driven teardown fails, the observer has not
// acknowledged the terminal semantic hash, so a later poll can redeliver it.
func trackedPRsForSession(sess domain.SessionRecord, prs []domain.PullRequest) []domain.PullRequest {
	if !sess.TerminateOnPRMerge {
		return openTrackedPRs(prs)
	}
	out := make([]domain.PullRequest, 0, len(prs))
	for _, pr := range prs {
		if pr.Number > 0 {
			out = append(out, pr)
		}
	}
	return out
}

func (o *Observer) logProviderRequest(stage, repository string, startedAt time.Time, err error) {
	completedAt := time.Now()
	duration := completedAt.Sub(startedAt)
	attrs := []any{
		"stage", stage,
		"repository", repository,
		"started_at", startedAt.UTC(),
		"completed_at", completedAt.UTC(),
		"duration_ms", duration.Milliseconds(),
	}
	if err != nil {
		attrs = append(attrs, "err", err)
	}
	if duration >= o.tick {
		o.logger.Warn("scm observer: slow provider request completed", attrs...)
		return
	}
	o.logger.Debug("scm observer: provider request completed", attrs...)
}

func repoNames(refs []ports.SCMPRRef) string {
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		seen[repoFullName(ref.Repo)] = struct{}{}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

func (o *Observer) guardRepos(ctx context.Context, sessionRepos []sessionRepo) map[string]repoGuardState {
	repos := map[string]ports.SCMRepo{}
	for _, sr := range sessionRepos {
		repos[prKey(sr.repo, 0)] = sr.repo
	}
	out := map[string]repoGuardState{}
	for key, repo := range repos {
		prev, had := o.Cache.RepoPRListETag[key]
		started := time.Now()
		res, err := o.provider.RepoPRListGuard(ctx, repo, prev)
		o.logProviderRequest("repo_pr_list_guard", repoFullName(repo), started, err)
		if err != nil {
			if o.guardFailLogged[key] != err.Error() {
				o.guardFailLogged[key] = err.Error()
				o.logger.Error("scm observer: repo PR-list guard failed", "repo", repoFullName(repo), "err", err)
			} else {
				o.logger.Debug("scm observer: repo PR-list guard still failing", "repo", repoFullName(repo), "err", err)
			}
			out[key] = repoGuardState{hadETag: had, err: err}
			continue
		}
		delete(o.guardFailLogged, key)
		out[key] = repoGuardState{result: res, hadETag: had}
	}
	return out
}

func pendingRepoRefreshes(guards map[string]repoGuardState) map[string]bool {
	out := map[string]bool{}
	for key, g := range guards {
		if g.err == nil && g.result.ETag != "" {
			out[key] = true
		}
	}
	return out
}
