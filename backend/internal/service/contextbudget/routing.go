package contextbudget

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

const (
	minSearchChars = 4
	maxTitleChars  = 80
	patchContext   = 3
	// searchLookback is how many tool rows after a search a doc read still counts as a result of it.
	searchLookback = 5
	// searchCostTokens estimates what one session spends searching for a doc it was not pointed at.
	searchCostTokens = 2000
	// routingBudget is the "budget" of a routing finding. The count of sessions
	// is its measure; a No holds until the count grows by a tenth of this, so
	// one more session does not bring the card back.
	routingBudget = 10 * domain.ContextRoutingMinSessions
)

// ActivityStore reads the tool rows of a project's sessions.
type ActivityStore interface {
	ListToolActivity(ctx context.Context, project domain.ProjectID, since time.Time) ([]store.ToolActivity, error)
	CountActiveSessions(ctx context.Context, project domain.ProjectID, since time.Time) (int, error)
}

// RoutingFinder finds docs that several sessions read, and searches several
// sessions repeat, because the instruction files do not point at them. Its one
// finding is the week's digest of those gaps (B837): a card per gap showed only
// a search string and was mostly false. The digest carries the evidence per
// gap and a deterministic baseline patch; the weekly analyst may refine it.
type RoutingFinder struct {
	Store  ActivityStore
	Reader func(ctx context.Context, proj domain.ProjectRecord) (Source, error)
	Now    func() time.Time
	// ArtifactRoot holds the declined gaps; empty keeps no memory of a No.
	ArtifactRoot string
}

// Find implements Finder: one digest finding when there are gaps, none otherwise.
func (r RoutingFinder) Find(ctx context.Context, proj domain.ProjectRecord, m Measurement) ([]domain.ContextHealthFinding, error) {
	gs, cov, err := r.gaps(ctx, proj, m)
	if err != nil {
		return nil, err
	}
	kept := r.withoutDeclined(proj, gs)
	if len(kept) == 0 {
		return nil, nil
	}
	return []domain.ContextHealthFinding{digest(kept, cov)}, nil
}

func (r RoutingFinder) gaps(ctx context.Context, proj domain.ProjectRecord, m Measurement) ([]gap, string, error) {
	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	since := now().Add(-domain.ContextRoutingWindow)
	rows, err := r.Store.ListToolActivity(ctx, domain.ProjectID(proj.ID), since)
	if err != nil {
		return nil, "", fmt.Errorf("list tool activity: %w", err)
	}
	coverage := r.coverage(ctx, proj, rows, since)
	docs, searches := map[string]gapStats{}, map[string]gapStats{}
	note := func(into map[string]gapStats, key string, s domain.SessionID, calls int, searched, reads []string) {
		if into[key] == nil {
			into[key] = gapStats{}
		}
		into[key].add(s, calls, searched, reads)
	}
	recent := map[domain.SessionID][]recentRow{}
	// Rows come newest first; walk them oldest first so a read sees what came before it.
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		reads, searched := parseActivity(row, proj.Path)
		// A read counts only when the session was looking for it: a search that
		// names the doc, or other docs opened first. A doc opened by its path,
		// or after a search for something else, was found without help.
		for _, d := range reads {
			if w := recent[row.SessionID]; lookedFor(w, d) {
				var qs []string
				for _, x := range w {
					qs = append(qs, x.queries...)
				}
				note(docs, d, row.SessionID, len(w)+1, qs, []string{d})
			}
		}
		recent[row.SessionID] = append(recent[row.SessionID], recentRow{search: searchText(row), reads: reads, queries: searched})
		if w := recent[row.SessionID]; len(w) > searchLookback {
			recent[row.SessionID] = w[len(w)-searchLookback:]
		}
		// A search counts only when it led to a doc: a code symbol looked up
		// and found in source is no fact a pointer could hold.
		if len(reads) > 0 {
			w := recent[row.SessionID]
			var got []string
			for _, r := range w {
				got = append(got, r.reads...)
			}
			for _, r := range w {
				for _, q := range r.queries {
					note(searches, q, row.SessionID, len(w), []string{q}, got)
				}
			}
		}
	}
	var out []gap
	hot, hotSearches := repeated(docs, domain.ContextRoutingMinSessions), repeated(searches, searchMinSessions)
	if len(hot)+len(hotSearches) > 0 {
		src, err := r.Reader(ctx, proj)
		if err != nil {
			return nil, "", err
		}
		inst := readInstructionFiles(ctx, src, m)
		have := map[string]bool{}
		for _, d := range hot {
			if f, ok := docFinding(ctx, src, inst, m, d, docs[d], coverage); ok {
				out, have[d] = append(out, f), true
			}
		}
		for _, q := range hotSearches {
			if f, ok := searchFinding(ctx, src, inst, m, q, searches[q], coverage); ok && !have[f.doc] {
				out, have[f.doc] = append(out, f), true
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].MeasuredTokens > out[j].MeasuredTokens })
	return out, coverage, nil
}

// coverage says how much of the project's session activity the finding rests
// on: a session with no recorded tool rows (a terminal-mode one) is invisible,
// so the count of sessions in a finding can understate. "" when unknown.
func (r RoutingFinder) coverage(ctx context.Context, proj domain.ProjectRecord, rows []store.ToolActivity, since time.Time) string {
	total, err := r.Store.CountActiveSessions(ctx, domain.ProjectID(proj.ID), since)
	if err != nil || total == 0 {
		return ""
	}
	seen := map[domain.SessionID]bool{}
	for _, row := range rows {
		seen[row.SessionID] = true
	}
	return fmt.Sprintf("\nCoverage: tool activity was recorded for %d of %d sessions active in these 7 days; sessions without recorded activity (terminal mode, say) are not counted above.\n", len(seen), total)
}

// repeated returns the keys seen in enough distinct sessions, busiest first.
func repeated(by map[string]gapStats, minSessions int) []string {
	var keys []string
	for k, s := range by {
		if len(s) >= minSessions {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if a, b := len(by[keys[i]]), len(by[keys[j]]); a != b {
			return a > b
		}
		return keys[i] < keys[j]
	})
	return keys
}

// parseActivity returns the repo-relative docs a row read and the searches it
// ran. Rows without the detail it needs yield nothing.
func parseActivity(row store.ToolActivity, projectPath string) (reads, searches []string) {
	switch {
	case row.ToolKind == "read":
		var locs []struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(row.Locations), &locs) != nil {
			return nil, nil
		}
		for _, l := range locs {
			if p, ok := repoDoc(l.Path, projectPath); ok {
				reads = append(reads, p)
			}
		}
	case row.ToolKind == "search":
		var in map[string]any
		if json.Unmarshal([]byte(row.Input), &in) != nil {
			return nil, nil
		}
		if isGlob(row.ProviderToolName, in) {
			return nil, nil
		}
		if p, _ := in["path"].(string); p != "" && !repoPath(p, projectPath) {
			return nil, nil // a log or tmp file, not the repo
		}
		for _, key := range []string{"pattern", "query", "regex", "search"} {
			if q, _ := in[key].(string); searchKey(q) != "" {
				return nil, []string{searchKey(q)}
			}
		}
	case row.Command != "":
		for _, seg := range shellPipeline(row.Command) {
			r, s := parseSegment(seg, projectPath)
			reads, searches = append(reads, r...), append(searches, s...)
		}
	}
	return reads, searches
}

// recentRow is what a session's earlier tool row looked for or opened.
type recentRow struct {
	search string   // lower-cased text of a search row, "" for other rows
	reads  []string // docs the row read
	// queries are the repo searches the row ran.
	queries []string
}

// searchText is what a grep, glob or find row looked for, lower-cased: the
// pattern, query or glob values only, never option keys, paths or other
// settings. "" when the row is no search.
func searchText(row store.ToolActivity) string {
	var parts []string
	if row.ToolKind == "search" {
		var in map[string]any
		if json.Unmarshal([]byte(row.Input), &in) != nil {
			return ""
		}
		for _, key := range []string{"pattern", "query", "regex", "search", "glob"} {
			if q, _ := in[key].(string); q != "" {
				parts = append(parts, q)
			}
		}
	}
	for _, seg := range shellSegments(row.Command) {
		parts = append(parts, segmentTargets(seg)...)
	}
	return strings.ToLower(strings.Join(parts, "\n"))
}

// lookedFor reports whether the session's recent rows show it hunting for doc:
// a search that names the doc's file name, or at least two other docs read first.
func lookedFor(recent []recentRow, doc string) bool {
	name := strings.ToLower(strings.TrimSuffix(path.Base(doc), path.Ext(doc)))
	others := map[string]bool{}
	for _, r := range recent {
		if r.search != "" && len(name) >= minSearchChars && strings.Contains(r.search, name) {
			return true
		}
		for _, d := range r.reads {
			if d != doc && !routedByName(d) {
				others[d] = true
			}
		}
	}
	return len(others) >= 2
}

// routedByName are docs sessions open by name; a pointer to them saves nothing.
func routedByName(doc string) bool {
	switch strings.ToUpper(strings.TrimSuffix(path.Base(doc), path.Ext(doc))) {
	case "CHANGELOG", "README":
		return true
	}
	return false
}

// isGlob tells a file-name glob from a content search. ACP reports both as kind
// "search" with a "pattern". The provider's tool name settles it; without one,
// a Grep usually carries options (output_mode, glob, -n, ...) and a Glob only
// the pattern and a directory. A glob in several sessions is no fact a pointer
// could hold.
func isGlob(tool string, in map[string]any) bool {
	switch tool {
	case "Glob":
		return true
	case "Grep":
		return false
	}
	if _, ok := in["pattern"]; !ok {
		return false
	}
	for k := range in {
		if k != "pattern" && k != "path" {
			return false
		}
	}
	return true
}

var docExts = map[string]bool{".md": true, ".mdx": true, ".txt": true, ".rst": true}

// repoDoc turns a path a session touched into a repo-relative doc path. An
// absolute path counts only inside the project or a Tallpine worktree of it:
// every session has its own worktree, and only the part below it is shared.
func repoDoc(p, projectPath string) (string, bool) {
	if !docExts[strings.ToLower(path.Ext(p))] {
		return "", false
	}
	return repoRelative(p, projectPath)
}

// repoRelative turns a path into one relative to the repo root; absolute paths
// count only inside the project or a Tallpine worktree of it.
func repoRelative(p, projectPath string) (string, bool) {
	p = path.Clean(p)
	switch {
	case path.IsAbs(p):
		if root := strings.TrimSuffix(projectPath, "/"); root != "" && p == root {
			return ".", true
		} else if root != "" && strings.HasPrefix(p, root+"/") {
			p = strings.TrimPrefix(p, root+"/")
		} else if _, rest, ok := strings.Cut(p, "/worktrees/"); ok {
			parts := strings.SplitN(rest, "/", 3) // <project>/<session>/<path>
			if len(parts) < 3 {
				return "", false
			}
			p = parts[2]
		} else {
			return "", false
		}
	case p == ".." || strings.HasPrefix(p, "../"):
		return "", false
	}
	return p, true
}

// instFile is an always-loaded file with its lines.
type instFile struct {
	path    string
	content string
	lines   []string
}

func readInstructionFiles(ctx context.Context, src Source, m Measurement) []instFile {
	seen := map[string]bool{}
	var out []instFile
	for _, g := range m.Groups {
		for _, f := range g.Files {
			if seen[f.Path] {
				continue
			}
			seen[f.Path] = true
			data, ok, err := src.Read(ctx, f.Path)
			if err != nil || !ok {
				continue
			}
			out = append(out, instFile{path: f.Path, content: string(data), lines: splitLines(string(data))})
		}
	}
	return out
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

func docFinding(ctx context.Context, src Source, inst []instFile, m Measurement, doc string, st gapStats, coverage string) (gap, bool) {
	sessions := len(st)
	if routedByName(doc) {
		return gap{}, false
	}
	for _, f := range inst {
		if f.path == doc {
			return gap{}, false // always loaded already
		}
	}
	for _, s := range m.Skills {
		if s.Path == doc {
			return gap{}, false
		}
	}
	if _, _, named := findMention(inst, doc); named {
		return gap{}, false // already routed: sessions can open it by path
	}
	if len(inst) == 0 {
		return gap{}, false // nowhere to add a line
	}
	data, ok, err := src.Read(ctx, doc)
	if err != nil || !ok {
		return gap{}, false // not on the default branch: nothing to point at
	}
	line := "- When working on " + docTitle(doc, string(data)) + ", read `" + doc + "` first."
	target := inst[0]
	var b strings.Builder
	fmt.Fprintf(&b, "In %d separate sessions of this project in the last 7 days, a session searched (grep, glob or find) and then read `%s`, which no instruction file names.\n", sessions, doc)
	b.WriteString(sessionEvidence(st))
	fmt.Fprintf(&b, "\nProposed: add this line to %s (after its line %d), pointing at `%s`:\n\n    %s\n", target.path, len(target.lines), doc, line)
	fmt.Fprintf(&b, "\nWhat it saves: sessions go straight to the file instead of searching for it, about %d tokens per session (estimate); roughly %d tokens over these sessions. It costs about %d tokens in every session's always-loaded context.\n", searchCostTokens, sessions*searchCostTokens, len(line)/4+1)
	return gap{
		ContextHealthFinding: domain.ContextHealthFinding{
			Kind: domain.ContextRoutingPointer, Fingerprint: "routing_pointer:doc:" + doc,
			MeasuredTokens: sessions, BudgetTokens: routingBudget, Evidence: b.String() + coverage,
			Patch: insertPatch(target.path, target.content, len(target.lines), line),
		},
		body: b.String(), line: line, file: target, doc: doc,
	}, true
}

// findMention returns the first instruction line that names doc, 1-based.
func findMention(inst []instFile, doc string) (instFile, int, bool) {
	for _, f := range inst {
		for i, l := range f.lines {
			if mentionsPath(l, doc) {
				return f, i + 1, true
			}
		}
	}
	return instFile{}, 0, false
}

// mentionsPath reports whether line names doc as a whole path: `README.md`
// does not match inside `backend/README.md`.
func mentionsPath(line, doc string) bool {
	for rest, off := line, 0; ; {
		i := strings.Index(rest, doc)
		if i < 0 {
			return false
		}
		start, end := off+i, off+i+len(doc)
		if (start == 0 || !pathChar(line[start-1])) && (end == len(line) || !pathChar(line[end])) {
			return true
		}
		rest, off = line[start+1:], start+1
	}
}

func pathChar(c byte) bool {
	return c == '/' || c == '_' || c == '-' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// docTitle is the doc's first top-level heading, else its file name.
func docTitle(doc, content string) string {
	for _, l := range strings.Split(content, "\n") {
		if t, ok := strings.CutPrefix(l, "# "); ok && strings.TrimSpace(t) != "" {
			t = strings.TrimSpace(t)
			if r := []rune(t); len(r) > maxTitleChars {
				t = string(r[:maxTitleChars])
			}
			return t
		}
	}
	return strings.TrimSuffix(path.Base(doc), path.Ext(doc))
}

// insertPatch is a unified diff that adds newLines after line `after` (1-based,
// 0 for the top) of the file.
func insertPatch(file, content string, after int, newLines ...string) string {
	lines := splitLines(content)
	after = min(max(after, 0), len(lines))
	noEOL := content != "" && !strings.HasSuffix(content, "\n")
	lo, hi := max(after-patchContext, 0), min(after+patchContext, len(lines))
	var body strings.Builder
	oldLen := hi - lo
	newLen := oldLen + len(newLines)
	for i := lo; i < hi; i++ {
		if noEOL && after == len(lines) && i == len(lines)-1 {
			// The last line gains a newline, so the diff rewrites it.
			body.WriteString("-" + lines[i] + "\n\\ No newline at end of file\n+" + lines[i] + "\n" + added(newLines))
			continue
		}
		if i == after {
			body.WriteString(added(newLines))
		}
		body.WriteString(" " + lines[i] + "\n")
		if noEOL && i == len(lines)-1 {
			body.WriteString("\\ No newline at end of file\n")
		}
	}
	if after == hi && (!noEOL || after != len(lines)) {
		body.WriteString(added(newLines))
	}
	oldStart := lo + 1
	if oldLen == 0 {
		oldStart = lo
	}
	return fmt.Sprintf("--- a/%s\n+++ b/%s\n@@ -%d,%d +%d,%d @@\n%s", file, file, oldStart, oldLen, lo+1, newLen, body.String())
}
