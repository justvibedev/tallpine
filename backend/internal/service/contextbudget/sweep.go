package contextbudget

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	asksvc "tallpine.eu/tallpine/backend/internal/service/ask"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

const (
	// checkEvery is how often a project is measured. The tick is shorter, so a
	// newly enabled project is picked up within the hour.
	checkEvery = 24 * time.Hour
	// calibrationWindow and calibrationSamples bound the first-turn read.
	calibrationWindow  = 14 * 24 * time.Hour
	calibrationSamples = 20
)

// Store is what the sweep reads and writes.
type Store interface {
	ListProjects(ctx context.Context) ([]domain.ProjectRecord, error)
	ListFindings(ctx context.Context, project domain.ProjectID) ([]domain.ContextHealthFinding, error)
	UpsertFinding(ctx context.Context, f domain.ContextHealthFinding) error
	SaveFindingTransition(ctx context.Context, f domain.ContextHealthFinding, from domain.ContextHealthState) (bool, error)
	GetRun(ctx context.Context, project domain.ProjectID) (time.Time, bool, error)
	SaveRun(ctx context.Context, project domain.ProjectID, ranAt time.Time, snapshot string) error
	ListFirstTurnInputs(ctx context.Context, project domain.ProjectID, since time.Time, limit int) ([]store.FirstTurnInput, error)
}

// Asks is the daemon's side of the owner-ask service. The sweep never sends a
// message to an orchestrator: a finding reaches the owner only as a card.
type Asks interface {
	CreateSystem(ctx context.Context, project domain.ProjectID, origin string, in asksvc.CreateInput) (domain.OwnerAsk, error)
	WithdrawSystem(ctx context.Context, project domain.ProjectID, ref, reason string) (domain.OwnerAsk, error)
	Get(ctx context.Context, project domain.ProjectID, ref string) (domain.OwnerAsk, error)
}

// Deps wires the sweep.
type Deps struct {
	Store Store
	Asks  Asks
	// Reader returns a source of the project's default-branch files.
	Reader func(ctx context.Context, proj domain.ProjectRecord) (Source, error)
	// Memory returns the project's Claude orchestrator memory, nil when it has none.
	Memory func(ctx context.Context, proj domain.ProjectRecord) (*Memory, error)
	// Prompt renders the Tallpine system prompt of a role, for evidence.
	Prompt func(ctx context.Context, proj domain.ProjectRecord, kind domain.SessionKind, h domain.AgentHarness) (string, error)
	// Analyst drafts patches for budget findings; nil leaves cards evidence-only.
	Analyst *Analyst
	// Finders default to the budget finder.
	Finders []Finder
	// ArtifactRoot holds <project>/context-health/<finding>/ evidence files.
	ArtifactRoot string
	Now          func() time.Time
	Log          *slog.Logger
}

// Sweeper measures opted-in projects, keeps their findings and files cards.
type Sweeper struct {
	deps Deps
	// checked holds the projects checked since this process started. The first
	// check of each ignores checkEvery, so an upgrade's new rules clear stale
	// findings at startup instead of up to a day later.
	checked map[domain.ProjectID]bool
}

// New returns a Sweeper.
func New(deps Deps) *Sweeper {
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if len(deps.Finders) == 0 {
		deps.Finders = []Finder{budgetFinder{}}
	}
	return &Sweeper{deps: deps, checked: map[domain.ProjectID]bool{}}
}

// Sweep checks every opted-in project that has not been checked today. It needs
// no orchestrator.
func (s *Sweeper) Sweep(ctx context.Context) error {
	projects, err := s.deps.Store.ListProjects(ctx)
	if err != nil {
		return fmt.Errorf("list projects: %w", err)
	}
	var errs []error
	for _, proj := range projects {
		if !proj.ArchivedAt.IsZero() || !proj.Config.ContextHealth {
			continue
		}
		if err := s.ingestAnalyst(ctx, proj); err != nil {
			errs = append(errs, fmt.Errorf("context health analyst %s: %w", proj.ID, err))
		}
		if err := s.check(ctx, proj); err != nil {
			errs = append(errs, fmt.Errorf("context health %s: %w", proj.ID, err))
		}
	}
	return errors.Join(errs...)
}

func (s *Sweeper) check(ctx context.Context, proj domain.ProjectRecord) error {
	pid, now := domain.ProjectID(proj.ID), s.deps.Now()
	if last, ok, err := s.deps.Store.GetRun(ctx, pid); err != nil {
		return err
	} else if ok && s.checked[pid] && now.Sub(last) < checkEvery {
		return nil
	}
	m, err := s.measure(ctx, proj)
	if err != nil {
		return err
	}
	s.checked[pid] = true
	var cands []domain.ContextHealthFinding
	for _, f := range s.deps.Finders {
		found, err := f.Find(ctx, proj, m)
		if err != nil {
			return err
		}
		cands = append(cands, found...)
	}
	if err := s.reconcile(ctx, proj, cands); err != nil {
		return err
	}
	return s.deps.Store.SaveRun(ctx, pid, now, snapshot(m))
}

func snapshot(m Measurement) string {
	v := map[string]any{"instructionTokens": m.Total, "budget": m.Budget, "skills": len(m.Skills)}
	if m.Memory != nil {
		v["memoryIndexTokens"] = m.Memory.IndexTokens
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func (s *Sweeper) measure(ctx context.Context, proj domain.ProjectRecord) (Measurement, error) {
	m := Measurement{Budget: proj.Config.ContextBudgetTokens, MemoryBudget: proj.Config.ContextMemoryBudgetTokens}
	if m.Budget <= 0 {
		m.Budget = domain.DefaultContextBudgetTokens
	}
	if m.MemoryBudget <= 0 {
		m.MemoryBudget = domain.DefaultContextMemoryBudgetTokens
	}
	src, err := s.deps.Reader(ctx, proj)
	if err != nil {
		return m, err
	}
	if m.Groups, m.Total, err = measureAll(ctx, src); err != nil {
		return m, err
	}
	if m.Skills, err = MeasureSkills(ctx, src); err != nil {
		return m, err
	}
	if s.deps.Memory != nil {
		if m.Memory, err = s.deps.Memory(ctx, proj); err != nil {
			s.deps.Log.Warn("context health: memory unreadable", "project", proj.ID, "err", err)
			m.Memory = nil
		}
	}
	m.Prompts = s.prompts(ctx, proj, m.Groups)
	m.Calibration = s.calibration(ctx, domain.ProjectID(proj.ID))
	return m, nil
}

// prompts sizes the Tallpine system prompt of each role for the harnesses that
// load instruction files. A prompt that cannot be rendered is skipped.
func (s *Sweeper) prompts(ctx context.Context, proj domain.ProjectRecord, groups []group) []PromptSize {
	if s.deps.Prompt == nil {
		return nil
	}
	var out []PromptSize
	for _, g := range groups {
		for _, h := range g.harnesses {
			for _, kind := range []domain.SessionKind{domain.KindOrchestrator, domain.KindWorker} {
				text, err := s.deps.Prompt(ctx, proj, kind, h)
				if err != nil || text == "" {
					continue
				}
				out = append(out, PromptSize{Kind: kind, Harness: h, Tokens: Tokens(len(text))})
			}
		}
	}
	return out
}

// calibration is the median first-turn input of the project's recent sessions
// per harness. A failed read leaves it out: it is evidence, not a gate.
func (s *Sweeper) calibration(ctx context.Context, project domain.ProjectID) []Calibration {
	rows, err := s.deps.Store.ListFirstTurnInputs(ctx, project, s.deps.Now().Add(-calibrationWindow), calibrationSamples)
	if err != nil {
		s.deps.Log.Warn("context health: first-turn usage unreadable", "project", project, "err", err)
		return nil
	}
	by := map[string][]int64{}
	for _, r := range rows {
		by[r.Harness] = append(by[r.Harness], r.Tokens)
	}
	var out []Calibration
	for h, v := range by {
		out = append(out, Calibration{Harness: domain.AgentHarness(h), Samples: len(v), Median: median(v)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Harness < out[j].Harness })
	return out
}

// reconcile moves the project's stored findings to match the candidates, then
// files cards for queued ones while fewer than domain.MaxOpenContextCards are open.
func (s *Sweeper) reconcile(ctx context.Context, proj domain.ProjectRecord, cands []domain.ContextHealthFinding) error {
	pid, now := domain.ProjectID(proj.ID), s.deps.Now()
	stored, err := s.deps.Store.ListFindings(ctx, pid)
	if err != nil {
		return err
	}
	for i := range stored {
		if stored[i], err = s.syncOpen(ctx, stored[i], now); err != nil {
			return err
		}
	}
	byFP := map[string]domain.ContextHealthFinding{}
	for _, f := range stored {
		byFP[f.Fingerprint] = f
	}
	seen := map[string]bool{}
	for _, c := range cands {
		seen[c.Fingerprint] = true
		f, ok := byFP[c.Fingerprint]
		if !ok {
			f = domain.ContextHealthFinding{ID: uuid.NewString(), ProjectID: pid, Kind: c.Kind, Fingerprint: c.Fingerprint, State: domain.ContextFindingQueued, CreatedAt: now}
		}
		from, prevEvidence := f.State, f.Evidence
		f.Evidence, f.MeasuredTokens, f.BudgetTokens, f.Patch, f.UpdatedAt = c.Evidence, c.MeasuredTokens, c.BudgetTokens, c.Patch, now
		switch {
		case isDigest(f.Fingerprint) && f.State == domain.ContextFindingDeclined && prevEvidence == c.Evidence:
			// The gaps are the ones declined. The finder's list can predate the decline record
			// (a withdrawal is read earlier in this sweep), or the record could not be written.
		case isDigest(f.Fingerprint) && (f.State == domain.ContextFindingDeclined || f.State == domain.ContextFindingResolved):
			// A No covers the gaps it showed, which the finder leaves out; what reaches here is new.
			// The finding keeps its id, so the old card's drafts go: they describe other gaps.
			clearDrafts(f.ArtifactDir)
			f.State, f.AskID, f.AskRef, f.DeclinedAt, f.DeclinedTokens = domain.ContextFindingQueued, "", "", time.Time{}, 0
			f.ArtifactDir = ""
		case f.State == domain.ContextFindingResolved && f.SuppressionHolds(now, c.MeasuredTokens, c.BudgetTokens):
			f.State = domain.ContextFindingDeclined // dipped under and grew back inside the owner's No
		case f.State == domain.ContextFindingResolved:
			f.State, f.AskID, f.AskRef, f.DeclinedAt, f.DeclinedTokens = domain.ContextFindingQueued, "", "", time.Time{}, 0 // a new cycle
		case f.State == domain.ContextFindingDeclined && !f.Suppressed(now, c.MeasuredTokens, c.BudgetTokens):
			f.State, f.AskID, f.AskRef, f.DeclinedAt, f.DeclinedTokens = domain.ContextFindingQueued, "", "", time.Time{}, 0
		}
		if err := s.save(ctx, f, from, !ok); err != nil {
			return err
		}
		byFP[c.Fingerprint] = f
	}
	for _, f := range stored {
		if seen[f.Fingerprint] || f.State == domain.ContextFindingResolved {
			continue
		}
		if err := s.resolve(ctx, f, now); err != nil {
			return err
		}
	}
	held := s.analystHold(ctx, pid, byFP, seen, now)
	return s.promote(ctx, proj, byFP, seen, held)
}

// syncOpen reconciles an open finding with its ask, which can close out of
// band: withdrawn by the owner (read as a No), or answered while the handler
// failed (the finding is queued again for a fresh card). Without this the
// finding would hold a card slot with a dead ask for good.
func (s *Sweeper) syncOpen(ctx context.Context, f domain.ContextHealthFinding, now time.Time) (domain.ContextHealthFinding, error) {
	if f.State != domain.ContextFindingOpen {
		return f, nil
	}
	a, err := s.deps.Asks.Get(ctx, f.ProjectID, f.AskRef)
	next := f
	switch {
	case err != nil && !isCode(err, "ASK_NOT_FOUND"):
		return f, err
	case err != nil || a.State == domain.AskAnswered:
		next.State, next.AskID, next.AskRef = domain.ContextFindingQueued, "", ""
	case a.State == domain.AskWithdrawn:
		next.State, next.DeclinedAt, next.DeclinedTokens, next.DeclineReason = domain.ContextFindingDeclined, now, f.MeasuredTokens, "withdrawn"
		recordDecline(f, now, s.deps.Log)
	default:
		return f, nil
	}
	next.UpdatedAt = now
	if ok, err := s.deps.Store.SaveFindingTransition(ctx, next, domain.ContextFindingOpen); err != nil || !ok {
		return f, err
	}
	return next, nil
}

func (s *Sweeper) save(ctx context.Context, f domain.ContextHealthFinding, from domain.ContextHealthState, isNew bool) error {
	if isNew {
		return s.deps.Store.UpsertFinding(ctx, f)
	}
	// A lost race (the owner answered meanwhile) is left for the next check.
	_, err := s.deps.Store.SaveFindingTransition(ctx, f, from)
	return err
}

// resolve closes a finding whose measurement is back under its limit, taking
// its card down with a reason.
func (s *Sweeper) resolve(ctx context.Context, f domain.ContextHealthFinding, now time.Time) error {
	from := f.State
	if from == domain.ContextFindingOpen && f.AskRef != "" {
		if _, err := s.deps.Asks.WithdrawSystem(ctx, f.ProjectID, f.AskRef, withdrawReason(f)); err != nil && !isNotOpen(err) {
			return err
		}
	}
	f.State, f.UpdatedAt = domain.ContextFindingResolved, now
	_, err := s.deps.Store.SaveFindingTransition(ctx, f, from)
	return err
}

func withdrawReason(f domain.ContextHealthFinding) string {
	if f.Kind == domain.ContextRoutingPointer {
		if !isDigest(f.Fingerprint) {
			return "Replaced by the weekly pointer-gap card: one card with a drafted patch instead of one per search."
		}
		return fmt.Sprintf("No doc-pointer gaps left: fewer than %d sessions hunted for the same thing this week.", domain.ContextRoutingMinSessions)
	}
	return "Back under budget: nothing left to trim."
}

func isNotOpen(err error) bool { return isCode(err, "ASK_NOT_OPEN") }

func isCode(err error, code string) bool {
	var ae *apierr.Error
	return errors.As(err, &ae) && ae.Code == code
}

// promote files a card for each queued finding, oldest first, within the cap.
func (s *Sweeper) promote(ctx context.Context, proj domain.ProjectRecord, byFP map[string]domain.ContextHealthFinding, seen, held map[string]bool) error {
	var queued []domain.ContextHealthFinding
	open := 0
	for fp, f := range byFP {
		switch {
		case f.State == domain.ContextFindingOpen && seen[fp]:
			open++
		case f.State == domain.ContextFindingQueued && seen[fp] && !held[f.ID]:
			queued = append(queued, f)
		}
	}
	sort.Slice(queued, func(i, j int) bool {
		if !queued[i].CreatedAt.Equal(queued[j].CreatedAt) {
			return queued[i].CreatedAt.Before(queued[j].CreatedAt)
		}
		return queued[i].Fingerprint < queued[j].Fingerprint
	})
	for _, f := range queued {
		if open >= domain.MaxOpenContextCards {
			break
		}
		if err := s.file(ctx, proj, f); errors.Is(err, errAskLimit) {
			s.deps.Log.Info("context health: main chat is full of asks; findings stay queued", "project", proj.ID)
			return nil
		} else if err != nil {
			return err
		}
		open++
	}
	return nil
}

// errAskLimit means the main chat has no room for another card; the finding stays queued.
var errAskLimit = errors.New("main chat ask limit")

func (s *Sweeper) file(ctx context.Context, proj domain.ProjectRecord, f domain.ContextHealthFinding) error {
	a, err := s.deps.Asks.CreateSystem(ctx, f.ProjectID, domain.AskOriginContextHealth, askInput(f))
	switch {
	case isCode(err, "ASK_ALREADY_OPEN"):
		// An earlier run filed this card and stopped before linking it: adopt it.
		var ae *apierr.Error
		errors.As(err, &ae)
		ref, _ := ae.Details["ref"].(string)
		if a, err = s.deps.Asks.Get(ctx, f.ProjectID, ref); err != nil {
			return err
		}
	case isCode(err, "ASK_LIMIT"):
		return errAskLimit
	case err != nil:
		return err
	}
	from := f.State
	f.State, f.AskID, f.AskRef, f.UpdatedAt = domain.ContextFindingOpen, a.ID, a.Ref, s.deps.Now()
	f.ArtifactDir = s.writeEvidence(f)
	if isDigest(f.Fingerprint) {
		pinShownGaps(f.ArtifactDir)
	}
	if ok, err := s.deps.Store.SaveFindingTransition(ctx, f, from); err != nil || !ok {
		return errors.Join(err, s.withdrawOrphan(ctx, f))
	}
	s.deps.Log.Info("context health card filed", "project", proj.ID, "kind", f.Kind, "tokens", f.MeasuredTokens, "budget", f.BudgetTokens, "ask", a.Ref)
	return nil
}

// withdrawOrphan takes down a card whose finding could not be updated, so the
// next check files one card again instead of two.
func (s *Sweeper) withdrawOrphan(ctx context.Context, f domain.ContextHealthFinding) error {
	_, err := s.deps.Asks.WithdrawSystem(ctx, f.ProjectID, f.AskRef, "The finding changed while the card was filed.")
	return err
}

// writeEvidence stores the evidence beside the finding's other artifacts and
// returns the directory, "" when no root is set or the write fails.
func (s *Sweeper) writeEvidence(f domain.ContextHealthFinding) string {
	if s.deps.ArtifactRoot == "" {
		return ""
	}
	dir := filepath.Join(s.deps.ArtifactRoot, string(f.ProjectID), "context-health", f.ID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		s.deps.Log.Warn("context health: artifact dir", "dir", dir, "err", err)
		return ""
	}
	if err := os.WriteFile(filepath.Join(dir, "evidence.md"), []byte(f.Evidence), 0o600); err != nil {
		s.deps.Log.Warn("context health: evidence file", "dir", dir, "err", err)
		return ""
	}
	if f.Patch != "" {
		if err := os.WriteFile(filepath.Join(dir, patchFile), []byte(f.Patch), 0o600); err != nil {
			s.deps.Log.Warn("context health: patch file", "dir", dir, "err", err)
		}
	}
	return dir
}

// group is the report shared by the harnesses that load the same files.
type group struct {
	Report
	harnesses []domain.AgentHarness
}

// measureAll measures every harness and folds those that load the same files
// into one group. total is the largest group, the worst case a session sees.
func measureAll(ctx context.Context, src Source) ([]group, int, error) {
	var groups []group
	total := 0
	for _, h := range domain.AllHarnesses {
		rep, err := Measure(ctx, src, h)
		if err != nil {
			return nil, 0, err
		}
		if len(rep.Files) == 0 {
			continue
		}
		total = max(total, rep.Tokens)
		merged := false
		for i := range groups {
			if samePaths(groups[i].Files, rep.Files) {
				groups[i].harnesses = append(groups[i].harnesses, h)
				merged = true
				break
			}
		}
		if !merged {
			groups = append(groups, group{Report: rep, harnesses: []domain.AgentHarness{h}})
		}
	}
	return groups, total, nil
}

func samePaths(a, b []File) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Path != b[i].Path {
			return false
		}
	}
	return true
}
