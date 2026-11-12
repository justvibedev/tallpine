package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/persistenthost"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/accountpool"
	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/service/accountfailover"
	"tallpine.eu/tallpine/backend/internal/service/artifactstorage"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
	projectsvc "tallpine.eu/tallpine/backend/internal/service/project"
	"tallpine.eu/tallpine/backend/internal/service/settingsaudit"
	"tallpine.eu/tallpine/backend/internal/service/settingsview"
	"tallpine.eu/tallpine/backend/internal/service/settingswrite"
	usagesvc "tallpine.eu/tallpine/backend/internal/service/usage"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
)

// wireSessions: the session service, its wiring, cleanup and project/settings services.
func (b *boot) wireSessions() error {
	var err error
	// The agent policy engine exists before anything that launches an agent, so
	// every launch path decides under the same rules.
	b.agentPolicySvc, err = newDaemonAgentPolicyEngine(b.store)
	if err != nil {
		return err
	}
	wireProjectEnvEncryption(b.ctx, b.store, b.agentPolicySvc, b.cfg.DataDir, b.log)
	var wiredSessMgr sessionLifecycle
	b.sessionSvc, b.reviewSvc, wiredSessMgr, err = startSession(b.ctx, b.cfg, b.runtimeAdapter, b.store, b.lcStack.LCM, b.lcStack.ArtifactVersions, b.messenger, b.notificationWriter, b.agents, b.agentSvc, b.managedPreview, b.browserBroker, b.browserAuthority, b.sessionTokens, chatLauncher{svc: b.chatSvc, policy: b.agentPolicySvc, persistentHostReconcileDone: b.persistentHostsReconciled}, b.reviewerChatsRecovered, b.settingsSvc, b.tracker, b.codexOperationGate, b.reviewers, b.reviewerAccounts, b.ghAccounts, b.log)
	if err != nil {
		b.stop()
		b.lcStack.Stop()
		if cdcErr := b.cdcPipe.Stop(); cdcErr != nil {
			b.log.Error("cdc pipeline shutdown", "err", cdcErr)
		}
		return fmt.Errorf("wire session service: %w", err)
	}
	b.sessionSvc.SetChatProviderPreserver(b.chatSvc.PreservesProviderOnRestart)
	if m, ok := wiredSessMgr.(interface{ SetPlannerEnabled(bool) }); ok {
		m.SetPlannerEnabled(b.cfg.Planner)
	}
	b.memoryReader = usagesvc.NewMemoryReader(usagesvc.MemoryReaderDeps{
		Store: b.store, Runtime: b.runtimeAdapter, Reviewers: b.store, CacheTTL: 2 * time.Second,
		ChatHostPID: func(id domain.SessionID) (int, bool) {
			return persistenthost.HostPID(b.cfg.DataDir, string(id))
		},
		// The desktop shell spawns an app-owned daemon, so its parent is
		// the Electron main process and that tree is the rest of Tallpine. Once
		// Electron dies the daemon is reparented to init, whose tree is the
		// whole host, so PID 1 is never a root.
		AppRootPIDs: func() []int {
			roots := []int{os.Getpid()}
			if p := os.Getppid(); p > 1 && brand.Getenv("OWNER") == "app" {
				roots = append(roots, p)
			}
			// The tmux server behind Tallpine's sessions detaches and reparents to
			// init, so it is a descendant of neither of the above.
			if pid, ok := b.runtimeAdapter.ServerPID(context.Background()); ok {
				roots = append(roots, pid)
			}
			return roots
		},
	})
	b.sessMgr = wiredSessMgr
	if switcher, ok := wiredSessMgr.(chatsvc.TerminalProviderSwitcher); ok {
		b.chatSvc.SetTerminalProviderSwitcher(switcher)
	}
	b.wireSessionManagerHooks()
	b.chatSvc.SetModelCatalog(b.agentSvc)

	// Automatic cleanup (Settings, Cleanup; `tallpine cleanup`): an hourly light pass
	// and a daily full pass over what ended sessions left behind. Deferred
	// after store.Close, so no pass outlives the store.
	b.artifactStorage = artifactstorage.New(b.cfg.DataDir, b.store, b.lcStack.ArtifactVersions, b.cfg.ArtifactSizeWarnBytes)
	b.artifactStorage.SetProtector(b.store)
	b.cleanupSvc = newCleanup(b.cfg.DataDir, os.TempDir(), b.store, b.sessMgr, b.lcStack.ArtifactVersions, b.artifactStorage, b.agentPolicySvc, b.notificationWriter, b.log)
	b.cleanupSvc.SetManaged(b.managedLocks) // before Start: the scheduler reads it
	cleanupDone := b.cleanupSvc.Start(b.ctx)
	b.cleanup(func() {
		b.stop()
		<-cleanupDone
	})
	b.wireLocalCIAlerts()

	// servers isn't clobbered. See preview_wiring.go (issue #4500).
	wireManagedPreviewExit(b.managedPreview, b.sessionSvc, b.log)
	b.sessMgr.SetTerminalInputGate(b.termMgr)
	b.lifecycleMessenger.Bind(sessionLifecycleMessenger{b.sessMgr})
	b.lcStack.LCM.SetCompletionTerminator(b.sessMgr)
	b.lcStack.LCM.SetMergeNotifier(newMergeNotifier(b.store, b.sessionSvc))
	b.lcStack.LCM.SetAutoCloseHooks(autoCloseHooks{store: b.store, jobs: b.workerJobs, turns: b.turnNotifier, branchWork: b.sessionSvc.WorkerBranchWork, orchestrator: orchestratorPicker(b.store), send: func(ctx context.Context, to domain.SessionID, msg string) error {
		return b.sessionSvc.Send(ctx, to, msg, nil)
	}, log: b.log})
	b.lcStack.LCM.SetSessionInputLease(b.sessMgr)
	b.lcStack.LCM.SetSessionOperationGate(b.sessMgr)
	b.termMgr.SetSessionInputLease(b.sessMgr)
	b.projectSvc = projectsvc.NewWithDeps(projectsvc.Deps{Policy: b.agentPolicySvc, Store: b.store, Sessions: b.sessionSvc, GitHubAccounts: b.ghAccounts, DefaultHarness: domain.AgentHarness(b.cfg.Agent), ProtectedDirs: []string{b.cfg.DataDir}, Logger: b.log})
	b.projectSvc.SetManaged(b.managedLocks)
	// A changed orchestrator compaction window reaches running orchestrators (B398).
	b.projectSvc.SetConfigChangedHook(b.chatSvc.ApplyProjectCompaction)
	b.settingsAudit = settingsaudit.New(b.store, b.log)
	b.settingsValues = &settingsview.Service{Managed: b.managedLocks, AppSettings: b.settingsSvc, CleanupPolicy: b.cleanupSvc, AgentPolicy: b.agentPolicySvc, Projects: b.projectSvc, LookupEnv: os.LookupEnv}
	b.settingsWriter = &settingswrite.Writer{View: b.settingsValues, Projects: b.projectSvc, AppSettings: b.settingsSvc, CleanupPolicy: b.cleanupSvc, AgentPolicy: b.agentPolicySvc, Audit: b.settingsAudit}
	b.projectSvc.SetSettingsAudit(b.settingsAudit)
	b.terminalApprovals, _ = b.sessMgr.(controllers.TerminalApprovalService)
	b.terminalUsage, _ = b.sessMgr.(controllers.TerminalUsageService)
	var ok bool
	b.reportSessions, ok = b.sessMgr.(reportSemanticSession)
	if !ok {
		return errors.New("wire report delivery: session manager lacks semantic send support")
	}
	return nil
}

// wireSessionManagerHooks attaches the optional capabilities of the session manager.
func (b *boot) wireSessionManagerHooks() {
	// The account pool settings are read live from the agent policy, so a
	// change in Settings or a managed profile applies to the next spawn or limit.
	b.agentAccountSvc.SetPlacement(func() domain.AccountPlacement {
		return accountpool.Of(b.agentPolicySvc.DecideStatic(b.ctx)).Placement()
	})
	b.chatSvc.SetUsageLimitAccounts(b.agentAccountSvc)
	// Worker and orchestrator turns that fail on a dropped network retry on their own (B682).
	retryPolicy := chatsvc.DefaultTransientRetryPolicy()
	b.chatSvc.SetTransientRetryPolicy(&retryPolicy)
	if starter, ok := b.sessMgr.(interface {
		StartAccountTransition(context.Context, domain.SessionID, string, domain.SessionInterfaceTransitionPolicy) (domain.SessionInterfaceTransition, error)
	}); ok {
		b.chatSvc.SetUsageLimitFailover(accountfailover.New(accountfailover.Config{
			Pool: b.agentAccountSvc.Pool(), Sessions: b.agentAccountSvc, Mover: starter, Transitions: b.store, Log: b.log,
			Enabled: func() bool { return accountpool.Of(b.agentPolicySvc.DecideStatic(b.ctx)).Failover() },
		}))
	}
	b.reviewerAccounts.Set(b.agentAccountSvc)
	if wired, ok := b.sessMgr.(interface {
		SetAgentAccounts(ports.AgentAccountLaunchEnv)
	}); ok {
		wired.SetAgentAccounts(b.agentAccountSvc)
	}
	if wired, ok := b.sessMgr.(interface {
		SetGitHubAccounts(sessionmanager.GitHubAccountTokens)
	}); ok {
		wired.SetGitHubAccounts(b.ghAccounts)
	}
	if wired, ok := b.sessMgr.(interface {
		SetProjectGitCredentials(ports.ProjectGitCredentials)
	}); ok {
		wired.SetProjectGitCredentials(b.gitCredentials())
	}
	if stopper, ok := b.sessMgr.(interface {
		ExitAgentsOnAccount(context.Context, string) error
	}); ok {
		b.agentAccountSvc.SetAgentStopper(stopper.ExitAgentsOnAccount)
	}
	wireAgentPolicy(b.agentPolicySvc, b.sessMgr, b.log)
	b.sessionSvc.SetComputerLoginCheck(b.agentAccountSvc.RunsOnComputerLogin)
	// A chat refused its credentials keeps asking for a sign-in until a reading
	// of its own account finds the login working again, however it came back.
	b.agentAccountSvc.SetLoginConfirmed(func(account domain.AgentAccount, at time.Time) {
		go func() {
			waiting, err := b.store.ConversationsAwaitingLogin(b.ctx)
			if err != nil {
				b.log.Warn("list chats awaiting sign-in", "error", err)
				return
			}
			for _, conversation := range waiting {
				id, err := b.agentAccountSvc.SessionAccountID(b.ctx, conversation.SessionID, conversation.Harness)
				if err != nil || id != account.ID {
					continue
				}
				if _, err := b.store.ConfirmConversationLogin(b.ctx, conversation.ConversationID, at, time.Now().UTC()); err != nil {
					b.log.Warn("clear chat sign-in demand", "conversation", conversation.ConversationID, "error", err)
				}
			}
		}()
	})
	b.accountAdoptSvc = newAccountAdopt(b.ctx, b.sessMgr, b.store, b.agentAccountSvc, b.log)
	if b.accountAdoptSvc != nil {
		// The batch runners stop with ctx; wait so they do not write to a closed store.
		b.cleanup(func() { b.accountAdoptSvc.Wait(5 * time.Second) })
	}
	if b.agentSvc != nil {
		b.agentSvc.Codex().SetSavedDefaultSignIn(b.agentAccountSvc.DefaultSignIn)
		if mover, ok := b.sessMgr.(interface {
			SetAccountMoved(func(domain.AgentHarness))
		}); ok {
			mover.SetAccountMoved(func(harness domain.AgentHarness) {
				b.agentSvc.InvalidateAgentAuthentication(string(harness))
			})
		}
		b.agentAccountSvc.SetDefaultChanged(func(p domain.AgentAccountProvider) {
			harness := map[domain.AgentAccountProvider]domain.AgentHarness{
				domain.AgentAccountProviderClaude: domain.HarnessClaudeCode,
				domain.AgentAccountProviderCodex:  domain.HarnessCodex,
			}[p]
			if harness != "" {
				b.agentSvc.InvalidateAgentAuthentication(string(harness))
			}
		})
	}
	if runner, ok := b.sessMgr.(interface {
		RunPromptSuggestionTask(context.Context, domain.SessionID, string, string, string) (string, error)
	}); ok {
		b.chatSvc.SetPromptSuggestionRunner(func(requestCtx context.Context, id domain.SessionID, turnID, systemPrompt, prompt string) (string, error) {
			taskCtx, cancel := context.WithCancel(requestCtx)
			stopOnShutdown := context.AfterFunc(b.ctx, cancel)
			defer stopOnShutdown()
			defer cancel()
			return runner.RunPromptSuggestionTask(taskCtx, id, turnID, systemPrompt, prompt)
		})
	}
	if observed, ok := b.sessMgr.(interface {
		SetStartFailureObserver(func(domain.SessionID, string))
	}); ok {
		observed.SetStartFailureObserver(b.turnNotifier.StartFailed)
	}
	if fenced, ok := b.sessMgr.(interface{ SetPersistentHostReconcileDone(<-chan struct{}) }); ok {
		fenced.SetPersistentHostReconcileDone(b.persistentHostsReconciled)
	}
	if wake, ok := b.sessMgr.(interface {
		WakeHibernatedChat(context.Context, domain.SessionID) error
	}); ok {
		b.chatSvc.SetWakeCallback(func(requestCtx context.Context, id domain.SessionID) error {
			wakeCtx, cancel := context.WithCancel(requestCtx)
			stopOnShutdown := context.AfterFunc(b.ctx, cancel)
			defer stopOnShutdown()
			defer cancel()
			return wake.WakeHibernatedChat(wakeCtx, id)
		})
	}
	if tunable, ok := b.sessMgr.(interface {
		SetModelCatalog(interface {
			Models(context.Context, string, string, bool) (ports.AgentModelCatalog, error)
		})
	}); ok {
		tunable.SetModelCatalog(b.agentSvc)
	}
}
