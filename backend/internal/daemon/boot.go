package daemon

import (
	"context"
	"log/slog"
	"time"

	codexagent "tallpine.eu/tallpine/backend/internal/adapters/agent/codex"
	chatdriverregistry "tallpine.eu/tallpine/backend/internal/adapters/chatdriver/registry"
	"tallpine.eu/tallpine/backend/internal/adapters/reviewer"
	"tallpine.eu/tallpine/backend/internal/adapters/runtime/runtimeselect"
	multi "tallpine.eu/tallpine/backend/internal/adapters/scm/multi"
	"tallpine.eu/tallpine/backend/internal/adapters/systemexec"
	agentpolicy "tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/autoreview"
	"tallpine.eu/tallpine/backend/internal/bgtasks"
	"tallpine.eu/tallpine/backend/internal/browserruntime"
	"tallpine.eu/tallpine/backend/internal/browserstream"
	"tallpine.eu/tallpine/backend/internal/codexops"
	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/datadirlock"
	"tallpine.eu/tallpine/backend/internal/httpd"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	managedcfg "tallpine.eu/tallpine/backend/internal/managedcfg"
	"tallpine.eu/tallpine/backend/internal/mobilebridge"
	bridgesvc "tallpine.eu/tallpine/backend/internal/mobilebridge/service"
	"tallpine.eu/tallpine/backend/internal/notify"
	usagepipeline "tallpine.eu/tallpine/backend/internal/observe/usage"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/presence"
	"tallpine.eu/tallpine/backend/internal/previewserver"
	"tallpine.eu/tallpine/backend/internal/restartresume"
	reviewcore "tallpine.eu/tallpine/backend/internal/review"
	accountadopt "tallpine.eu/tallpine/backend/internal/service/accountadopt"
	agentsvc "tallpine.eu/tallpine/backend/internal/service/agent"
	"tallpine.eu/tallpine/backend/internal/service/agentaccounts"
	"tallpine.eu/tallpine/backend/internal/service/agentauth"
	"tallpine.eu/tallpine/backend/internal/service/artifactstorage"
	automation "tallpine.eu/tallpine/backend/internal/service/automation"
	backlogsvc "tallpine.eu/tallpine/backend/internal/service/backlog"
	browsersvc "tallpine.eu/tallpine/backend/internal/service/browser"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
	cleanup "tallpine.eu/tallpine/backend/internal/service/cleanup"
	notificationsvc "tallpine.eu/tallpine/backend/internal/service/notification"
	prsvc "tallpine.eu/tallpine/backend/internal/service/pr"
	projectsvc "tallpine.eu/tallpine/backend/internal/service/project"
	reportsvc "tallpine.eu/tallpine/backend/internal/service/report"
	reviewsvc "tallpine.eu/tallpine/backend/internal/service/review"
	session "tallpine.eu/tallpine/backend/internal/service/session"
	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
	"tallpine.eu/tallpine/backend/internal/service/settingsaudit"
	"tallpine.eu/tallpine/backend/internal/service/settingsview"
	"tallpine.eu/tallpine/backend/internal/service/settingswrite"
	shellterm "tallpine.eu/tallpine/backend/internal/service/shellterm"
	"tallpine.eu/tallpine/backend/internal/service/systemcheck"
	"tallpine.eu/tallpine/backend/internal/service/systeminstall"
	"tallpine.eu/tallpine/backend/internal/service/turnnotify"
	usagesvc "tallpine.eu/tallpine/backend/internal/service/usage"
	"tallpine.eu/tallpine/backend/internal/sessiontoken"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/terminal"
)

// boot is the daemon's startup state. Run builds the services in stages (the
// wire* methods, in dependency order); each stage reads what earlier stages
// stored here and stores what later ones need. Teardown that used to be Run's
// deferred calls is registered with cleanup and runs in reverse order.
type boot struct {
	cfg                       config.Config
	log                       *slog.Logger
	browserAuthority          *browsersvc.Authority
	browserBroker             *browserruntime.Broker
	browserStreamBroker       *browserstream.Broker
	sessionTokens             *sessiontoken.Authority
	store                     *sqlite.Store
	hostLosses                *hostLossLedger
	managedLocks              *managedcfg.Locks
	ghAccounts                *githubAccounts
	ctx                       context.Context
	stop                      context.CancelFunc
	cdcPipe                   *cdcPipeline
	runtimeAdapter            runtimeselect.Runtime
	managedPreview            *previewserver.Manager
	termMgr                   *terminal.Manager
	lifecycleMessenger        *modeAwareMessenger
	notificationHub           *notify.Hub
	notifier                  *notificationsvc.Manager
	notificationWriter        *notify.Manager
	agents                    ports.AgentResolver
	messenger                 ports.AgentMessenger
	lcStack                   *lifecycleStack
	dataDirLock               *datadirlock.Lock // nil when the file system cannot lock
	shutdownBackstop          *time.Timer
	agentSvc                  *agentsvc.Service
	agentAccountSvc           *agentaccounts.Service
	chatDrivers               *chatdriverregistry.Registry
	settingsSvc               *settingssvc.Service
	sessMgr                   sessionLifecycle
	turnNotifier              *turnnotify.Observer
	workerJobs                *turnnotify.ProcessJobs
	bgTasks                   *bgtasks.Tracker
	chatSvc                   *chatsvc.Service
	codexModelPlugin          *codexagent.Plugin
	tracker                   ports.Tracker
	codexOperationGate        *codexops.Gate
	persistentHostsReconciled chan struct{}
	reviewerChatsRecovered    chan struct{}
	reviewers                 *reviewer.Resolver
	reviewerAccounts          *reviewcore.AccountEnvSlot
	agentPolicySvc            *agentpolicy.Engine
	sessionSvc                *session.Service
	reviewSvc                 reviewsvc.Manager
	memoryReader              *usagesvc.MemoryReader
	accountAdoptSvc           *accountadopt.Service
	artifactStorage           *artifactstorage.Service
	cleanupSvc                *cleanup.Service
	projectSvc                *projectsvc.Service
	settingsAudit             *settingsaudit.Recorder
	settingsValues            *settingsview.Service
	settingsWriter            *settingswrite.Writer
	terminalApprovals         controllers.TerminalApprovalService
	terminalUsage             controllers.TerminalUsageService
	reportSessions            reportSemanticSession
	reportCoordinator         *reportsvc.Coordinator
	autoReview                *autoreview.Coordinator
	backlogSvc                *backlogsvc.Service
	planSvc                   *backlogsvc.PlanService
	reportSvc                 *reportsvc.Service
	turnFallbackDone          <-chan struct{}
	reviewNotices             reviewFailureSweeper
	hostCommands              systemexec.Adapter
	systemChecks              *systemcheck.Service
	systemInstall             *systeminstall.Service
	bs                        *bridgesvc.BridgeService
	mc                        *controllers.MobileController
	browserService            *browsersvc.Service
	browserLive               *httpd.BrowserLiveHub
	shellTermSvc              *shellterm.Service
	agentAuthSvc              *agentauth.Service
	usageCollector            *usagesvc.Collector
	usagePipeline             *usagepipeline.Pipeline
	usagePricing              *usagePricingRuntime
	prActions                 prsvc.ActionManager
	prReader                  *multi.Provider
	resumeTracker             *restartresume.Tracker
	automationSvc             *automation.Service
	pushRegistry              controllers.PushRegistry
	pushDevices               *mobilebridge.DeviceRegistry
	deviceRoster              controllers.DeviceRoster
	deviceRev                 *deviceRevoker
	presenceTracker           *presence.Tracker
	hostIdentity              mobilebridge.Identity
	awakeSvc                  controllers.AwakeService
	srv                       *httpd.Server
	previewDone               <-chan struct{}
	usageDone                 <-chan struct{}
	lan                       *httpd.LANManager

	cleanups []func()
}

// cleanup registers f to run when Run returns, after the cleanups registered
// later (the order `defer` gave).
func (b *boot) cleanup(f func()) { b.cleanups = append(b.cleanups, f) }

// runCleanups runs the registered cleanups, last first.
func (b *boot) runCleanups() {
	for i := len(b.cleanups) - 1; i >= 0; i-- {
		b.cleanups[i]()
	}
}

// bootStages lists the startup stages in the order Run executes them.
func (b *boot) bootStages() []func() error {
	return []func() error{b.wireStorage, b.wireChat, b.wireAgents, b.wireSessions, b.wireReview, b.wireMobile, b.wireHTTP, b.serve}
}
