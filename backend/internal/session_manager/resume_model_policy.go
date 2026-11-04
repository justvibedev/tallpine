package sessionmanager

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

var (
	// ErrResumeModelDenied refuses a resume whose persisted model the project's
	// policy now denies, when no allowed fallback model exists. The session stays
	// stopped; retrying cannot help until the policy or the model changes.
	ErrResumeModelDenied = errors.New("session: the saved model is denied by project policy and no allowed fallback exists")
	// ErrResumeModelPolicyUnavailable refuses a resume because the project's
	// config could not be read (fail closed).
	ErrResumeModelPolicyUnavailable = errors.New("session: project model policy could not be read; resume refused")
	// ErrResumeFallbackNotSaved refuses a resume because the fallback model could
	// not be saved; resuming on a model that is not recorded would be a silent switch.
	ErrResumeFallbackNotSaved = errors.New("session: could not save the fallback model; resume refused")
)

// sessionNoticePoster is the optional chat-launcher surface that puts a system
// row on a session's timeline. It reports false when the session has no
// conversation yet, so the notice must travel with the start instead.
type sessionNoticePoster interface {
	PostSessionNotice(ctx context.Context, id domain.SessionID, summary string) (bool, error)
}

// enforceResumeModel applies the project's model denylist to the model a stopped
// session is about to resume on. A model denied after the session started is
// replaced by the project's default model, or the harness default when the
// project names none, provided that model is allowed; the replacement is
// persisted and announced on the chat timeline before anything starts, so even a
// start that then fails leaves the notice. It returns the config to launch with
// and a notice the caller must still carry on the start ("" when nothing changed
// or the notice is already posted). With no allowed fallback it refuses.
func (m *Manager) enforceResumeModel(ctx context.Context, rec domain.SessionRecord, cfg ports.AgentConfig) (ports.AgentConfig, string, error) {
	saved := strings.TrimSpace(cfg.Model)
	if saved == "" {
		return cfg, "", nil
	}
	// loadProject treats a missing row as an empty project, which would silently
	// drop the project's denylist. A session with a project must find it.
	var policy domain.ProjectConfig
	if rec.ProjectID != "" {
		project, found, err := m.store.GetProject(ctx, string(rec.ProjectID))
		if err != nil || !found {
			return cfg, "", fmt.Errorf("%w: project %s: %w", ErrResumeModelPolicyUnavailable, rec.ProjectID, cmp.Or(err, errors.New("not found")))
		}
		policy = project.Config
	}
	var denied *domain.ModelDeniedError
	if err := policy.CheckModel(saved); !errors.As(err, &denied) {
		return cfg, "", nil
	}
	// Project default first, then the harness default. An empty model is the
	// harness's own default; Codex names its default, which the policy can deny.
	fallback := strings.TrimSpace(effectiveAgentConfigFor(rec.Harness, rec.Kind, rec.Role, policy).Model)
	if fallback == "" || policy.CheckModel(fallback) != nil {
		var err error
		fallback, err = policy.ResolveModel(rec.Harness, "")
		if err == nil {
			err = policy.CheckModel(fallback)
		}
		if err != nil {
			return cfg, "", fmt.Errorf("%w: %q: %w", ErrResumeModelDenied, saved, denied)
		}
	}
	if err := m.PersistChatModel(ctx, rec.ID, fallback); err != nil {
		return cfg, "", fmt.Errorf("%w: %w", ErrResumeFallbackNotSaved, err)
	}
	cfg.Model = fallback
	to := fallback
	if to == "" {
		to = "the agent's default model"
	}
	m.logger.Warn("resume: saved model is denied by project policy; resuming on a fallback",
		"sessionID", rec.ID, "from", saved, "to", to)
	notice := fmt.Sprintf("Model %s is blocked by project policy; resumed on %s", saved, to)
	// A terminal has no timeline: the log line above is its whole notice.
	if domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
		return cfg, "", nil
	}
	if poster, ok := m.chat.(sessionNoticePoster); ok {
		if posted, err := poster.PostSessionNotice(ctx, rec.ID, notice); err != nil {
			m.logger.Warn("resume: could not post the model policy notice", "sessionID", rec.ID, "error", err)
		} else if posted {
			return cfg, "", nil
		}
	}
	return cfg, notice, nil
}
