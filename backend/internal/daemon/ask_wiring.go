package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"unicode/utf8"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	asksvc "tallpine.eu/tallpine/backend/internal/service/ask"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// newAskService builds the tracked owner-ask service (B698). The chat service
// anchors an ask on the asking turn; owner answers go to the live orchestrator
// of the ask's project and lane as the owner's own message; a new ask becomes
// one critical owner notification.
func newAskService(store *sqlite.Store, anchor asksvc.Anchorer, sessions backlogRunSessions, owner planOwner, handlers map[string]func(context.Context, domain.OwnerAsk) error, log *slog.Logger) *asksvc.Service {
	d := asksvc.Deps{Store: store, Anchor: anchor, Log: log}
	if sessions != nil {
		d.Router = askRouter{sessions: sessions, pick: orchestratorPicker(store)}
	}
	if owner != nil {
		d.Notifier = askNotifier{owner: owner, pick: orchestratorPicker(store)}
	}
	d.AnswerHandlers = handlers
	return asksvc.New(d)
}

// askRouter posts an owner's card answer into the chat of the live
// orchestrator that owns the ask's lane, found by project and lane at answer
// time so it survives a restart of the asking session.
type askRouter struct {
	sessions backlogRunSessions
	pick     func(context.Context, domain.ProjectID, string) (domain.SessionID, bool)
}

func (r askRouter) SendOwnerReply(ctx context.Context, project domain.ProjectID, lane, text string) error {
	to, ok := r.pick(ctx, project, lane)
	if !ok {
		return fmt.Errorf("no live orchestrator for lane %q", lane)
	}
	// The owner's own words: queued behind a running turn like any chat message.
	return r.sessions.SendWithOptions(ctx, to, text, nil, ports.MessageDeliveryOptions{AuthoredByUser: true})
}

// askNotifier files one plan_waiting-kind notification per new ask (the
// critical urgency of a waiting plan) and resolves it once the ask closes.
type askNotifier struct {
	owner planOwner
	// pick finds the project's orchestrator, whose session a daemon ask's
	// notification hangs on: a notification needs a session row.
	pick func(context.Context, domain.ProjectID, string) (domain.SessionID, bool)
}

func (n askNotifier) Opened(ctx context.Context, a domain.OwnerAsk) {
	prefix := domain.AskSourcePrefix
	if a.Lane != "" {
		prefix = domain.AskLaneSourcePrefix
	}
	body := a.Text
	if utf8.RuneCountInString(body) > 200 {
		body = string([]rune(body)[:200]) + "…"
	}
	title, sid := "Your orchestrator has a question ("+a.Ref+")", a.SessionID
	if a.FiledByDaemon() {
		title = "Tallpine has a question (" + a.Ref + ")"
		var ok bool
		if sid, ok = n.pick(ctx, a.ProjectID, ""); !ok {
			return // no session to hang the notification on: the card still shows in chat
		}
	}
	if err := n.owner.Notify(ctx, ports.NotificationIntent{
		Type: domain.NotificationPlanWaiting, SessionID: sid, ProjectID: a.ProjectID,
		SourceKey: prefix + a.ID, Title: title, Body: body,
	}); err != nil {
		slog.Warn("could not notify the owner of an ask", "ask", a.ID, "error", err)
	}
}

func (n askNotifier) Closed(ctx context.Context, a domain.OwnerAsk) {
	for _, prefix := range []string{domain.AskSourcePrefix, domain.AskLaneSourcePrefix} {
		if err := n.owner.Resolve(ctx, ports.NotificationResolution{Type: domain.NotificationPlanWaiting, SourceKeyPrefix: prefix + a.ID}); err != nil {
			slog.Warn("could not clear an ask's owner notification", "ask", a.ID, "error", err)
		}
	}
}
