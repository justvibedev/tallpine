package contextbudget

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// DigestFingerprint is the one routing finding of a project: the week's pointer
// gaps. It stays stable while its gaps change, so an open card keeps its analyst
// draft. A No is recorded per gap (see routing_declined.go), not on the finding.
const DigestFingerprint = "routing_pointer:weekly"

func isDigest(fingerprint string) bool { return fingerprint == DigestFingerprint }

// gap is one doc or search that several sessions had to hunt for. The embedded
// finding is the per-gap view (Evidence = body + coverage); the digest uses body.
type gap struct {
	domain.ContextHealthFinding
	body string
	// line and file are the drafted pointer; doc is what it leads to.
	line string
	file instFile
	doc  string // the doc a pointer would lead to
}

// sessStat is what one session did on its way to a gap.
type sessStat struct {
	calls           int
	searched, reads map[string]bool
}

// gapStats is the per-session evidence of one doc or search key.
type gapStats map[domain.SessionID]*sessStat

func (g gapStats) add(s domain.SessionID, calls int, searched, reads []string) {
	st := g[s]
	if st == nil {
		st = &sessStat{searched: map[string]bool{}, reads: map[string]bool{}}
		g[s] = st
	}
	st.calls = max(st.calls, calls)
	for _, q := range searched {
		st.searched[q] = true
	}
	for _, d := range reads {
		st.reads[d] = true
	}
}

// A repeated search is a gap only when a pointer would have saved real work:
// the search is a lookup of a project concept, most sessions that ran it ended
// in the same doc, that doc costs them a few tool calls each, and no instruction
// file links it yet. The bars below are the owner-approved selection for B837;
// they keep markers, keywords and code lookups (Q45, Q46) out of the digest.
const (
	// searchMinSessions is above the 3 a doc gap needs: a search string says
	// less than a doc read, so more sessions must agree.
	searchMinSessions = 5
	// searchAgreePercent is the share of the sessions that ran the search which ended in the same doc.
	searchAgreePercent = 60
	// searchMinCalls is the mean tool calls from search to doc (inclusive): at
	// least one step between them, or the pointer saves nothing.
	searchMinCalls = 3
	// searchMinChars is the shortest lookup that names a concept.
	searchMinChars = 6
)

// codeWords are keywords and terms too common to name a project concept.
var codeWords = map[string]bool{
	"export": true, "import": true, "func": true, "function": true, "class": true, "const": true,
	"return": true, "struct": true, "interface": true, "package": true, "default": true,
	"todo": true, "fixme": true, "error": true, "errors": true, "test": true, "tests": true,
	"type": true, "types": true, "false": true, "true": true, "null": true, "main": true, "init": true,
}

// conceptQuery reports whether a search looks up a project concept or place:
// no regex or shell syntax (conflict markers, alternations, anchors) and not a
// bare keyword or a very short term.
func conceptQuery(q string) bool {
	q = strings.TrimSpace(q)
	if len([]rune(q)) < searchMinChars || strings.ContainsAny(q, `^$|\()[]{}*+?<>=!`) {
		return false
	}
	return !codeWords[strings.ToLower(q)]
}

// agreedDoc is the doc most of the sessions ended in, with the stats of the
// sessions that did; false when the sessions did not agree.
func agreedDoc(st gapStats) (string, gapStats, bool) {
	by := map[string]gapStats{}
	for id, s := range st {
		for d := range s.reads {
			if by[d] == nil {
				by[d] = gapStats{}
			}
			by[d][id] = s
		}
	}
	best := ""
	for d, g := range by {
		if best == "" || len(g) > len(by[best]) || len(g) == len(by[best]) && d < best {
			best = d
		}
	}
	if best == "" || len(by[best])*100 < searchAgreePercent*len(st) {
		return "", nil, false
	}
	return best, by[best], true
}

// searchFinding turns a repeated search into a pointer gap for the doc the sessions ended in.
func searchFinding(ctx context.Context, src Source, inst []instFile, m Measurement, query string, st gapStats, coverage string) (gap, bool) {
	if !conceptQuery(query) {
		return gap{}, false
	}
	doc, agreed, ok := agreedDoc(st)
	if !ok || len(agreed) < searchMinSessions {
		return gap{}, false
	}
	calls := 0
	for _, s := range agreed {
		calls += s.calls
	}
	if calls < searchMinCalls*len(agreed) {
		return gap{}, false
	}
	g, ok := docFinding(ctx, src, inst, m, doc, agreed, coverage)
	if !ok {
		return gap{}, false // linked already, always loaded or not on the default branch
	}
	sum := sha256.Sum256([]byte(query))
	g.Fingerprint = "routing_pointer:search:" + hex.EncodeToString(sum[:6])
	g.doc = doc
	g.body = fmt.Sprintf("The search %#q ran in %d separate sessions of this project in the last 7 days; %d of them ended in %#q, about %d tool calls each.\n", query, len(st), len(agreed), doc, calls/len(agreed)) + g.body
	g.Evidence = g.body + coverage
	return g, true
}

const maxEvidenceSessions = 5

// sessionEvidence lists, per session, what it searched for, what it ended up
// reading and about how many tool calls that took: the justification of a gap.
func sessionEvidence(st gapStats) string {
	ids := make([]domain.SessionID, 0, len(st))
	total := 0
	for id, s := range st {
		ids = append(ids, id)
		total += s.calls
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var b strings.Builder
	b.WriteString("\nWhat the sessions did:\n")
	for _, id := range ids[:min(len(ids), maxEvidenceSessions)] {
		s := st[id]
		fmt.Fprintf(&b, "- session %s searched %s, then read %s (about %d tool calls)\n", id, quoted(s.searched), quoted(s.reads), s.calls)
	}
	if len(ids) > maxEvidenceSessions {
		fmt.Fprintf(&b, "- and %d more sessions\n", len(ids)-maxEvidenceSessions)
	}
	fmt.Fprintf(&b, "About %d tool calls in all.\n", total)
	return b.String()
}

func quoted(set map[string]bool) string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return "(nothing recorded)"
	}
	if len(keys) > 3 {
		keys = append(keys[:3], "…")
	}
	return "`" + strings.Join(keys, "`, `") + "`"
}

// digest is the one routing finding: every gap with its justification and its
// exact change, plus one baseline patch that adds the doc pointers in one go.
func digest(gs []gap, coverage string) domain.ContextHealthFinding {
	var b strings.Builder
	fmt.Fprintf(&b, "%d doc-pointer gaps this week: sessions searched for the same things, or read docs no instruction file names. Approve to start a worker that adds the pointers.\n", len(gs))
	var lines []string
	var file instFile
	for i, g := range gs {
		fmt.Fprintf(&b, "\n## Gap %d: %s\n%s", i+1, strings.TrimPrefix(g.Fingerprint, "routing_pointer:"), g.body)
		if g.line != "" {
			lines, file = append(lines, g.line), g.file
		}
	}
	b.WriteString(coverage)
	f := domain.ContextHealthFinding{
		Kind: domain.ContextRoutingPointer, Fingerprint: DigestFingerprint,
		MeasuredTokens: len(gs), BudgetTokens: routingBudget, Evidence: b.String(),
	}
	if len(lines) > 0 {
		f.Patch = insertPatch(file.path, file.content, len(file.lines), lines...)
	}
	return f
}

func added(lines []string) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString("+" + l + "\n")
	}
	return b.String()
}
