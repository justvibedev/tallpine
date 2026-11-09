package backlog

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// Service owns the project backlog: items, hierarchy, links and ordering.
// Invariants (one-level nesting, no blocked_by cycles, ref allocation) live in
// the store; the service validates input, resolves refs and applies the
// actor rules.
type Service struct {
	store Store
	newID func() string
	now   func() time.Time
	log   *slog.Logger

	// plans handles planner sessions (B205); nil until NewPlanService sets it.
	plans *PlanService

	logMu      sync.Mutex
	lastLogged map[string]string
}

// Deps configures a Service.
type Deps struct {
	Store Store
	NewID func() string
	Now   func() time.Time
	// Logger receives the auto-close decisions; nil uses slog.Default.
	Logger *slog.Logger
}

// New constructs the backlog service.
func New(d Deps) *Service {
	if d.NewID == nil {
		d.NewID = func() string { return "bl_" + uuid.NewString() }
	}
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &Service{store: d.Store, newID: d.NewID, now: d.Now, log: d.Logger, lastLogged: map[string]string{}}
}

// Create files an item. Workers always land in the inbox; orchestrators do
// unless Open is set; humans always create open items.
func (s *Service) Create(ctx context.Context, projectID domain.ProjectID, actor Actor, in CreateInput) (View, error) {
	if strings.TrimSpace(string(projectID)) == "" {
		return View{}, apierr.Invalid("INVALID_PROJECT_ID", "Project id is required", nil)
	}
	title := strings.TrimSpace(in.Title)
	item := domain.BacklogItem{
		ID: s.newID(), ProjectID: projectID, Title: title, Note: in.Note, DoneWhen: in.DoneWhen,
		Scope: in.Scope, Hold: strings.TrimSpace(in.Hold), AgentNotes: in.AgentNotes,
		Origin: domain.BacklogOrigin{Kind: actor.Kind, SessionID: actor.SessionID, Excerpt: truncateRunes(in.Excerpt, domain.MaxBacklogExcerpt)},
	}
	if err := validateItem(item); err != nil {
		return View{}, err
	}
	if actor.Kind != domain.BacklogOriginHuman && actor.Kind != domain.BacklogOriginOrchestrator {
		return s.createAsAgent(ctx, projectID, actor, in)
	}
	switch actor.Kind {
	case domain.BacklogOriginHuman:
		item.State = domain.BacklogOpen
	case domain.BacklogOriginOrchestrator:
		item.State = domain.BacklogInbox
		if in.Open {
			item.State = domain.BacklogOpen
		}
	}
	if in.ParentRef != "" {
		parent, err := s.resolve(ctx, projectID, in.ParentRef)
		if err != nil {
			return View{}, err
		}
		item.ParentID = parent.ID
		if item.Scope == "" {
			item.Scope = parent.Scope
		}
	}
	now := s.now().UTC()
	item.CreatedAt, item.UpdatedAt = now, now
	created, err := s.store.CreateBacklogItem(ctx, item)
	if err != nil {
		return View{}, mapError(err)
	}
	return s.view(ctx, created)
}

// Get returns one item by id or ref.
func (s *Service) Get(ctx context.Context, projectID domain.ProjectID, idOrRef string) (View, error) {
	item, err := s.resolve(ctx, projectID, idOrRef)
	if err != nil {
		return View{}, err
	}
	return s.view(ctx, item)
}

// List returns a project's items in drag order.
func (s *Service) List(ctx context.Context, projectID domain.ProjectID, f ListFilter) ([]View, error) {
	if f.State == "all" {
		f.State = ""
	}
	if f.State != "" && f.State != "active" && !f.State.Valid() {
		return nil, apierr.Invalid("INVALID_BACKLOG_STATE", "State must be inbox, open, done, dropped, active or all", nil)
	}
	if f.Held != "" && f.Held != "held" && f.Held != "unheld" {
		return nil, apierr.Invalid("INVALID_BACKLOG_FILTER", "held must be held or unheld", nil)
	}
	parentID := ""
	if f.ParentRef != "" {
		parent, err := s.resolve(ctx, projectID, f.ParentRef)
		if err != nil {
			return nil, err
		}
		parentID = parent.ID
	}
	items, err := s.store.ListBacklogItems(ctx, projectID, store.BacklogFilter{
		State: f.State, ParentID: parentID, Held: f.Held, Query: strings.TrimSpace(f.Query), Limit: f.Limit,
	})
	if err != nil {
		return nil, err
	}
	refs := map[string]string{}
	for _, it := range items {
		refs[it.ID] = it.Ref()
	}
	childStats, err := s.store.BacklogChildStats(ctx, projectID)
	if err != nil {
		return nil, err
	}
	out := make([]View, 0, len(items))
	for _, it := range items {
		stats := childStats[it.ID]
		v := View{BacklogItem: it, ChildCount: stats.Total, ChildClosedCount: stats.Closed}
		if it.ParentID != "" {
			v.ParentRef = refs[it.ParentID]
			if v.ParentRef == "" {
				if p, ok, _ := s.store.GetBacklogItem(ctx, it.ParentID); ok {
					v.ParentRef = p.Ref()
				}
			}
		}
		if v.OpenBlockers, err = s.listOpenBlockers(ctx, it); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := s.attachBadges(ctx, projectID, "", out); err != nil {
		return nil, err
	}
	return out, nil
}

// Update applies a partial edit.
func (s *Service) Update(ctx context.Context, projectID domain.ProjectID, idOrRef string, in UpdateInput) (View, error) {
	item, err := s.resolve(ctx, projectID, idOrRef)
	if err != nil {
		return View{}, err
	}
	parentID := item.ParentID
	switch {
	case in.ClearParent:
		parentID = ""
	case in.ParentRef != nil:
		parent, err := s.resolve(ctx, projectID, *in.ParentRef)
		if err != nil {
			return View{}, err
		}
		parentID = parent.ID
	}
	textEdited := (in.Title != nil && strings.TrimSpace(*in.Title) != item.Title) ||
		(in.Note != nil && *in.Note != item.Note) || (in.DoneWhen != nil && *in.DoneWhen != item.DoneWhen)
	updated, err := s.store.UpdateBacklogItem(ctx, item.ID, s.now().UTC(), func(i *domain.BacklogItem) error {
		if in.Title != nil {
			i.Title = strings.TrimSpace(*in.Title)
		}
		if in.Note != nil {
			i.Note = *in.Note
		}
		if in.DoneWhen != nil {
			i.DoneWhen = *in.DoneWhen
		}
		if in.Scope != nil {
			i.Scope = *in.Scope
		}
		if in.Hold != nil {
			i.Hold = strings.TrimSpace(*in.Hold)
		}
		if in.AgentNotes != nil {
			i.AgentNotes = *in.AgentNotes
		}
		i.ParentID = parentID
		return validateItem(*i)
	})
	if err != nil {
		return View{}, mapError(err)
	}
	if textEdited {
		s.plans.ItemEdited(ctx, item.ID)
	}
	return s.view(ctx, updated)
}

// SetField sets (or, for an empty value, clears) one custom field.
func (s *Service) SetField(ctx context.Context, projectID domain.ProjectID, idOrRef, key, value string) (View, error) {
	item, err := s.resolve(ctx, projectID, idOrRef)
	if err != nil {
		return View{}, err
	}
	updated, err := s.store.UpdateBacklogItem(ctx, item.ID, s.now().UTC(), func(i *domain.BacklogItem) error {
		fields, err := domain.ApplyBacklogField(i.Fields, key, value)
		i.Fields = fields
		return err
	})
	if err != nil {
		return View{}, mapError(err)
	}
	return s.view(ctx, updated)
}

// SetState moves an item between states; reason is kept for done/dropped.
func (s *Service) SetState(ctx context.Context, projectID domain.ProjectID, idOrRef string, state domain.BacklogState, reason string) (View, error) {
	if !state.Valid() {
		return View{}, apierr.Invalid("INVALID_BACKLOG_STATE", "State must be inbox, open, done or dropped", nil)
	}
	reason = strings.TrimSpace(reason)
	if utf8.RuneCountInString(reason) > domain.MaxBacklogClosedReason {
		return View{}, apierr.Invalid("INVALID_BACKLOG_REASON", "Reason is too long", nil)
	}
	item, err := s.resolve(ctx, projectID, idOrRef)
	if err != nil {
		return View{}, err
	}
	updated, err := s.store.SetBacklogItemState(ctx, item.ID, state, reason, s.now().UTC())
	if err != nil {
		return View{}, mapError(err)
	}
	if state.Closed() {
		s.plans.ItemClosed(ctx, item.ID)
	}
	return s.view(ctx, updated)
}

// Move reorders an item before or after another.
func (s *Service) Move(ctx context.Context, projectID domain.ProjectID, in MoveInput) error {
	if (in.BeforeRef == "") == (in.AfterRef == "") {
		return apierr.Invalid("INVALID_BACKLOG_MOVE", "Give exactly one of before or after", nil)
	}
	item, err := s.resolve(ctx, projectID, in.Ref)
	if err != nil {
		return err
	}
	targetRef, after := in.BeforeRef, false
	if in.AfterRef != "" {
		targetRef, after = in.AfterRef, true
	}
	target, err := s.resolve(ctx, projectID, targetRef)
	if err != nil {
		return err
	}
	return mapError(s.store.MoveBacklogItem(ctx, item.ID, target.ID, after, s.now().UTC()))
}

// Link adds a typed link. For item links the ref may be a B12-style ref.
func (s *Service) Link(ctx context.Context, projectID domain.ProjectID, idOrRef string, kind domain.BacklogLinkKind, ref, createdBy string) (View, error) {
	item, linkRef, err := s.linkTarget(ctx, projectID, idOrRef, kind, ref)
	if err != nil {
		return View{}, err
	}
	if _, err := s.store.AddBacklogLink(ctx, item.ID, domain.BacklogLink{Kind: kind, Ref: linkRef, CreatedBy: createdBy}, s.now().UTC()); err != nil {
		return View{}, mapError(err)
	}
	return s.Get(ctx, projectID, item.ID)
}

// Unlink removes a link.
func (s *Service) Unlink(ctx context.Context, projectID domain.ProjectID, idOrRef string, kind domain.BacklogLinkKind, ref string) (View, error) {
	item, linkRef, err := s.linkTarget(ctx, projectID, idOrRef, kind, ref)
	if err != nil {
		return View{}, err
	}
	if _, err := s.store.RemoveBacklogLink(ctx, item.ID, kind, linkRef, s.now().UTC()); err != nil {
		return View{}, mapError(err)
	}
	return s.Get(ctx, projectID, item.ID)
}

// Next returns the topmost startable item, or ok=false when none qualifies.
func (s *Service) Next(ctx context.Context, projectID domain.ProjectID, parentRef string) (View, bool, error) {
	parentID := ""
	if parentRef != "" {
		parent, err := s.resolve(ctx, projectID, parentRef)
		if err != nil {
			return View{}, false, err
		}
		parentID = parent.ID
	}
	item, ok, err := s.store.NextBacklogItem(ctx, projectID, parentID)
	if err != nil || !ok {
		return View{}, false, err
	}
	v, err := s.view(ctx, item)
	return v, err == nil, err
}

// IsStartable reports whether work may begin on the item: open, unheld and not
// a parent (running a parent runs its children). Used by run mode and spawn.
func (s *Service) IsStartable(ctx context.Context, item domain.BacklogItem) (bool, error) {
	n, err := s.store.CountBacklogChildren(ctx, item.ID)
	if err != nil {
		return false, err
	}
	return item.IsStartable(n), nil
}

// ItemProject returns the project an item belongs to, for caller scope checks.
func (s *Service) ItemProject(ctx context.Context, id string) (domain.ProjectID, error) {
	item, ok, err := s.store.GetBacklogItem(ctx, id)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", apierr.NotFound("BACKLOG_ITEM_NOT_FOUND", "Unknown backlog item")
	}
	return item.ProjectID, nil
}

func (s *Service) linkTarget(ctx context.Context, projectID domain.ProjectID, idOrRef string, kind domain.BacklogLinkKind, ref string) (domain.BacklogItem, string, error) {
	if !kind.Valid() {
		return domain.BacklogItem{}, "", apierr.Invalid("INVALID_BACKLOG_LINK_KIND", "Link kind must be session, pr, issue, artifact, memory, item or blocked_by", nil)
	}
	ref = strings.TrimSpace(ref)
	if ref == "" || utf8.RuneCountInString(ref) > 1000 {
		return domain.BacklogItem{}, "", apierr.Invalid("INVALID_BACKLOG_LINK_REF", "Link ref is required and must be at most 1000 characters", nil)
	}
	item, err := s.resolve(ctx, projectID, idOrRef)
	if err != nil {
		return domain.BacklogItem{}, "", err
	}
	if kind.IsItemLink() {
		other, err := s.resolve(ctx, projectID, ref)
		if err != nil {
			return domain.BacklogItem{}, "", err
		}
		ref = other.ID
	}
	return item, ref, nil
}

// resolve finds an item by id or by "B12"/"12" ref within a project.
func (s *Service) resolve(ctx context.Context, projectID domain.ProjectID, idOrRef string) (domain.BacklogItem, error) {
	idOrRef = strings.TrimSpace(idOrRef)
	if idOrRef == "" {
		return domain.BacklogItem{}, apierr.Invalid("INVALID_BACKLOG_REF", "Item ref is required", nil)
	}
	var (
		item domain.BacklogItem
		ok   bool
		err  error
	)
	if seq, isRef := domain.ParseBacklogRef(idOrRef); isRef && !strings.HasPrefix(idOrRef, "bl_") {
		if projectID == "" {
			return domain.BacklogItem{}, apierr.Invalid("INVALID_PROJECT_ID", "A project is required to resolve a ref", nil)
		}
		item, ok, err = s.store.GetBacklogItemByRef(ctx, projectID, seq)
	} else {
		item, ok, err = s.store.GetBacklogItem(ctx, idOrRef)
	}
	if err != nil {
		return domain.BacklogItem{}, err
	}
	if !ok || (projectID != "" && item.ProjectID != projectID) {
		return domain.BacklogItem{}, apierr.NotFound("BACKLOG_ITEM_NOT_FOUND", "Unknown backlog item")
	}
	return item, nil
}

func (s *Service) view(ctx context.Context, item domain.BacklogItem) (View, error) {
	full, ok, err := s.store.GetBacklogItem(ctx, item.ID)
	if err != nil {
		return View{}, err
	}
	if !ok {
		return View{}, apierr.NotFound("BACKLOG_ITEM_NOT_FOUND", "Unknown backlog item")
	}
	v := View{BacklogItem: full, Plan: s.plans.Summary(ctx, full.ID, true)}
	if full.ParentID != "" {
		if p, ok, err := s.store.GetBacklogItem(ctx, full.ParentID); err == nil && ok {
			v.ParentRef = p.Ref()
		}
	}
	n, err := s.store.CountBacklogChildren(ctx, full.ID)
	if err != nil {
		return View{}, err
	}
	v.ChildCount = n
	if v.ChildClosedCount, err = s.store.CountBacklogChildrenClosed(ctx, full.ID); err != nil {
		return View{}, err
	}
	for _, l := range full.Links {
		lv := LinkView{BacklogLink: l, Display: l.Ref}
		if l.Kind.IsItemLink() {
			if o, ok, err := s.store.GetBacklogItem(ctx, l.Ref); err == nil && ok {
				lv.Display = o.Ref()
			}
		}
		v.LinkViews = append(v.LinkViews, lv)
	}
	if v.OpenBlockers, err = s.openBlockers(ctx, full.Links); err != nil {
		return View{}, err
	}
	one := []View{v}
	if err := s.attachBadges(ctx, full.ProjectID, full.ID, one); err != nil {
		return View{}, err
	}
	return one[0], nil
}

func validateItem(i domain.BacklogItem) error {
	switch {
	case strings.TrimSpace(i.Title) == "" || utf8.RuneCountInString(i.Title) > domain.MaxBacklogTitle:
		return apierr.Invalid("INVALID_BACKLOG_TITLE", "Title is required and must be at most 200 characters", nil)
	case utf8.RuneCountInString(i.Note) > domain.MaxBacklogNote:
		return apierr.Invalid("INVALID_BACKLOG_NOTE", "Note must be at most 20000 characters", nil)
	case utf8.RuneCountInString(i.DoneWhen) > domain.MaxBacklogDoneWhen:
		return apierr.Invalid("INVALID_BACKLOG_DONE_WHEN", "Done-when must be at most 2000 characters", nil)
	case utf8.RuneCountInString(i.Hold) > domain.MaxBacklogHold:
		return apierr.Invalid("INVALID_BACKLOG_HOLD", "Hold must be at most 200 characters", nil)
	case utf8.RuneCountInString(i.AgentNotes) > domain.MaxBacklogAgentNotes:
		return apierr.Invalid("INVALID_BACKLOG_AGENT_NOTES", "Agent notes must be at most 20000 characters", nil)
	case !i.Scope.Valid():
		return apierr.Invalid("INVALID_BACKLOG_SCOPE", "Scope must be local, pr or pr-merge", nil)
	case !i.Origin.Kind.Valid():
		return apierr.Invalid("INVALID_BACKLOG_ORIGIN", "Unknown origin", nil)
	}
	return nil
}

func mapError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrBacklogNotFound):
		return apierr.NotFound("BACKLOG_ITEM_NOT_FOUND", "Unknown backlog item")
	case errors.Is(err, domain.ErrBacklogHierarchy):
		return apierr.Conflict("BACKLOG_HIERARCHY", cleanMsg(err), nil)
	case errors.Is(err, domain.ErrBacklogNotClosed):
		return apierr.Conflict("BACKLOG_NOT_CLOSED", cleanMsg(err), nil)
	case errors.Is(err, domain.ErrBacklogCycle):
		return apierr.Conflict("BACKLOG_CYCLE", "That blocked_by link would create a cycle", nil)
	case errors.Is(err, domain.ErrBacklogCrossProject):
		return apierr.Invalid("BACKLOG_CROSS_PROJECT", "Items must belong to the same project", nil)
	case errors.Is(err, domain.ErrBacklogInvalidField):
		return apierr.Invalid("INVALID_BACKLOG_FIELD", cleanMsg(err), nil)
	case errors.Is(err, domain.ErrBacklogInvalid):
		return apierr.Invalid("INVALID_BACKLOG_REQUEST", cleanMsg(err), nil)
	case errors.Is(err, domain.ErrProjectUnknown):
		return apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	return err
}

// cleanMsg drops the sentinel prefix ("backlog hierarchy violation: ") so the
// API message carries only the specific reason.
func cleanMsg(err error) string {
	msg := err.Error()
	if i := strings.Index(msg, ": "); i >= 0 {
		return msg[i+2:]
	}
	return msg
}

func truncateRunes(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
