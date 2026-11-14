// Package ask tracks the questions orchestrators put to the owner (B698): each
// is a daemon record owned by project and lane, shown as a "Waiting on you"
// strip, answered on the card or relayed from chat, and deduped across the
// project's orchestrators.
package ask

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/service/ownermsg"
)

// Store is the ask service's persistence surface; the SQLite store satisfies it.
type Store interface {
	ownermsg.Source
	CreateOwnerAsk(ctx context.Context, a domain.OwnerAsk, check func(existing []domain.OwnerAsk, a *domain.OwnerAsk) error) (domain.OwnerAsk, error)
	SetOwnerAskAnchor(ctx context.Context, id, turnID, activityID string) error
	GetOwnerAsk(ctx context.Context, project domain.ProjectID, ref string) (domain.OwnerAsk, bool, error)
	ListOwnerAsks(ctx context.Context, project domain.ProjectID) ([]domain.OwnerAsk, error)
	ListOpenOwnerAsks(ctx context.Context, project domain.ProjectID) ([]domain.OwnerAsk, error)
	SaveOwnerAskTransition(ctx context.Context, a domain.OwnerAsk) error
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	SessionLane(ctx context.Context, id domain.SessionID) (domain.SessionLane, error)
	GetBacklogItem(ctx context.Context, id string) (domain.BacklogItem, bool, error)
}

// Anchorer records an ask as an activity on the asking session's turn in
// flight. It returns an error (ErrNoTurn) when no turn is running.
type Anchorer interface {
	RecordAsk(ctx context.Context, session domain.SessionID, ask domain.OwnerAsk) (turnID, activityID string, err error)
}

// Router posts the owner's answer into the chat of the live orchestrator of a
// project and lane, as the owner's own message.
type Router interface {
	SendOwnerReply(ctx context.Context, project domain.ProjectID, lane, text string) error
}

// Notifier tells the owner about asks. Opened is called once per new ask;
// Closed when it is answered or withdrawn.
type Notifier interface {
	Opened(ctx context.Context, ask domain.OwnerAsk)
	Closed(ctx context.Context, ask domain.OwnerAsk)
}

// Actor is who performs a request, taken from the caller's credentials.
type Actor struct {
	Kind      domain.BacklogOriginKind
	SessionID string
}

// Deps wires the service.
type Deps struct {
	Store    Store
	Anchor   Anchorer
	Router   Router
	Notifier Notifier
	Log      *slog.Logger
	Now      func() time.Time
}

// Service is the ask service.
type Service struct {
	d Deps
}

// New builds the service.
func New(d Deps) *Service {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{d: d}
}

// CreateInput is a new ask.
type CreateInput struct {
	Text        string
	Reason      string
	About       string
	Options     []domain.PlanOption
	Recommended string
	// Reask lets the ask repeat an answered one; the card shows it as "asked again".
	Reask string
}

// Create files an ask from an orchestrator. A match with an open ask is always
// refused; a match with an answered one only without Reask.
func (s *Service) Create(ctx context.Context, project domain.ProjectID, actor Actor, in CreateInput) (domain.OwnerAsk, error) {
	if actor.Kind != domain.BacklogOriginOrchestrator || actor.SessionID == "" {
		return domain.OwnerAsk{}, apierr.Forbidden("ASK_ORCHESTRATOR_ONLY", "Only an orchestrator asks the owner; a worker reports to its orchestrator")
	}
	in.Text, in.Reason, in.About, in.Reask = strings.TrimSpace(in.Text), strings.TrimSpace(in.Reason), strings.TrimSpace(in.About), strings.TrimSpace(in.Reask)
	if err := domain.ValidateAsk(in.Text, in.Reason, in.About, in.Options, in.Recommended, in.Reask); err != nil {
		return domain.OwnerAsk{}, apierr.Invalid("ASK_INVALID", err.Error(), nil)
	}
	sid := domain.SessionID(actor.SessionID)
	rec, ok, err := s.d.Store.GetSession(ctx, sid)
	if err != nil {
		return domain.OwnerAsk{}, err
	}
	if !ok || rec.ProjectID != project {
		return domain.OwnerAsk{}, apierr.Forbidden("ASK_WRONG_PROJECT", "This session is not an orchestrator of that project")
	}
	lane, err := s.d.Store.SessionLane(ctx, sid)
	if err != nil {
		return domain.OwnerAsk{}, err
	}
	laneRef := ""
	if lane.LaneItemID != "" {
		it, ok, err := s.d.Store.GetBacklogItem(ctx, lane.LaneItemID)
		if err != nil || !ok {
			return domain.OwnerAsk{}, apierr.Internal("ASK_LANE_UNKNOWN", "Could not resolve this orchestrator's lane; try again")
		}
		laneRef = it.Ref()
	}
	now := s.d.Now()
	a := domain.OwnerAsk{
		ID: uuid.NewString(), ProjectID: project, Lane: lane.LaneItemID, LaneRef: laneRef, SessionID: sid,
		Text: in.Text, Reason: in.Reason, Options: in.Options, Recommended: in.Recommended, About: in.About,
		State: domain.AskOpen, CreatedAt: now, UpdatedAt: now,
	}
	a, err = s.d.Store.CreateOwnerAsk(ctx, a, func(existing []domain.OwnerAsk, a *domain.OwnerAsk) error {
		return checkCreate(existing, a, in.Reask)
	})
	if err != nil {
		return domain.OwnerAsk{}, err
	}
	// The inline card lands on the turn the orchestrator is running; with no
	// turn in flight the ask stays valid, just without a jump target.
	if s.d.Anchor != nil {
		turn, act, err := s.d.Anchor.RecordAsk(ctx, sid, a)
		switch {
		case err != nil:
			s.d.Log.Info("ask has no inline card", "ask", a.ID, "error", err)
		default:
			if err := s.d.Store.SetOwnerAskAnchor(ctx, a.ID, turn, act); err != nil {
				s.d.Log.Warn("could not record an ask's anchor", "ask", a.ID, "error", err)
			} else {
				a.AnchorTurnID, a.AnchorActivityID = turn, act
			}
		}
	}
	if s.d.Notifier != nil {
		s.d.Notifier.Opened(ctx, a)
	}
	return a, nil
}

// checkCreate applies the dedupe and cap rules to the project's asks.
func checkCreate(existing []domain.OwnerAsk, a *domain.OwnerAsk, reask string) error {
	laneOpen := 0
	var answered *domain.OwnerAsk
	for i := range existing {
		e := &existing[i]
		if e.State == domain.AskOpen {
			if e.Lane == a.Lane {
				laneOpen++
			}
			if e.Matches(a.About, a.Text) {
				return apierr.Conflict("ASK_ALREADY_OPEN", fmt.Sprintf("%s already asks this and is still open: wait for the owner, do not ask again", e.Ref), askDetails(*e))
			}
		}
		if e.State == domain.AskAnswered && e.Matches(a.About, a.Text) {
			answered = e // later asks overwrite earlier: the newest answer wins
		}
	}
	if answered != nil {
		if reask == "" {
			return apierr.Conflict("ASK_ALREADY_ANSWERED", fmt.Sprintf("The owner already answered this as %s (%s); use that answer, or add --reask \"<why>\" if things changed", answered.Ref, answered.Answer.Text), askDetails(*answered))
		}
		a.ReaskOf, a.ReaskReason = answered.Ref, reask
	}
	if laneOpen >= domain.MaxOpenAsks {
		return apierr.Conflict("ASK_LIMIT", fmt.Sprintf("There are already %d open asks in this chat: wait for answers or withdraw some (tallpine ask ls)", domain.MaxOpenAsks), nil)
	}
	return nil
}

func askDetails(a domain.OwnerAsk) map[string]any {
	d := map[string]any{"ref": a.Ref, "state": string(a.State), "text": a.Text}
	if a.Answer != nil {
		d["answer"] = a.Answer.Text
	}
	return d
}

// List returns the project's asks, oldest first.
func (s *Service) List(ctx context.Context, project domain.ProjectID, openOnly bool) ([]domain.OwnerAsk, error) {
	if openOnly {
		return s.d.Store.ListOpenOwnerAsks(ctx, project)
	}
	return s.d.Store.ListOwnerAsks(ctx, project)
}

// AnswerInput answers an ask: an option id or free text, and for an
// orchestrator the owner message the answer rests on.
type AnswerInput struct {
	Option       string
	Other        string
	OwnerMessage string
}

func (s *Service) load(ctx context.Context, project domain.ProjectID, ref string) (domain.OwnerAsk, error) {
	a, ok, err := s.d.Store.GetOwnerAsk(ctx, project, ref)
	if err != nil {
		return a, err
	}
	if !ok {
		return a, apierr.NotFound("ASK_NOT_FOUND", "No ask "+ref+" in this project")
	}
	return a, nil
}

func notOpen(a domain.OwnerAsk) error {
	return apierr.Conflict("ASK_NOT_OPEN", fmt.Sprintf("%s is %s: only an open ask can change", a.Ref, a.State), askDetails(a))
}

// Answer records the owner's answer. The owner answers directly and the answer
// is posted to the asking chat; an orchestrator relays what the owner typed and
// needs that owner message. Of two racing answers the first wins.
func (s *Service) Answer(ctx context.Context, project domain.ProjectID, actor Actor, ref string, in AnswerInput) (domain.OwnerAsk, bool, error) {
	a, err := s.load(ctx, project, ref)
	if err != nil {
		return a, false, err
	}
	if a.State != domain.AskOpen {
		return a, false, notOpen(a)
	}
	ans, err := a.ResolveAskAnswer(in.Option, in.Other)
	if err != nil {
		return a, false, apierr.Invalid("ASK_ANSWER_INVALID", err.Error(), nil)
	}
	relayed := false
	switch actor.Kind {
	case domain.BacklogOriginHuman:
	case domain.BacklogOriginOrchestrator:
		relayed = true
		mid, err := ownermsg.Verify(ctx, s.d.Store, ownermsg.Check{
			ProjectID: project, Since: a.CreatedAt, ID: in.OwnerMessage, CodePrefix: "ASK",
			Verb: "answers", Changed: "the ask was asked", Older: "the ask",
		})
		if err != nil {
			return a, false, err
		}
		ans.Relayed, ans.MessageID = true, mid
	default:
		return a, false, apierr.Forbidden("ASK_ANSWER_FORBIDDEN", "Only the owner answers an ask; an orchestrator relays the owner's message")
	}
	closed, err := a.Answered(ans, s.d.Now())
	if err != nil {
		return a, false, notOpen(a)
	}
	if err := s.d.Store.SaveOwnerAskTransition(ctx, closed); err != nil {
		if errors.Is(err, domain.ErrAskNotOpen) {
			fresh, _ := s.load(ctx, project, ref)
			return fresh, false, notOpen(fresh)
		}
		return a, false, err
	}
	if s.d.Notifier != nil {
		s.d.Notifier.Closed(ctx, closed)
	}
	// A card answer goes to the orchestrator that will act on it; a relayed one
	// came from that chat already.
	// The answer is recorded either way (`tallpine ask ls --all` shows it to any
	// orchestrator); a failed post is reported so the card can tell the owner to
	// say it in chat.
	failed := false
	if !relayed && s.d.Router != nil {
		if err := s.d.Router.SendOwnerReply(ctx, project, closed.Lane, "Re "+closed.Ref+": "+ans.Text); err != nil {
			s.d.Log.Warn("ask answer not delivered to an orchestrator", "ask", closed.ID, "error", err)
			failed = true
		}
	}
	return closed, failed, nil
}

// Withdraw drops an open ask with a reason. The owner may withdraw any ask; an
// orchestrator only those of its own lane (or the main chat).
func (s *Service) Withdraw(ctx context.Context, project domain.ProjectID, actor Actor, ref, reason string) (domain.OwnerAsk, error) {
	if actor.Kind != domain.BacklogOriginHuman && actor.Kind != domain.BacklogOriginOrchestrator {
		return domain.OwnerAsk{}, apierr.Forbidden("ASK_WITHDRAW_FORBIDDEN", "Only an orchestrator or the owner withdraws an ask")
	}
	reason = strings.TrimSpace(reason)
	a, err := s.load(ctx, project, ref)
	if err != nil {
		return a, err
	}
	// An ask belongs to its project and lane: an orchestrator withdraws only
	// its own chat's asks; the owner may withdraw any.
	if actor.Kind == domain.BacklogOriginOrchestrator {
		lane, err := s.d.Store.SessionLane(ctx, domain.SessionID(actor.SessionID))
		if err != nil {
			return a, err
		}
		if lane.LaneItemID != a.Lane {
			return a, apierr.Forbidden("ASK_WRONG_LANE", a.Ref+" belongs to another chat (lane): only the orchestrator that asked it, or the owner, withdraws it")
		}
	}
	return s.withdraw(ctx, a, reason)
}

func (s *Service) withdraw(ctx context.Context, a domain.OwnerAsk, reason string) (domain.OwnerAsk, error) {
	if a.State != domain.AskOpen {
		return a, notOpen(a)
	}
	closed, err := a.Withdrawn(reason, s.d.Now())
	if err != nil {
		return a, notOpen(a)
	}
	if err := s.d.Store.SaveOwnerAskTransition(ctx, closed); err != nil {
		if errors.Is(err, domain.ErrAskNotOpen) {
			return a, notOpen(a)
		}
		return a, err
	}
	if s.d.Notifier != nil {
		s.d.Notifier.Closed(ctx, closed)
	}
	return closed, nil
}

// WithdrawLane withdraws the open asks of a lane that ended, and returns how
// many it closed. A failure on one ask is logged and does not stop the rest.
func (s *Service) WithdrawLane(ctx context.Context, project domain.ProjectID, lane string) int {
	open, err := s.d.Store.ListOpenOwnerAsks(ctx, project)
	if err != nil {
		s.d.Log.Warn("could not list asks of an ended lane", "lane", lane, "error", err)
		return 0
	}
	n := 0
	for _, a := range open {
		if a.Lane != lane {
			continue
		}
		if _, err := s.withdraw(ctx, a, "the lane ended"); err != nil {
			if code := apiCode(err); code != "ASK_NOT_OPEN" {
				s.d.Log.Warn("could not withdraw an ask of an ended lane", "ask", a.ID, "error", err)
			}
			continue
		}
		n++
	}
	return n
}

func apiCode(err error) string {
	var ae *apierr.Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	return ""
}
