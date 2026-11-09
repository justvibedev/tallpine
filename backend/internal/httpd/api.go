package httpd

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"tallpine.eu/tallpine/backend/internal/service/settingsaudit"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"tallpine.eu/tallpine/backend/internal/attachmentstore"
	"tallpine.eu/tallpine/backend/internal/cdc"
	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/presence"
	"tallpine.eu/tallpine/backend/internal/service/accountadopt"
	prsvc "tallpine.eu/tallpine/backend/internal/service/pr"
	projectsvc "tallpine.eu/tallpine/backend/internal/service/project"
	reviewsvc "tallpine.eu/tallpine/backend/internal/service/review"
	"tallpine.eu/tallpine/backend/internal/sessiontoken"
)

// APIDeps bundles every service the API layer's controllers depend on.
type APIDeps struct {
	Awake         controllers.AwakeService
	Agents        controllers.AgentCatalog
	CodexAccounts controllers.CodexAccountService
	AgentAccounts controllers.AgentAccountService
	AccountAdopt  *accountadopt.Service
	Projects      projectsvc.Manager
	// DefaultBranch resolves the ref session worktrees start from (Settings >
	// Skills commits pending agent files there). Optional.
	DefaultBranch      controllers.DefaultBranchResolver
	GitHubAccountCheck controllers.GitHubAccountChecker
	Sessions           controllers.SessionService
	ArtifactVersions   controllers.ArtifactVersionService
	Automations        controllers.AutomationService
	DesktopWorkspaces  controllers.DesktopWorkspaceService
	Activity           controllers.ActivityRecorder
	UsageHooks         controllers.UsageHookRecorder
	UsageSummary       controllers.UsageSummaryService
	SessionMemory      controllers.SessionMemoryService
	LastMessages       controllers.LastAssistantMessageReader
	Models             controllers.ModelResolver
	Instructions       controllers.WorkerInstructionsReader
	BackgroundTasks    BackgroundTaskTracker
	UsageLimits        controllers.UsageLimitReader
	MessageSent        controllers.MessageSentFunc
	// OwnerMessages backs `tallpine send --relay-owner-message`; nil refuses it.
	OwnerMessages      controllers.OwnerMessageLookup
	SessionSteps       controllers.SessionStepsReader
	PRs                prsvc.ActionManager
	Reviews            reviewsvc.Manager
	Notifications      controllers.NotificationService
	Reports            controllers.ReportService
	NotificationStream controllers.NotificationStream
	// ApprovalNotifier records the notification for an orchestrator-answered prompt.
	ApprovalNotifier controllers.ApprovalNotifier
	// TerminalApprovals answers permission dialogs of terminal-mode sessions.
	TerminalApprovals controllers.TerminalApprovalService
	Push              controllers.PushRegistry
	Import            controllers.ImportService
	Directories       controllers.DirectoryBrowserService
	ShellTerminals    controllers.ShellTerminalService
	Cues              controllers.CueService
	Backlog           controllers.BacklogService
	// ReleaseInstall runs the orchestrator-only release install; nil serves 501.
	ReleaseInstall controllers.ReleaseInstaller
	// ReleaseBuild runs the daemon-side request-build; nil serves 501.
	ReleaseBuild   controllers.ReleaseBuildRequester
	BacklogSpawner controllers.BacklogSpawner
	BacklogRuns    controllers.BacklogRunService
	// TerminalUsage reports spend of terminal-mode sessions.
	TerminalUsage controllers.TerminalUsageService
	// BacklogPlans serves item plans (B205); nil serves 501.
	BacklogPlans controllers.BacklogPlanService
	Merges       controllers.MergesService
	Intents      controllers.IntentStore
	// Conversations is nil until a Chat driver is wired; the controller then
	// answers 501 rather than panicking, matching the other optional surfaces.
	Conversations controllers.ConversationService
	// Settings is the daemon-owned preference surface.
	Settings controllers.SettingsService
	// SettingsAudit records settings changes with their actor (B202); nil
	// records nothing.
	SettingsAudit *settingsaudit.Recorder
	// SettingsChanges reads the settings change log; nil serves 501.
	SettingsChanges controllers.SettingsChangesStore
	// SettingsValues reads effective setting values; nil serves 501.
	SettingsValues controllers.SettingsValues
	// SettingsWriter changes settings by key; nil serves 501.
	SettingsWriter controllers.SettingsWriter
	// AgentPolicy is the enterprise agent policy (permission cap).
	AgentPolicy controllers.AgentPolicyService
	// AgentPolicyDecisions reads the per-session decision records.
	AgentPolicyDecisions controllers.AgentPolicyDecisions
	// GitHubCredentials issues per-use GitHub credentials to helper-mode agents and runs the daemon's own git as the project's account.
	GitHubCredentials controllers.GitHubCredentialService
	// SessionCommit commits in a session's own worktree for sandboxed workers.
	SessionCommit controllers.SessionCommitService
	// SessionLand lands an approved session branch without a PR (orchestrators and the owner).
	SessionLand controllers.SessionLandService
	// ArtifactStorage reports artifact disk usage and applies confirmed retention.
	ArtifactStorage controllers.ArtifactStorageService
	// Cleanup owns the automatic cleanup policy, manual runs and their history.
	Cleanup controllers.CleanupService
	// Storage reports everything Tallpine and its agents keep on disk and removes leftovers.
	Storage controllers.StorageUsageService
	// Health summarises silent failures for `tallpine doctor` and the desktop badge.
	Health controllers.HealthService
	// Update backs "Update & restart" (`tallpine update`, Settings, mobile).
	Update    controllers.UpdateService
	DevImport controllers.DevImportService
	// AOImport migrates projects from an official Agent Orchestrator install.
	AOImport controllers.AOImportService
	CDC      cdc.Source
	Events   cdcSubscriber
	// StatusReadiness drives the live `status_readiness_changed` SSE frame.
	StatusReadiness readinessSubscriber
	Mobile          *controllers.MobileController
	// MobileFeatureEnabled is the mobile feature flag; see ControlDeps.
	MobileFeatureEnabled func() bool
	BrowserLive          *BrowserLiveHub
	Browser              controllers.BrowserService
	PreviewServer        controllers.ManagedPreviewServer
	SessionCapabilities  controllers.SessionCapabilityValidator
	// SessionTokens verifies scoped agent tokens (TALLPINE_SESSION_TOKEN); it must
	// be derived from the same install token the daemon enforces. Nil derives
	// it from ControlDeps.DaemonToken.
	SessionTokens *sessiontoken.Authority
	// AgentScopes resolves the session or review a scoped token names. Nil
	// refuses every scoped token.
	AgentScopes AgentScopeStore
	// PRScope resolves a PR's owning session for scoped agents' PR actions.
	PRScope                  controllers.PRScopeStore
	ShellPreviewCapabilities controllers.ShellPreviewCapabilityValidator
	SystemChecks             controllers.SystemChecker
	// HostID is this machine's stable, machine-bound identity, served by the
	// unauthenticated GET /api/v1/identity probe so a phone can confirm which
	// machine answered before presenting a credential.
	HostID string
	// Endpoints reports how this daemon can currently be reached, for the
	// phone's endpoint-refresh route.
	Endpoints controllers.EndpointSource
	Installer controllers.Installer
	AgentAuth controllers.AgentAuthService
	// GitHub is the local GitHub PAT + repos surface.
	GitHub controllers.GitHubPATService
	// LinkPreview unfurls external URLs for the renderer's hover cards; nil
	// leaves the route answering 501.
	LinkPreview controllers.LinkPreviewService

	// Presence tracks which mobile devices are currently running the app.
	// Nil disables presence tracking (the roster then reports every device offline).
	Presence *presence.Tracker

	// DeviceRoster and DeviceLive back the desktop-only mobile device roster.
	DeviceRoster controllers.DeviceRoster
	DeviceLive   controllers.LiveSet
}

// normalizeAPIDeps closes the Presence/DeviceLive duplication trap structurally.
// Liveness enters APIDeps twice — Presence drives the heartbeat middleware that
// touches it, DeviceLive is what the device roster reads — and nothing enforces
// they stay the same tracker. If a future edit set Presence but left DeviceLive
// nil (or re-split them), the roster would silently and permanently report
// every device offline: no error, no log, no test failure short of a live
// phone. Defaulting DeviceLive to Presence here, at the one place APIDeps is
// consumed to build the API, makes that trap unreachable rather than merely
// currently avoided by careful call-site wiring.
//
// A nil Presence on its own is not an error: the roster must keep listing and
// managing devices with every device simply reporting offline (see
// MobileDevicesController.List's own nil-Presence fallback) — that decision
// stands. What IS a real mis-wiring is a live DeviceRoster with no liveness
// source at all after the fallback above; that gets exactly one startup
// warning, because a silent-forever-offline roster is precisely what a
// startup log is for.
func normalizeAPIDeps(deps APIDeps, log *slog.Logger) APIDeps {
	if deps.DeviceLive == nil && deps.Presence != nil {
		deps.DeviceLive = deps.Presence
	}
	if deps.DeviceRoster != nil && deps.DeviceLive == nil {
		log.Warn("mobile device roster has no liveness tracker wired; every device will report offline")
	}
	return deps
}

// API owns one controller per resource and is the single Register call the
// router invokes to mount the /api/v1 surface.
type API struct {
	cfg            config.Config
	deps           APIDeps
	agents         *controllers.AgentsController
	codexAccounts  *controllers.CodexAccountsController
	agentAccounts  *controllers.AgentAccountsController
	accountAdopt   *controllers.AccountAdoptController
	projects       *controllers.ProjectsController
	sessions       *controllers.SessionsController
	automations    *controllers.AutomationsController
	desktop        *controllers.DesktopWorkspaceController
	usage          *controllers.UsageController
	prs            *controllers.PRsController
	reviews        *controllers.ReviewsController
	notifications  *controllers.NotificationsController
	reports        *controllers.ReportsController
	push           *controllers.PushController
	imports        *controllers.ImportController
	fs             *controllers.FSController
	shellTerms     *controllers.ShellTerminalsController
	cues           *controllers.CuesController
	backlog        *controllers.BacklogController
	releaseInstall *controllers.ReleaseInstallController
	backlogRuns    *controllers.BacklogRunController
	backlogPlans   *controllers.BacklogPlanController
	merges         *controllers.MergesController
	intents        *controllers.IntentsController
	conversations  *controllers.ConversationsController
	settings       *controllers.SettingsController
	agentPolicy    *controllers.AgentPolicyController
	projectSkills  *controllers.ProjectSkillsController
	projectGitHub  *controllers.ProjectGitHubAccountController
	githubCreds    *controllers.GitHubCredentialController
	sessionCommit  *controllers.SessionCommitController
	sessionLand    *controllers.SessionLandController
	artifactStore  *controllers.ArtifactStorageController
	cleanup        *controllers.CleanupController
	settingsLog    *controllers.SettingsChangesController
	settingsReg    *controllers.SettingsRegistryController
	storage        *controllers.StorageUsageController
	health         *controllers.HealthController
	update         *controllers.UpdateController
	dev            *controllers.DevController
	tallpineImport *controllers.AOImportController
	browser        *controllers.BrowserController
	system         *controllers.SystemController
	identity       *controllers.IdentityController
	endpoints      *controllers.EndpointsController
	systemInstall  *controllers.SystemInstallController
	agentAuth      *controllers.AgentAuthController
	linkPreview    *controllers.LinkPreviewController
	github         *controllers.GitHubController
	events         *EventsController
}

// NewAPI constructs the API surface from its dependencies. cfg carries the
// per-request timeout so the REST group can apply it without re-reading the
// environment.
func NewAPI(cfg config.Config, deps APIDeps) *API {
	return newAPIWithLogger(cfg, deps, loggerOrDefault(nil))
}

// newAPIWithLogger carries the daemon logger to controllers that emit service
// errors, so their logs use the same configured handler as the rest of HTTP.
func newAPIWithLogger(cfg config.Config, deps APIDeps, log *slog.Logger) *API {
	return &API{
		cfg:  cfg,
		deps: deps,
		agents: &controllers.AgentsController{
			Catalog: deps.Agents,
		},
		codexAccounts: &controllers.CodexAccountsController{Svc: deps.CodexAccounts},
		agentAccounts: newAgentAccountsController(deps),
		accountAdopt:  newAccountAdoptController(deps),
		projects: &controllers.ProjectsController{
			Mgr:      deps.Projects,
			Creators: projectCreators(deps.AgentScopes),
			Policy:   deps.AgentPolicy,
		},
		sessions:       newSessionsController(cfg, deps),
		automations:    &controllers.AutomationsController{Svc: deps.Automations},
		desktop:        &controllers.DesktopWorkspaceController{Svc: deps.DesktopWorkspaces},
		usage:          &controllers.UsageController{Svc: deps.UsageSummary, Log: loggerOrDefault(log), Memory: deps.SessionMemory, Steps: deps.SessionSteps, Pressure: memoryPressure(deps.SessionMemory)},
		prs:            &controllers.PRsController{Svc: deps.PRs, Scope: deps.PRScope},
		reviews:        &controllers.ReviewsController{Svc: deps.Reviews},
		notifications:  &controllers.NotificationsController{Svc: deps.Notifications, Stream: deps.NotificationStream},
		reports:        &controllers.ReportsController{Svc: deps.Reports},
		push:           &controllers.PushController{Registry: deps.Push},
		imports:        &controllers.ImportController{Svc: deps.Import},
		fs:             &controllers.FSController{Svc: deps.Directories},
		shellTerms:     &controllers.ShellTerminalsController{Svc: deps.ShellTerminals},
		cues:           &controllers.CuesController{Svc: deps.Cues},
		backlog:        &controllers.BacklogController{Svc: deps.Backlog},
		releaseInstall: &controllers.ReleaseInstallController{Svc: deps.ReleaseInstall, Build: deps.ReleaseBuild},
		backlogRuns:    &controllers.BacklogRunController{Svc: deps.BacklogRuns},
		backlogPlans:   &controllers.BacklogPlanController{Svc: deps.BacklogPlans, UsageLimits: deps.UsageLimits},
		merges:         &controllers.MergesController{Svc: deps.Merges},
		intents:        &controllers.IntentsController{Store: deps.Intents},
		conversations: &controllers.ConversationsController{Svc: deps.Conversations, SessionKind: func(ctx context.Context, id domain.SessionID) (domain.SessionKind, error) {
			if deps.Sessions == nil {
				return "", fmt.Errorf("sessions unavailable")
			}
			sess, err := deps.Sessions.Get(ctx, id)
			return sess.Kind, err
		}, Renders: attachmentstore.New(cfg.DataDir), MessageSent: deps.MessageSent, Notifier: deps.ApprovalNotifier, Terminal: deps.TerminalApprovals, TerminalUsage: deps.TerminalUsage, BackgroundTasks: deps.BackgroundTasks},
		settings:       newSettingsController(deps),
		githubCreds:    &controllers.GitHubCredentialController{Svc: deps.GitHubCredentials},
		sessionCommit:  &controllers.SessionCommitController{Svc: deps.SessionCommit},
		sessionLand:    &controllers.SessionLandController{Svc: deps.SessionLand},
		projectGitHub:  &controllers.ProjectGitHubAccountController{Checker: deps.GitHubAccountCheck},
		projectSkills:  &controllers.ProjectSkillsController{Projects: deps.Projects, DefaultBranch: deps.DefaultBranch, GitCredentials: deps.GitHubCredentials},
		agentPolicy:    &controllers.AgentPolicyController{Audit: deps.SettingsAudit, Svc: deps.AgentPolicy, Projects: deps.Projects, Decisions: deps.AgentPolicyDecisions},
		artifactStore:  &controllers.ArtifactStorageController{Svc: deps.ArtifactStorage},
		cleanup:        &controllers.CleanupController{Audit: deps.SettingsAudit, Svc: deps.Cleanup},
		settingsLog:    &controllers.SettingsChangesController{Store: deps.SettingsChanges},
		settingsReg:    &controllers.SettingsRegistryController{Values: deps.SettingsValues, Writer: deps.SettingsWriter},
		storage:        &controllers.StorageUsageController{Svc: deps.Storage},
		health:         &controllers.HealthController{Svc: deps.Health},
		update:         &controllers.UpdateController{Svc: deps.Update},
		dev:            &controllers.DevController{Import: deps.DevImport},
		tallpineImport: &controllers.AOImportController{Svc: deps.AOImport},
		browser:        &controllers.BrowserController{Svc: deps.Browser},
		system:         &controllers.SystemController{Checks: deps.SystemChecks},
		identity:       &controllers.IdentityController{HostID: deps.HostID},
		endpoints:      &controllers.EndpointsController{Source: deps.Endpoints},
		systemInstall:  &controllers.SystemInstallController{Installer: deps.Installer},
		agentAuth:      &controllers.AgentAuthController{Svc: deps.AgentAuth},
		linkPreview:    &controllers.LinkPreviewController{Svc: deps.LinkPreview},
		github:         &controllers.GitHubController{Svc: deps.GitHub},
		events:         &EventsController{Source: deps.CDC, Live: deps.Events, Readiness: deps.StatusReadiness},
	}
}

const attachmentUploadHeader = "X-AO-Attachment-Upload"

// setupRoute reports a request that may run the project's Setup steps.
func setupRoute(req *http.Request) bool {
	if req.Method != http.MethodPost {
		return false
	}
	switch chi.RouteContext(req.Context()).RoutePattern() {
	case "/api/v1/sessions", "/api/v1/orchestrators", "/api/v1/sessions/{sessionId}/restore":
		return true
	}
	return false
}

// serveWithSetupBudget serves a Setup route under budget, like
// middleware.Timeout, except that the operation may extend the deadline
// (ports.ExtendSpawnRequestBudget) once it knows its Setup steps.
func serveWithSetupBudget(next http.Handler, w http.ResponseWriter, req *http.Request, budget time.Duration) {
	ctx, timedOut, release := ports.NewSpawnRequestContext(req.Context(), budget)
	defer release()
	next.ServeHTTP(w, req.WithContext(ctx))
	if timedOut() {
		w.WriteHeader(http.StatusGatewayTimeout)
	}
}

// Register mounts the bounded /api/v1 REST surface. Long-lived surfaces such
// as muxed terminal streams stay outside this timeout group.
func (a *API) Register(root chi.Router) {
	timeout := a.cfg.RequestTimeout
	if timeout <= 0 {
		timeout = config.DefaultRequestTimeout
	}
	root.Route("/api/v1", func(r chi.Router) {
		// Serve the OpenAPI document from the same origin as the routes it describes.
		r.Get("/openapi.yaml", apispec.ServeYAML)

		r.Group(func(r chi.Router) {
			// Large base64 bodies can spend longer than the ordinary REST budget
			// uploading over a phone connection. Only attachment-bearing requests
			// opt in; ordinary calls to the same routes keep the configured timeout.
			r.Use(func(next http.Handler) http.Handler {
				ordinary := middleware.Timeout(timeout)(next)
				upload := middleware.Timeout(max(timeout, 10*time.Minute))(next)
				clone := middleware.Timeout(max(timeout, 30*time.Minute))(next)
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					// A clone downloads the whole repository inside the request,
					// which takes minutes for a large repository.
					// A manual cleanup run measures and removes whole worktrees and
					// artifact trees inside the request.
					if pattern := chi.RouteContext(req.Context()).RoutePattern(); pattern == "/api/v1/projects/clone" ||
						pattern == "/api/v1/projects/clone/prepare" ||
						(pattern == "/api/v1/cleanup/runs" && req.Method == http.MethodPost) ||
						pattern == "/api/v1/storage/cleanup" || pattern == "/api/v1/storage/delete" {
						clone.ServeHTTP(w, req)
						return
					}
					// A spawn, an orchestrator start or a restore runs the project's
					// Setup inside the request, and a Setup step may take far longer
					// than one REST request: the operation extends this budget by its
					// Setup steps' limits once it knows its project.
					if setupRoute(req) {
						budget := timeout
						if a.cfg.SpawnTimeout > 0 {
							budget = a.cfg.SpawnTimeout
						}
						if attachmentUploadRoute(req) {
							budget = max(budget, 10*time.Minute)
						}
						serveWithSetupBudget(next, w, req, budget)
						return
					}
					if attachmentUploadRoute(req) {
						upload.ServeHTTP(w, req)
						return
					}
					// A managed preview carries the dev server's HMR socket and event
					// streams, and the agent browser its stream socket: both stay open
					// as long as the page does.
					if pattern := chi.RouteContext(req.Context()).RoutePattern(); pattern == "/api/v1/sessions/{sessionId}/preview/app/*" ||
						pattern == "/api/v1/sessions/{sessionId}/agent-browser/*" {
						next.ServeHTTP(w, req)
						return
					}
					ordinary.ServeHTTP(w, req)
				})
			})
			r.Use(presenceMiddleware(a.deps.Presence))
			a.agents.Register(r)
			a.codexAccounts.Register(r)
			a.agentAccounts.Register(r)
			a.accountAdopt.Register(r)
			a.projects.Register(r)
			a.sessions.Register(r)
			a.automations.Register(r)
			a.desktop.Register(r)
			a.usage.Register(r)
			a.prs.Register(r)
			a.reviews.Register(r)
			a.notifications.Register(r)
			a.reports.Register(r)
			a.push.Register(r)
			a.imports.Register(r)
			a.fs.Register(r)
			a.shellTerms.Register(r)
			a.cues.Register(r)
			a.backlog.Register(r)
			a.releaseInstall.Register(r)
			a.backlogRuns.Register(r)
			a.backlogPlans.Register(r)
			a.merges.Register(r)
			a.intents.Register(r)
			a.conversations.Register(r)
			a.settings.Register(r)
			a.agentPolicy.Register(r)
			a.projectSkills.Register(r)
			a.projectGitHub.Register(r)
			a.githubCreds.Register(r)
			a.sessionCommit.Register(r)
			a.sessionLand.Register(r)
			a.artifactStore.Register(r)
			a.cleanup.Register(r)
			a.settingsLog.Register(r)
			a.settingsReg.Register(r)
			a.storage.Register(r)
			a.health.Register(r)
			a.update.Register(r)
			a.dev.Register(r)
			a.tallpineImport.Register(r)
			a.browser.Register(r)
			a.system.Register(r)
			a.identity.Register(r)
			a.endpoints.Register(r)
			a.systemInstall.Register(r)
			a.agentAuth.Register(r)
			a.linkPreview.Register(r)
			a.github.Register(r)
			// Sibling REST controllers plug in here.
		})
		// Long-lived streams intentionally bypass the REST timeout middleware.
		a.notifications.RegisterStream(r)
		a.codexAccounts.RegisterStreams(r)
		a.sessions.RegisterStreams(r)
		a.events.Register(r)
	})
}

func attachmentUploadRoute(req *http.Request) bool {
	if req.Method != http.MethodPost {
		return false
	}
	route := chi.RouteContext(req.Context()).RoutePattern()
	// This route only accepts attachments, including from older clients.
	if route == "/api/v1/sessions/{sessionId}/attachments" {
		return true
	}
	if req.Header.Get(attachmentUploadHeader) != "1" {
		return false
	}
	switch route {
	case "/api/v1/sessions",
		"/api/v1/orchestrators/delegate",
		"/api/v1/sessions/{sessionId}/send",
		"/api/v1/sessions/{sessionId}/conversation/messages",
		"/api/v1/sessions/{sessionId}/provider-switch",
		"/api/v1/sessions/{sessionId}/conversation/steer",
		"/api/v1/sessions/{sessionId}/conversation/steer-or-send",
		"/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/queue/edit",
		"/api/v1/reviews/{reviewId}/conversation/messages":
		return true
	default:
		return false
	}
}

// memoryPressure is the memory service's cheap pressure read when it offers
// one; nil leaves the route at 501.
func memoryPressure(svc controllers.SessionMemoryService) controllers.MemoryPressureReader {
	if p, ok := svc.(controllers.MemoryPressureReader); ok {
		return p
	}
	return nil
}

// newAgentAccountsController moves sessions through the session service when
// it can, so a move maps errors like every other session command.
func newAgentAccountsController(deps APIDeps) *controllers.AgentAccountsController {
	c := &controllers.AgentAccountsController{Svc: deps.AgentAccounts}
	if mover, ok := deps.Sessions.(controllers.AgentAccountSessionMover); ok {
		c.Sessions = mover
	}
	return c
}

// newAccountAdoptController answers 501 when the daemon built no adoption
// service (a session manager that cannot move sessions between accounts).
func newAccountAdoptController(deps APIDeps) *controllers.AccountAdoptController {
	c := &controllers.AccountAdoptController{}
	if deps.AccountAdopt != nil {
		c.Svc = deps.AccountAdopt
	}
	return c
}

// agentBrowserURL is the host's agent browser, or nil when it has none.
// config.Load already accepted only a loopback http URL.
func agentBrowserURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if raw == "" || err != nil {
		return nil
	}
	return u
}
