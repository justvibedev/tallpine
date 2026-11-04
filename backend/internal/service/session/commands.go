package session

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// Send delegates agent messaging to the internal manager. attachment is an
// optional inline image (e.g. a browser-annotation snapshot) written into the
// session worktree and referenced from the delivered message.
func (s *Service) Send(ctx context.Context, id domain.SessionID, message string, attachment *ports.SpawnAttachment) error {
	return toAPIError(s.manager.Send(ctx, id, message, attachment))
}

// SendWithOptions preserves authorship facts supplied by trusted UI surfaces.
func (s *Service) SendWithOptions(ctx context.Context, id domain.SessionID, message string, attachment *ports.SpawnAttachment, options ports.MessageDeliveryOptions) error {
	return toAPIError(s.manager.SendWithOptions(ctx, id, message, attachment, options))
}

// SendWithReceipt is SendWithOptions that also reports the delivery outcome.
func (s *Service) SendWithReceipt(ctx context.Context, id domain.SessionID, message string, attachment *ports.SpawnAttachment, options ports.MessageDeliveryOptions) (ports.MessageReceipt, error) {
	if m, ok := s.manager.(interface {
		SendWithReceipt(ctx context.Context, id domain.SessionID, message string, attachment *ports.SpawnAttachment, options ports.MessageDeliveryOptions) (ports.MessageReceipt, error)
	}); ok {
		receipt, err := m.SendWithReceipt(ctx, id, message, attachment, options)
		return receipt, toAPIError(err)
	}
	return ports.MessageReceipt{}, toAPIError(s.manager.SendWithOptions(ctx, id, message, attachment, options))
}

// Rename updates the user-facing session display name.
//
// The project orchestrator is refused: the app labels it "Orchestrator" rather
// than by its display name, so a rename looked like it did nothing. The check is
// on the target's kind because the request carries no caller, which also stops
// the orchestrator agent from renaming itself through `tallpine session rename`.
func (s *Service) Rename(ctx context.Context, id domain.SessionID, displayName string) error {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return apierr.Invalid("DISPLAY_NAME_REQUIRED", "Display name is required", nil)
	}
	if utf8.RuneCountInString(displayName) > maxDisplayNameLen {
		return apierr.Invalid("DISPLAY_NAME_TOO_LONG", fmt.Sprintf("Display name must be %d characters or fewer", maxDisplayNameLen), nil)
	}
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return fmt.Errorf("rename %s: %w", id, err)
	}
	if !ok {
		return apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	if rec.Kind == domain.KindOrchestrator {
		return apierr.Invalid("ORCHESTRATOR_RENAME_UNSUPPORTED", "The project orchestrator cannot be renamed", nil)
	}
	renamed, err := s.store.RenameSession(ctx, id, displayName, time.Now().UTC())
	if err != nil {
		wrapped := fmt.Errorf("rename %s: %w", id, err)
		s.recordRenamed(ctx, id, rec.DisplayName, displayName, domain.ActivityStatusFailed, wrapped)
		return wrapped
	}
	if !renamed {
		missing := apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
		s.recordRenamed(ctx, id, rec.DisplayName, displayName, domain.ActivityStatusFailed, missing)
		return missing
	}
	s.recordRenamed(ctx, id, rec.DisplayName, displayName, domain.ActivityStatusCompleted, nil)
	return nil
}

// SetPreview persists the browser preview URL for a session and returns the
// refreshed read model. The URL is taken verbatim from the caller (the
// controller resolves it, either an explicit target or an autodetected entry).
// Persisting it via the store fans out a session_updated CDC event through the
// sessions_cdc_update trigger, mirroring how other session mutations surface on
// the live event stream.
func (s *Service) SetPreview(ctx context.Context, id domain.SessionID, previewURL string) (domain.Session, error) {
	updated, err := s.store.SetSessionPreviewURL(ctx, id, previewURL, time.Now().UTC())
	if err != nil {
		return domain.Session{}, fmt.Errorf("set preview url %s: %w", id, err)
	}
	if !updated {
		return domain.Session{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	return s.Get(ctx, id)
}

// SetTerminateOnPRMerge persists the user's merge-completion lifecycle policy
// and returns the refreshed read model.
func (s *Service) SetTerminateOnPRMerge(ctx context.Context, id domain.SessionID, terminate bool) (domain.Session, error) {
	updated, err := s.store.SetSessionTerminateOnPRMerge(ctx, id, terminate, time.Now().UTC())
	if err != nil {
		return domain.Session{}, fmt.Errorf("set terminate-on-pr-merge %s: %w", id, err)
	}
	if !updated {
		return domain.Session{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	return s.Get(ctx, id)
}

// SetAutoInjectReview persists whether new SCM and Tallpine review feedback should be sent to the session.
func (s *Service) SetAutoInjectReview(ctx context.Context, id domain.SessionID, autoInject bool) (domain.Session, error) {
	updated, err := s.store.SetSessionAutoInjectReview(ctx, id, autoInject, time.Now().UTC())
	if err != nil {
		return domain.Session{}, fmt.Errorf("set auto-inject review %s: %w", id, err)
	}
	if !updated {
		return domain.Session{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	return s.Get(ctx, id)
}

// SetAutoInjectCI persists the default automatic CI-failure injection policy
// for PRs created after this update. Existing PRs keep their captured policy.
func (s *Service) SetAutoInjectCI(ctx context.Context, id domain.SessionID, autoInject bool) (domain.Session, error) {
	updated, err := s.store.SetSessionAutoInjectCI(ctx, id, autoInject, time.Now().UTC())
	if err != nil {
		return domain.Session{}, fmt.Errorf("set auto-inject CI %s: %w", id, err)
	}
	if !updated {
		return domain.Session{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	return s.Get(ctx, id)
}

// Pin marks a session as pinned and returns the refreshed read model.
func (s *Service) Pin(ctx context.Context, id domain.SessionID) (domain.Session, error) {
	now := s.now()
	updated, err := s.store.SetSessionPinned(ctx, id, true, &now, now)
	if err != nil {
		return domain.Session{}, fmt.Errorf("pin %s: %w", id, err)
	}
	if !updated {
		return domain.Session{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	return s.Get(ctx, id)
}

// Unpin marks a session as unpinned and returns the refreshed read model.
func (s *Service) Unpin(ctx context.Context, id domain.SessionID) (domain.Session, error) {
	now := s.now()
	updated, err := s.store.SetSessionPinned(ctx, id, false, nil, now)
	if err != nil {
		return domain.Session{}, fmt.Errorf("unpin %s: %w", id, err)
	}
	if !updated {
		return domain.Session{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	return s.Get(ctx, id)
}

// SetReviewerHarness persists the reviewer selected for this session. Empty
// clears the preference and restores the project-level fallback.
func (s *Service) SetReviewerHarness(ctx context.Context, id domain.SessionID, harness domain.ReviewerHarness, config domain.AgentConfig) (domain.Session, error) {
	if harness != "" && !harness.IsKnown() {
		return domain.Session{}, apierr.Invalid("UNKNOWN_REVIEWER_HARNESS", "Unknown reviewer harness", nil)
	}
	if err := config.Validate(); err != nil {
		return domain.Session{}, apierr.Invalid("INVALID_REVIEWER_CONFIG", "Invalid reviewer config", map[string]any{"detail": err.Error()})
	}
	updated, err := s.store.SetSessionReviewerConfig(ctx, id, harness, config, time.Now().UTC())
	if err != nil {
		return domain.Session{}, fmt.Errorf("set reviewer config %s: %w", id, err)
	}
	if !updated {
		return domain.Session{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	return s.Get(ctx, id)
}

// SetAutoReview enables or disables daemon-side review automation for a session.
func (s *Service) SetAutoReview(ctx context.Context, id domain.SessionID, enabled bool) (domain.Session, error) {
	updated, err := s.store.SetSessionAutoReview(ctx, id, enabled, s.now())
	if err != nil {
		return domain.Session{}, fmt.Errorf("set auto review %s: %w", id, err)
	}
	if !updated {
		return domain.Session{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	return s.Get(ctx, id)
}

// Cleanup delegates terminal workspace cleanup to the internal manager and
// reports both reclaimed and preserved (skipped) workspaces.
func (s *Service) Cleanup(ctx context.Context, project domain.ProjectID) (CleanupOutcome, error) {
	res, err := s.manager.Cleanup(ctx, project)
	if err != nil {
		return CleanupOutcome{}, err
	}
	out := CleanupOutcome{
		Cleaned:     res.Cleaned,
		AlreadyGone: res.AlreadyGone,
		Skipped:     make([]CleanupSkipped, 0, len(res.Skipped)),
	}
	if out.Cleaned == nil {
		out.Cleaned = []domain.SessionID{}
	}
	if out.AlreadyGone == nil {
		out.AlreadyGone = []domain.SessionID{}
	}
	for _, skip := range res.Skipped {
		out.Skipped = append(out.Skipped, CleanupSkipped{SessionID: skip.SessionID, Reason: skip.Reason})
	}
	return out, nil
}

// TeardownProject stops every live session in a project concurrently, then asks
// the session manager to reclaim terminal workspaces. The expensive per-session
// work (agent/runtime shutdown, controller teardown) is independent, so running
// the kills in parallel is what makes removing a many-session project fast;
// sessions of the same project that reach the shared repository are serialized
// by the workspace adapter's per-repo teardown lock. Dirty worktrees are
// preserved by Kill and Cleanup; callers only see hard teardown failures.
func (s *Service) TeardownProject(ctx context.Context, project domain.ProjectID) error {
	recs, err := s.listRecords(ctx, project)
	if err != nil {
		return err
	}
	errs := make([]error, len(recs))
	var wg sync.WaitGroup
	for i := range recs {
		if recs[i].IsTerminated {
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := s.Kill(ctx, recs[i].ID); err != nil {
				errs[i] = err
			}
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	_, err = s.Cleanup(ctx, project)
	return err
}
