package daemon

import (
	"context"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/binaryutil"
	"tallpine.eu/tallpine/backend/internal/adapters/systemexec"
	"tallpine.eu/tallpine/backend/internal/domain"
	backlogsvc "tallpine.eu/tallpine/backend/internal/service/backlog"
	reportsvc "tallpine.eu/tallpine/backend/internal/service/report"
	"tallpine.eu/tallpine/backend/internal/service/systemcheck"
	"tallpine.eu/tallpine/backend/internal/service/systeminstall"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
)

// wireReview: backlog, plan, report delivery, review notices, tracker intake and system install.
func (b *boot) wireReview() error {
	var ok bool

	b.backlogSvc = backlogsvc.New(backlogsvc.Deps{Store: b.store, Logger: b.log})
	b.planSvc = newPlanService(b.cfg, b.store, b.backlogSvc, b.sessionSvc, b.agentPolicySvc, b.notificationWriter)
	// Turning planAutoApprove on also approves plans already waiting (B516).
	b.projectSvc.SetConfigChangedHook(b.planSvc.ApproveWaiting)

	if gated, ok := b.reviewSvc.(interface {
		SetPlanGate(func(context.Context, domain.SessionID) (string, bool))
	}); ok {
		gated.SetPlanGate(b.planSvc.OpenPlanOf)
	}

	b.reportSvc = reportsvc.New(reportsvc.Deps{Store: b.store, BatchWindow: domain.ReportBatchWindow, FileDeferred: b.backlogSvc.FileDeferred, Artifacts: b.chatSvc, RefreshBranchState: b.sessionSvc.ReconcileSessionBranchState, ClaimReportedPR: reportedPRClaimer{sessions: b.sessionSvc}.Claim, DigestCount: b.reportDigestCount, OnCreated: func(r domain.ReportRecord) {
		// A daemon-authored notice about the worker is not the worker reporting.
		if !r.Synthesized {
			b.turnNotifier.ReportFiled(r)
		}
		b.turnNotifier.ApprovalReported(r)
		if !r.Synthesized && r.State == domain.ReportDone {
			// Off the report path: soft verification reads the worktree.
			go func() {
				hookCtx, cancel := context.WithTimeout(b.ctx, time.Minute)
				defer cancel()
				if err := b.backlogSvc.ReportFiled(hookCtx, r, b.reportSvc.UnverifiedDoneReason); err != nil && b.ctx.Err() == nil {
					b.log.Warn("backlog done-report hook failed", "session", r.SessionID, "error", err)
				}
			}()
		}
		b.reviewOnReport(r)
		if b.reportCoordinator != nil {
			b.reportCoordinator.Wake()
		}
	}})
	backlogLifecycleCtx, stopBacklogLifecycle := context.WithCancel(b.ctx)
	backlogLifecycleDone := backlogsvc.NewSubscriber(b.backlogSvc, backlogsvc.SubscriberConfig{Logger: b.log}).Start(backlogLifecycleCtx, b.cdcPipe.Broadcaster)
	b.cleanup(func() { stopBacklogLifecycle(); <-backlogLifecycleDone })
	b.turnNotifier.SetReports(b.reportSvc)
	b.turnFallbackDone = b.turnNotifier.Start(b.ctx)
	b.cleanup(func() { b.turnNotifier.Close(); <-b.turnFallbackDone })
	b.reviewNotices, ok = b.reviewSvc.(reviewFailureSweeper)
	if !ok {
		return errors.New("wire review failure notices: review service lacks failure notice support")
	}
	b.reviewNotices.SetFailureNotice(b.store, b.reportSvc, b.lcStack.LCM)
	// The store must settle an archived project's reports; without it they
	// would stay pending, and parked, forever.
	var _ reportsvc.ArchivedReportDropper = b.store
	var _ reportsvc.StaleReportStore = b.store
	var _ reportsvc.QuietReportStore = b.store
	b.reportCoordinator = reportsvc.NewCoordinator(reportsvc.CoordinatorDeps{
		Store:    b.store,
		Delivery: reportSemanticDelivery{chat: b.chatSvc, sessions: b.reportSessions, store: b.store},
		Logger:   b.log,
		Verify:   b.reportSvc.UnverifiedDoneReason,
		Notifier: b.notificationWriter,
	})
	b.chatSvc.SetReportCoordinator(b.reportCoordinator)
	b.lcStack.LCM.SetOrchestratorIdleObserver(func(rec domain.SessionRecord) {
		b.reportCoordinator.Available(rec.ID, rec.Metadata.ControllerGeneration)
	})
	reportDeliveryDone := b.reportCoordinator.Start(b.ctx)
	b.cleanup(func() {
		b.stop()
		<-reportDeliveryDone
	})
	b.lcStack.trackerDone = startTrackerIntake(b.ctx, b.cfg, b.store, b.sessionSvc, b.tracker, b.log)

	b.hostCommands = systemexec.New(b.cfg.DataDir)
	b.systemChecks = systemcheck.NewWithCommandRunner(b.agentSvc, b.hostCommands, b.hostCommands)
	b.systemInstall = systeminstall.NewWithDeps(b.hostCommands, b.hostCommands, systeminstall.Deps{
		JobStore:                  b.store,
		Verifier:                  systeminstall.NewVerifier(b.agents, b.hostCommands),
		Sessions:                  b.store,
		Reviewers:                 b.store,
		DisableUpdateChecks:       !b.cfg.HarnessUpdateChecks,
		DisableUpdateWhileRunning: !b.cfg.UpdateWhileRunning,
		UpdateWhenIdle:            b.cfg.UpdateWhenIdle,
		ManagedRoot:               managedHarnessRoot(b.cfg),
		Releases:                  b.store,
	})
	if err := b.systemInstall.Recover(b.ctx); err != nil {
		b.stop()
		b.lcStack.Stop()
		if cdcErr := b.cdcPipe.Stop(); cdcErr != nil {
			b.log.Error("cdc pipeline shutdown", "err", cdcErr)
		}
		return fmt.Errorf("recover harness install jobs: %w", err)
	}
	b.sessMgr.SetHarnessUseGate(b.systemInstall)
	b.systemInstall.SetHarnessUpdateObserver(b.sessMgr)
	b.systemInstall.SetIdleUpdateCoordinator(b.sessMgr)
	// Harness resolvers prefer Tallpine's managed launchers once an update created them.
	binaryutil.SetManagedBinDir(b.systemInstall.ManagedBinDir())
	binaryCaches := []harnessBinaryCache{
		resolverBinaryCache{agents: b.agents},
		b.chatDrivers,
		b.reviewers,
		pluginBinaryCache{harness: domain.HarnessCodex, plugin: b.codexModelPlugin},
	}
	b.systemInstall.SetBinaryInvalidator(func(target systeminstall.Target) {
		if harness, ok := installedAgentHarness(target); ok {
			invalidateHarnessBinary(b.agentSvc, harness, binaryCaches...)
		}
	})
	b.sessMgr.SetVersionRefreshPolicy(func() sessionmanager.VersionRefreshPolicy {
		return versionRefreshPolicy(b.ctx, b.cfg, b.settingsSvc)
	})
	b.systemInstall.SetOnSucceeded(func(target systeminstall.Target) {
		harness, ok := installedAgentHarness(target)
		if !ok {
			return
		}
		invalidateHarnessBinary(b.agentSvc, harness, binaryCaches...)
		b.agentSvc.RecheckAgent(harness)
	})
	return nil
}

// reportDigestCount is the project's quiet-report flush threshold (0 = off).
func (b *boot) reportDigestCount(ctx context.Context, id domain.ProjectID) int {
	proj, ok, err := b.store.GetProject(ctx, string(id))
	if err != nil || !ok {
		return 0
	}
	return proj.Config.ReportDigestCount
}

// reviewOnReport starts the native review when a worker reports a PR ready
// (a done report or a pr_created output) unless the project opted out. Off the
// report path: the review launch is slow. A skipped or failed evaluation is
// left to the idle sweep.
func (b *boot) reviewOnReport(r domain.ReportRecord) {
	if r.Synthesized || b.autoReview == nil || !reportsPRReady(r) {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(b.ctx, 5*time.Minute)
		defer cancel()
		if _, err := b.autoReview.EvaluateReady(ctx, r.SessionID); err != nil && b.ctx.Err() == nil {
			b.log.Warn("auto-review on report failed", "session", r.SessionID, "error", err)
		}
	}()
}

func reportsPRReady(r domain.ReportRecord) bool {
	if r.State == domain.ReportDone {
		return true
	}
	for _, o := range r.Outputs {
		if o.Kind == domain.ReportOutputPRCreated {
			return true
		}
	}
	return false
}
