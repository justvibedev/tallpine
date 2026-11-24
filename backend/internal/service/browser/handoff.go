package browser

import (
	"context"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/browserruntime"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// Handoff limits (B858), shared with the CLI (backend/internal/cli/browser_commands_handoff.go)
// and the desktop (frontend/src/main/browser-handoff.ts).
const (
	HandoffMaxReason     = 500
	HandoffMaxUntilURL   = 2048
	HandoffMinTimeout    = 10 * time.Second
	HandoffMaxTimeout    = 30 * time.Minute
	handoffRuntimeGrace  = 15 * time.Second
	handoffSourcePrefix  = "browser-handoff:"
	handoffActionName    = "handoff"
	handoffInvalidReason = "BROWSER_HANDOFF_INVALID"
)

// notifier raises and closes the needs-you notification of a handoff; the
// daemon passes its notify.Manager, which also pushes it to paired phones.
type notifier interface {
	Notify(ctx context.Context, intent ports.NotificationIntent) error
	Resolve(ctx context.Context, res ports.NotificationResolution) error
}

// WithNotifier sets the sink for handoff notifications; without one a handoff
// still shows its banner but raises no notification.
func (s *Service) WithNotifier(n notifier, log *slog.Logger) *Service {
	s.notifier = n
	s.log = log
	return s
}

// handoffArgs validates and normalizes a handoff's arguments: a reason, an
// optional URL glob, and a timeout in milliseconds.
func handoffArgs(args map[string]interface{}) (map[string]interface{}, time.Duration, error) {
	invalid := func(message string) error { return apierr.Invalid(handoffInvalidReason, message, nil) }
	reason, _ := args["reason"].(string)
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, 0, invalid("reason is required: say what the person should do")
	}
	if utf8.RuneCountInString(reason) > HandoffMaxReason {
		return nil, 0, invalid("reason must be at most 500 characters")
	}
	out := map[string]interface{}{"reason": reason}
	if raw, present := args["untilUrl"]; present && raw != nil {
		untilURL, ok := raw.(string)
		untilURL = strings.TrimSpace(untilURL)
		if !ok || untilURL == "" || len(untilURL) > HandoffMaxUntilURL {
			return nil, 0, invalid("untilUrl must be a non-empty URL glob of at most 2048 characters")
		}
		out["untilUrl"] = untilURL
	}
	millis, ok := args["timeoutMs"].(float64)
	if !ok {
		if n, isInt := args["timeoutMs"].(int); isInt {
			millis, ok = float64(n), true
		}
	}
	timeout := time.Duration(millis) * time.Millisecond
	if !ok || millis != float64(int64(millis)) || timeout < HandoffMinTimeout || timeout > HandoffMaxTimeout {
		return nil, 0, invalid("timeoutMs must be a whole number between 10000 and 1800000")
	}
	out["timeoutMs"] = timeout.Milliseconds()
	return out, timeout, nil
}

// executeHandoff raises a needs-you notification, waits for the desktop to
// report Continue, a URL match or the timeout, then resolves the notification.
func (s *Service) executeHandoff(
	ctx context.Context,
	session domain.Session,
	backend string,
	args map[string]interface{},
) (browserruntime.Result, error) {
	normalized, timeout, err := handoffArgs(args)
	if err != nil {
		return browserruntime.Result{}, err
	}
	// The desktop runs one command per session at a time, so a second
	// handoff would queue behind the first with its notification already
	// raised and no banner to act on: refuse it here instead.
	if !s.claimHandoff(session.ID) {
		return browserruntime.Result{}, apierr.Conflict("BROWSER_HANDOFF_ACTIVE",
			"This session is already waiting on the person; wait for that handoff to end", nil)
	}
	defer s.releaseHandoff(session.ID)
	reason, _ := normalized["reason"].(string)
	sourceKey := handoffSourcePrefix + string(session.ID) + ":" + uuid.NewString()
	if s.notifier != nil {
		err := s.notifier.Notify(ctx, ports.NotificationIntent{
			Type:               domain.NotificationNeedsInput,
			SessionID:          session.ID,
			ProjectID:          session.ProjectID,
			SourceKey:          sourceKey,
			SessionDisplayName: session.DisplayName,
			Body:               "Browser: " + reason,
		})
		if err != nil {
			s.warn("browser handoff notification failed", session.ID, err)
		} else {
			defer func() {
				// The request may be gone (the agent quit): resolve regardless.
				resolveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer cancel()
				if err := s.notifier.Resolve(resolveCtx, ports.NotificationResolution{
					Type: domain.NotificationNeedsInput, SourceKeyPrefix: sourceKey,
				}); err != nil {
					s.warn("browser handoff resolve failed", session.ID, err)
				}
			}()
		}
	}
	// The desktop enforces the timeout; this backstop only ends a wait whose
	// runtime went silent.
	waitCtx, cancel := context.WithTimeout(ctx, timeout+handoffRuntimeGrace)
	defer cancel()
	return s.runtime.ExecuteOn(waitCtx, session.ID, backend, handoffActionName, normalized)
}

// claimHandoff marks a handoff in flight for sessionID; false when one already is.
func (s *Service) claimHandoff(sessionID domain.SessionID) bool {
	s.handoffMu.Lock()
	defer s.handoffMu.Unlock()
	if _, busy := s.handoffs[sessionID]; busy {
		return false
	}
	if s.handoffs == nil {
		s.handoffs = map[domain.SessionID]struct{}{}
	}
	s.handoffs[sessionID] = struct{}{}
	return true
}

func (s *Service) releaseHandoff(sessionID domain.SessionID) {
	s.handoffMu.Lock()
	defer s.handoffMu.Unlock()
	delete(s.handoffs, sessionID)
}

func (s *Service) warn(message string, sessionID domain.SessionID, err error) {
	if s.log != nil {
		s.log.Warn(message, "session", sessionID, "err", err)
	}
}
