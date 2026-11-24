// Package browser owns authorization and dispatch for session-scoped browser
// commands. HTTP controllers remain transport-only adapters.
package browser

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"tallpine.eu/tallpine/backend/internal/browserruntime"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

var actions = map[string]struct{}{
	"open": {}, "snapshot": {}, "act": {}, "click": {}, "dblclick": {}, "focus": {}, "fill": {}, "type": {}, "press": {},
	"hover": {}, "highlight": {}, "unhighlight": {}, "scrollintoview": {}, "drag": {}, "tabs": {}, "tab-new": {},
	"tab-select": {}, "tab-close": {}, "scroll": {}, "select": {}, "check": {},
	"uncheck": {}, "get": {}, "wait": {}, "screenshot": {}, "network-start": {},
	"network-status": {}, "network-list": {}, "network-stop": {}, "network-clear": {},
	"console": {}, "errors": {}, "frame": {}, "dialog": {},
	"devtools-open": {}, "devtools-close": {}, "viewport": {},
	"back": {}, "forward": {}, "reload": {}, "eval": {}, "upload": {},
	"handoff": {},
}

type sessionReader interface {
	Get(ctx context.Context, id domain.SessionID) (domain.Session, error)
}

type runtime interface {
	Status() browserruntime.Status
	ExecuteOn(ctx context.Context, sessionID domain.SessionID, backend, action string, args map[string]interface{}) (browserruntime.Result, error)
}

// projectReader reads the project config that picks a session's browser
// backend (project.browserBackend, B849).
type projectReader interface {
	GetProjectWithoutEnv(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
}

// Service validates worker ownership and lifecycle state before dispatching to
// the Electron runtime.
type Service struct {
	sessions  sessionReader
	runtime   runtime
	authority *Authority
	projects  projectReader
	// dataDir locates the session temp dirs upload may read from.
	dataDir string
	// notifier raises a handoff's needs-you notification (B858); nil skips it.
	notifier notifier
	log      *slog.Logger
	// handoffs holds the sessions with a handoff in flight (one each).
	handoffMu sync.Mutex
	handoffs  map[domain.SessionID]struct{}
}

// New creates a browser service. projects may be nil, which keeps every
// session on the in-app (electron) browser.
func New(sessions sessionReader, runtime runtime, authority *Authority, projects projectReader) *Service {
	return &Service{sessions: sessions, runtime: runtime, authority: authority, projects: projects}
}

// WithDataDir sets the daemon data directory, under which each session's
// temp dir ($TMPDIR) lives; without it upload reads only from the worktree.
func (s *Service) WithDataDir(dataDir string) *Service {
	s.dataDir = dataDir
	return s
}

// Status returns transport state after validating the session owner.
func (s *Service) Status(ctx context.Context, sessionID domain.SessionID, capability string) (browserruntime.Status, string, error) {
	session, err := s.authorize(ctx, sessionID, capability)
	if err != nil {
		return browserruntime.Status{}, "", err
	}
	backend, err := s.backend(ctx, session.ProjectID)
	return s.runtime.Status(), backend, err
}

// Execute validates ownership and dispatches one supported action.
func (s *Service) Execute(
	ctx context.Context,
	sessionID domain.SessionID,
	capability string,
	action string,
	args map[string]interface{},
) (browserruntime.Result, string, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	session, err := s.authorize(ctx, sessionID, capability)
	if err != nil {
		return browserruntime.Result{}, action, err
	}
	if _, ok := actions[action]; !ok {
		return browserruntime.Result{}, action, apierr.Invalid(
			"BROWSER_ACTION_UNSUPPORTED",
			"Unsupported browser action",
			nil,
		)
	}
	if action == "upload" {
		paths, err := resolveUploadPaths(args["paths"], uploadRoots(session, s.dataDir))
		if err != nil {
			return browserruntime.Result{}, action, err
		}
		next := make(map[string]interface{}, len(args))
		for key, value := range args {
			next[key] = value
		}
		next["paths"] = paths
		args = next
	}
	backend, err := s.backend(ctx, session.ProjectID)
	if err != nil {
		return browserruntime.Result{}, action, err
	}
	if action == handoffActionName {
		result, err := s.executeHandoff(ctx, session, backend, args)
		return result, action, err
	}
	result, err := s.runtime.ExecuteOn(ctx, sessionID, backend, action, args)
	return result, action, err
}

// backend is the browser the session's project drives, read on every command
// so a toggle takes effect on the next one (B849 a11). A failed read fails the
// command rather than silently falling back to the in-app browser.
func (s *Service) backend(ctx context.Context, project domain.ProjectID) (string, error) {
	if s.projects == nil || project == "" {
		return domain.BrowserBackendElectron, nil
	}
	rec, ok, err := s.projects.GetProjectWithoutEnv(ctx, string(project))
	if err != nil {
		return "", err
	}
	if !ok {
		return domain.BrowserBackendElectron, nil
	}
	return rec.Config.EffectiveBrowserBackend(), nil
}

func (s *Service) authorize(ctx context.Context, sessionID domain.SessionID, capability string) (domain.Session, error) {
	session, err := s.sessions.Get(ctx, sessionID)
	if err != nil {
		return domain.Session{}, err
	}
	if session.IsTerminated {
		return domain.Session{}, apierr.Conflict("SESSION_TERMINATED", "Session is terminated", nil)
	}
	if s.authority == nil || !s.authority.Valid(
		sessionID,
		strings.TrimSpace(capability),
		session.Metadata.BrowserCapabilityVerifier,
	) {
		return domain.Session{}, apierr.Forbidden("BROWSER_CAPABILITY_INVALID", "Browser capability is invalid")
	}
	return session, nil
}
