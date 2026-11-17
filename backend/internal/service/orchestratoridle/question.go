package orchestratoridle

import (
	"context"
	"strconv"
	"time"
	"unicode/utf8"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// QuestionStore is what the unanswered-question check reads on top of Store.
type QuestionStore interface {
	// LastAssistantMessage is the final message of the session's latest main
	// turn, recorded by lifecycle for Chat and Terminal UI sessions alike.
	LastAssistantMessage(ctx context.Context, id domain.SessionID) (string, time.Time, bool, error)
	ListSessionLanes(ctx context.Context, project domain.ProjectID) (map[domain.SessionID]string, error)
}

// Owner files and clears owner notifications.
type Owner interface {
	Notify(ctx context.Context, intent ports.NotificationIntent) error
	Resolve(ctx context.Context, res ports.NotificationResolution) error
}

// replyTurnSlack is how far the stored reply may predate the idle state it
// ended in: further means a later turn ended with no text, so the reply is old.
const replyTurnSlack = time.Minute

const maxQuestionBody = 200

// questionNotify tells the owner when an orchestrator went idle on a reply that
// asks them something, no ask was filed, the owner has not written since and
// no worker, review or ask is in flight (B750). Without this an orchestrator
// that forgot `tallpine ask` sits silent until the owner happens to look.
// Always on: it only fires on a question to the owner, once per reply.
func (s *Sweeper) questionNotify(ctx context.Context, rec domain.SessionRecord) error {
	if s.deps.Questions == nil || s.deps.Owner == nil {
		return nil
	}
	text, at, ok, err := s.deps.Questions.LastAssistantMessage(ctx, rec.ID)
	if err != nil || !ok {
		return err
	}
	key := strconv.FormatInt(at.UTC().UnixNano(), 10)
	// The owner wrote after the reply: it is answered.
	if !rec.Metadata.LatestUserPromptAt.Before(at) {
		return s.clearQuestion(ctx, rec.ID)
	}
	if s.questionKey(rec.ID) == key {
		return nil // already told the owner about this reply
	}
	if rec.Activity.State != domain.ActivityIdle || rec.Activity.LastActivityAt.Sub(at) > replyTurnSlack ||
		s.deps.Now().Sub(rec.Activity.LastActivityAt) < domain.OwnerQuestionNotifyAfter || !domain.AsksOwner(text) {
		return nil
	}
	inFlight, err := s.inFlight(ctx, rec)
	if err != nil || inFlight {
		return err
	}
	if err := s.clearQuestion(ctx, rec.ID); err != nil { // an older reply's notice
		return err
	}
	lanes, err := s.deps.Questions.ListSessionLanes(ctx, rec.ProjectID)
	if err != nil {
		return err
	}
	prefix := domain.OwnerQuestionSourcePrefix
	if lanes[rec.ID] != "" {
		prefix = domain.OwnerQuestionLaneSourcePrefix
	}
	body := text
	if utf8.RuneCountInString(body) > maxQuestionBody {
		body = "…" + string([]rune(body)[utf8.RuneCountInString(body)-maxQuestionBody:])
	}
	if err := s.deps.Owner.Notify(ctx, ports.NotificationIntent{
		Type: domain.NotificationPlanWaiting, SessionID: rec.ID, ProjectID: rec.ProjectID,
		SourceKey: prefix + string(rec.ID) + ":" + key,
		Title:     "Your orchestrator is waiting on your answer", Body: body,
	}); err != nil {
		return err
	}
	s.mu.Lock()
	s.questions[rec.ID] = key
	s.mu.Unlock()
	s.deps.Log.Info("owner notified of an unanswered orchestrator question", "session", rec.ID)
	return nil
}

func (s *Sweeper) questionKey(id domain.SessionID) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.questions[id]
}

// clearQuestion resolves id's owner-question notifications. The notice may
// predate this process (a daemon restart empties questions), so the first
// clear after start-up always resolves; later ones only after a new notice.
func (s *Sweeper) clearQuestion(ctx context.Context, id domain.SessionID) error {
	s.mu.Lock()
	skip := s.questions[id] == "" && s.cleared[id]
	s.mu.Unlock()
	if skip {
		return nil
	}
	for _, prefix := range []string{domain.OwnerQuestionSourcePrefix, domain.OwnerQuestionLaneSourcePrefix} {
		if err := s.deps.Owner.Resolve(ctx, ports.NotificationResolution{
			Type: domain.NotificationPlanWaiting, SourceKeyPrefix: prefix + string(id) + ":",
		}); err != nil {
			return err
		}
	}
	s.mu.Lock()
	delete(s.questions, id)
	s.cleared[id] = true
	s.mu.Unlock()
	return nil
}
