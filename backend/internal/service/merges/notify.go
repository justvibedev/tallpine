package merges

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// noticeWindow bounds how old a merge may be and still be announced, so a
// daemon restart that re-observes old merged PRs does not repeat notices.
const noticeWindow = 10 * time.Minute

// Notifier tells orchestrators about merges (B376): the project's main
// orchestrator hears about every lane merge, and the orchestrator of each lane
// whose open PRs touch the same files hears that those PRs must be rebased.
// Send and Orchestrator are optional wiring; without them it does nothing.
type Notifier struct {
	Service *Service
	// Orchestrator returns the live orchestrator receiving a lane's messages
	// ("" = the project's main orchestrator), falling back like report routing.
	Orchestrator func(ctx context.Context, project domain.ProjectID, lane string) (domain.SessionID, bool)
	// Owner returns the live orchestrator that owns a worker (the one that
	// spawned it). The merge FYI goes there and to the main-health owner
	// (Orchestrator("")), never to other orchestrators.
	Owner func(ctx context.Context, project domain.ProjectID, worker domain.SessionID) (domain.SessionID, bool)
	Send  func(ctx context.Context, to domain.SessionID, message string) error

	mu   sync.Mutex
	seen map[string]bool
}

// NotifyMerge announces the merge of prURL. Safe to call more than once per PR.
func (n *Notifier) NotifyMerge(ctx context.Context, project domain.ProjectID, prURL string) {
	if n == nil || n.Service == nil || n.Send == nil || n.Orchestrator == nil {
		return
	}
	n.mu.Lock()
	if n.seen == nil {
		n.seen = map[string]bool{}
	}
	dup := n.seen[prURL]
	n.seen[prURL] = true
	n.mu.Unlock()
	if dup {
		return
	}
	prs, err := n.Service.d.Store.MergeDigestPRs(ctx, project, n.Service.d.Now().Add(-DefaultWindow))
	if err != nil {
		slog.Warn("merge notice: load PRs", "project", project, "err", err)
		return
	}
	for _, t := range n.Service.notices(ctx, project, prs, prURL, func(lane string) (domain.SessionID, bool) {
		return n.Orchestrator(ctx, project, lane)
	}, func(w domain.SessionID) (domain.SessionID, bool) {
		if n.Owner == nil {
			return "", false
		}
		return n.Owner(ctx, project, w)
	}) {
		if err := n.Send(ctx, t.To, t.Text); err != nil {
			slog.Warn("merge notice: send", "to", t.To, "err", err)
		}
	}
}

// Notice is one message to one orchestrator.
type Notice struct {
	To   domain.SessionID
	Text string
}

// notices computes who is told what about the merged PR prURL.
func (s *Service) notices(ctx context.Context, project domain.ProjectID, prs []domain.MergeDigestPR, prURL string, orch func(lane string) (domain.SessionID, bool), owner func(domain.SessionID) (domain.SessionID, bool)) []Notice {
	var m *domain.MergeDigestPR
	for i := range prs {
		if prs[i].URL == prURL && prs[i].State == domain.PRStateMerged {
			m = &prs[i]
		}
	}
	if m == nil || (!m.MergedAt.IsZero() && s.d.Now().Sub(m.MergedAt) > noticeWindow) {
		return nil
	}
	label := func(lane string) string {
		if lane == "" {
			return "main"
		}
		if s.d.LaneName != nil {
			if n := s.d.LaneName(ctx, lane); n != "" {
				return "lane " + n
			}
		}
		return "lane " + lane
	}
	lines := map[domain.SessionID][]string{}
	add := func(lane, line string) {
		if to, ok := orch(lane); ok {
			lines[to] = append(lines[to], line)
		}
	}
	// Worker-controlled text (title, paths) never rides in a trusted [auto] line:
	// the title is dropped and paths are framed as untrusted data.
	head := fmt.Sprintf("[auto] merge: #%d merged on main (%s).", m.Number, label(m.LaneItemID))
	if m.LaneItemID != "" {
		fyi := head + " FYI, no action needed."
		// The merged PR's owning orchestrator and the main-health owner (the
		// default orchestrator, which checks main and rebases overlaps) hear it;
		// one session that is both hears it once. No other orchestrator does.
		own, hasOwner := owner(m.SessionID)
		if hasOwner {
			lines[own] = append(lines[own], fyi)
		}
		if to, ok := orch(""); ok {
			if _, dup := lines[to]; !dup {
				text := fyi
				if hasOwner {
					text = fmt.Sprintf("%s This PR belongs to orchestrator %s, which handles it; no action needed unless main breaks or your own open PRs overlap.", head, own)
				}
				lines[to] = append(lines[to], text)
			}
		}
	}
	for _, f := range prs {
		if f.State == domain.PRStateMerged || f.URL == prURL {
			continue
		}
		// Pure un-laned traffic is unchanged: notices exist for lanes.
		if m.LaneItemID == "" && f.LaneItemID == "" {
			continue
		}
		if shared := sharedPaths(f.Paths, m.Paths); len(shared) > 0 && !s.containsMerge(ctx, project, f, *m) {
			add(f.LaneItemID, fmt.Sprintf("%s Your open PR #%d (%s) touches the same files as the merge (file list below): have its worker rebase on origin/main before it merges.\n%s",
				head, f.Number, label(f.LaneItemID), domain.FrameUntrusted(string(m.SessionID), strings.Join(shared, ", "))))
		}
	}
	var out []Notice
	for to, ls := range lines {
		out = append(out, Notice{To: to, Text: strings.Join(ls, "\n")})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].To < out[j].To })
	return out
}
