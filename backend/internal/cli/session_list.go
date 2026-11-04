package cli

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

type sessionListEntry struct {
	ID             string          `json:"id"`
	ProjectID      string          `json:"projectId"`
	Role           string          `json:"role"`
	DisplayName    string          `json:"displayName,omitempty"`
	Status         string          `json:"status,omitempty"`
	Activity       string          `json:"activity,omitempty"`
	PausedUntil    *time.Time      `json:"pausedUntil,omitempty"`
	IssueID        string          `json:"issueId,omitempty"`
	Harness        string          `json:"harness,omitempty"`
	Branch         string          `json:"branch,omitempty"`
	PRs            []sessionListPR `json:"prs"`
	IsTerminated   bool            `json:"isTerminated"`
	LastActivityAt *time.Time      `json:"lastActivityAt,omitempty"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
}

type sessionListPR struct {
	Number  int    `json:"number"`
	CI      string `json:"ci,omitempty"`
	Review  string `json:"review,omitempty"`
	Threads *int   `json:"threads"`
}

type sessionListOutput struct {
	Data []sessionListEntry `json:"data"`
	Meta struct {
		HiddenTerminatedCount   int `json:"hiddenTerminatedCount"`
		HiddenOrchestratorCount int `json:"hiddenOrchestratorCount"`
	} `json:"meta"`
}

func (c *commandContext) listSessions(ctx context.Context, cmd *cobra.Command, opts sessionListOptions) error {
	params := url.Values{}
	if opts.project != "" {
		params.Set("project", opts.project)
	}
	if !opts.includeTerminated {
		params.Set("active", "true")
	}
	var res apitypes.ListSessionsResponse
	if err := c.getJSON(ctx, apiPath("sessions", params), &res); err != nil {
		return err
	}
	hiddenOrchestratorCount := 0
	if !opts.all {
		for _, sess := range res.Sessions {
			if sess.Kind == "orchestrator" {
				hiddenOrchestratorCount++
			}
		}
	}
	sessions := filterAndSortSessions(res.Sessions, opts.all)
	hiddenTerminatedCount := 0
	if !opts.includeTerminated {
		terminatedCount, terminatedOrchestratorCount, err := c.countHiddenTerminated(ctx, opts.project, opts.all)
		if err != nil {
			return err
		}
		hiddenTerminatedCount = terminatedCount
		hiddenOrchestratorCount += terminatedOrchestratorCount
	}
	prSummaries, err := c.listSessionPRSummaries(ctx, sessions)
	if err != nil {
		return err
	}
	if opts.json {
		out := sessionListOutput{Data: sessionListEntries(sessions, prSummaries)}
		out.Meta.HiddenTerminatedCount = hiddenTerminatedCount
		out.Meta.HiddenOrchestratorCount = hiddenOrchestratorCount
		return writeJSON(cmd.OutOrStdout(), out)
	}
	return writeSessionList(cmd, sessions, prSummaries, hiddenTerminatedCount, hiddenOrchestratorCount, c.deps.Now())
}

// listSessionPRSummaries fetches rich PR facts concurrently from the daemon.
// Individual summary failures are intentionally non-fatal: the list response
// still contains the persisted PR/CI/review facts and remains useful while an
// SCM observation is incomplete or unavailable.
func (c *commandContext) listSessionPRSummaries(ctx context.Context, sessions []apitypes.SessionView) (map[string][]apitypes.SessionPRSummary, error) {
	out := make(map[string][]apitypes.SessionPRSummary)
	var mu sync.Mutex
	var wg sync.WaitGroup
	// Keep the loopback daemon responsive when a project has many sessions while
	// still resolving independent session summaries in parallel.
	sem := make(chan struct{}, 8)
	for _, sess := range sessions {
		if len(sess.PRs) == 0 {
			continue
		}
		id := sess.ID
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			var res apitypes.ListSessionPRsResponse
			if err := c.getJSON(ctx, "sessions/"+url.PathEscape(string(id))+"/pr", &res); err != nil {
				return
			}
			mu.Lock()
			out[string(id)] = res.PRs
			mu.Unlock()
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *commandContext) countHiddenTerminated(ctx context.Context, project string, includeOrchestrators bool) (int, int, error) {
	params := url.Values{}
	if project != "" {
		params.Set("project", project)
	}
	params.Set("active", "false")
	var res apitypes.ListSessionsResponse
	if err := c.getJSON(ctx, apiPath("sessions", params), &res); err != nil {
		return 0, 0, err
	}
	hiddenOrchestratorCount := 0
	if !includeOrchestrators {
		for _, sess := range res.Sessions {
			if sess.Kind == "orchestrator" {
				hiddenOrchestratorCount++
			}
		}
	}
	return len(filterAndSortSessions(res.Sessions, includeOrchestrators)), hiddenOrchestratorCount, nil
}

func filterAndSortSessions(sessions []apitypes.SessionView, includeOrchestrators bool) []apitypes.SessionView {
	out := make([]apitypes.SessionView, 0, len(sessions))
	for _, sess := range sessions {
		if !includeOrchestrators && sess.Kind == "orchestrator" {
			continue
		}
		out = append(out, sess)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ProjectID != out[j].ProjectID {
			return out[i].ProjectID < out[j].ProjectID
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func sessionListEntries(sessions []apitypes.SessionView, summaries map[string][]apitypes.SessionPRSummary) []sessionListEntry {
	entries := make([]sessionListEntry, 0, len(sessions))
	for _, sess := range sessions {
		var last *time.Time
		if !sess.Activity.LastActivityAt.IsZero() {
			activity := sess.Activity.LastActivityAt
			last = &activity
		}
		entries = append(entries, sessionListEntry{
			ID:             string(sess.ID),
			ProjectID:      string(sess.ProjectID),
			Role:           sessionRole(sess),
			DisplayName:    sess.DisplayName,
			Status:         string(sess.Status),
			Activity:       activityWithReauth(activityWithPause(activityWithWaiting(string(sess.Activity.State), sess.WaitingOn), sess.PausedUntil), sess.ReauthRequired),
			PausedUntil:    sess.PausedUntil,
			IssueID:        string(sess.IssueID),
			Harness:        string(sess.Harness),
			Branch:         sess.Branch,
			PRs:            listPRsForSession(sess, summaries[string(sess.ID)]),
			IsTerminated:   sess.IsTerminated,
			LastActivityAt: last,
			CreatedAt:      sess.CreatedAt,
			UpdatedAt:      sess.UpdatedAt,
		})
	}
	return entries
}

func listPRsForSession(sess apitypes.SessionView, summaries []apitypes.SessionPRSummary) []sessionListPR {
	if len(summaries) > 0 {
		out := make([]sessionListPR, 0, len(summaries))
		for _, pr := range summaries {
			// Threads stays nil (rendered as "-") when the daemon did not
			// publish a count: an older daemon or an only-partially observed
			// thread snapshot must read as unknown, not zero.
			out = append(out, sessionListPR{Number: pr.Number, CI: string(pr.CI.State), Review: string(pr.Review.Decision), Threads: pr.Review.UnresolvedThreadCount})
		}
		return out
	}

	// Preserve useful list facts when the more detailed endpoint failed or its
	// snapshot has already been replaced.
	out := make([]sessionListPR, 0, len(sess.PRs))
	for _, pr := range sess.PRs {
		out = append(out, sessionListPR{Number: pr.Number, CI: string(pr.CI), Review: string(pr.Review)})
	}
	return out
}

func writeSessionList(cmd *cobra.Command, sessions []apitypes.SessionView, summaries map[string][]apitypes.SessionPRSummary, hiddenTerminatedCount, hiddenOrchestratorCount int, now time.Time) error {
	out := cmd.OutOrStdout()
	if len(sessions) == 0 {
		if _, err := fmt.Fprintln(out, "(no active sessions)"); err != nil {
			return err
		}
	} else {
		table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		currentProject := ""
		for _, sess := range sessions {
			if string(sess.ProjectID) != currentProject {
				if currentProject != "" {
					if _, err := fmt.Fprintln(table); err != nil {
						return err
					}
				}
				currentProject = string(sess.ProjectID)
				if _, err := fmt.Fprintf(table, "%s:\n", currentProject); err != nil {
					return err
				}
				if _, err := fmt.Fprintln(table, "  SESSION\tNAME\tROLE\tBRANCH\tPR\tCI\tREVIEW\tTHREADS\tACTIVITY\tAGE"); err != nil {
					return err
				}
			}
			pr, ci, review, threads := sessionPRColumns(sess, summaries[string(sess.ID)])
			if _, err := fmt.Fprintf(table, "  %s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", sess.ID, truncateName(sess.DisplayName, 28), sessionRole(sess), emptyDash(sess.Branch), pr, ci, review, threads, emptyDash(string(sess.Activity.State)), sessionAge(now, sess.Activity.LastActivityAt)); err != nil {
				return err
			}
		}
		if err := table.Flush(); err != nil {
			return err
		}
	}
	if hiddenTerminatedCount > 0 {
		if _, err := fmt.Fprintf(out, "%d terminated session%s hidden. Use --include-terminated to show.\n", hiddenTerminatedCount, pluralS(hiddenTerminatedCount)); err != nil {
			return err
		}
	}
	if hiddenOrchestratorCount > 0 {
		_, err := fmt.Fprintf(out, "%d orchestrator session%s hidden. Use --all or `tallpine orchestrator ls` to show.\n", hiddenOrchestratorCount, pluralS(hiddenOrchestratorCount))
		return err
	}
	return nil
}

func sessionPRColumns(sess apitypes.SessionView, summaries []apitypes.SessionPRSummary) (prNumbers, ci, review, threads string) {
	prs := listPRsForSession(sess, summaries)
	if len(prs) == 0 {
		return "-", "-", "-", "0"
	}
	prValues := make([]string, 0, len(prs))
	ciValues := make([]string, 0, len(prs))
	reviewValues := make([]string, 0, len(prs))
	threadCount := 0
	threadsKnown := true
	for _, pr := range prs {
		prValues = append(prValues, fmt.Sprintf("#%d", pr.Number))
		ciValues = append(ciValues, emptyDash(pr.CI))
		reviewValues = append(reviewValues, emptyDash(pr.Review))
		if pr.Threads == nil {
			threadsKnown = false
			continue
		}
		threadCount += *pr.Threads
	}
	threads = "-"
	if threadsKnown {
		threads = fmt.Sprintf("%d", threadCount)
	}
	return strings.Join(prValues, ","), strings.Join(ciValues, ","), strings.Join(reviewValues, ","), threads
}

func emptyDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func sessionAge(now, at time.Time) string {
	if at.IsZero() {
		return "-"
	}
	return formatSessionAge(now.Sub(at)) + " ago"
}

// activityWithWaiting marks an idle session that waits on harness background
// tasks, so it reads as waiting rather than stalled.
// activityWithPause appends the end of a provider quota pause to an activity.
func activityWithPause(activity string, pausedUntil *time.Time) string {
	if pausedUntil == nil {
		return activity
	}
	return activity + " (paused until " + pausedUntil.Local().Format("15:04") + ")"
}

func activityWithWaiting(state string, waiting []string) string {
	if len(waiting) == 0 || state == "active" || state == "exited" {
		return state
	}
	return state + " (waiting on task " + strings.Join(waiting, ", ") + ")"
}

// truncateName shortens a session name to max runes for the list table.
func truncateName(name string, limit int) string {
	r := []rune(strings.TrimSpace(name))
	if len(r) == 0 {
		return "-"
	}
	if len(r) > limit {
		return string(r[:limit-1]) + "…"
	}
	return string(r)
}
