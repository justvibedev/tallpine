package daemon

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/persistenthost"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/systeminstall"
)

// serve: start the background workers, serve until stopped and shut down in order.
func (b *boot) serve() error {
	var startupReconcileDone <-chan struct{}
	var usageLimitResumeDone <-chan struct{}
	runErr := b.srv.RunWithReady(b.ctx, func() {
		// Agent-readiness warming is advisory and idempotent, and request paths
		// lazily Ensure on demand. Kick it here, after the listener is live, so its
		// bounded subprocess probes no longer contend with the synchronous
		// migration and fencing reconcile that gate the port bind.
		readinessDone := b.agentSvc.WarmReadiness()
		if b.cfg.HarnessUpdateChecks {
			go watchInstalledHarnessUpdates(b.ctx, readinessDone, b.cfg.HarnessUpdateInterval, b.agentSvc.EnsureInstallationReadiness, func(ctx context.Context, target systeminstall.Target, refresh bool) (systeminstall.UpdateAdvisory, error) {
				return b.systemInstall.UpdateAdvisory(ctx, target, refresh)
			}, b.log)
		}
		done := make(chan struct{})
		startupReconcileDone = done
		go func() {
			defer close(done)
			if reconcileErr := reconcilePersistentChatHosts(b.ctx, b.cfg.DataDir, b.store, b.cfg.AppRunID); reconcileErr != nil {
				b.log.Error("persistent chat host reconciliation on boot failed", "err", reconcileErr)
			}
			// Hosts alive now outlived the restart (controllers have not started
			// new ones yet): their agents' jobs are still running.
			adoptedHosts := map[domain.SessionID]bool{}
			if recs, listErr := b.store.ListAllSessions(b.ctx); listErr == nil {
				for _, rec := range recs {
					if _, alive := persistenthost.HostPID(b.cfg.DataDir, string(rec.ID)); alive {
						adoptedHosts[rec.ID] = true
					}
				}
			}
			// An unclean exit never recorded its in-flight turns: any chat session
			// still stored as mid-turn without a surviving host was cut off.
			if recErr := b.resumeTracker.RecordUnclean(b.ctx, func(id domain.SessionID) bool { return adoptedHosts[id] }); recErr != nil {
				b.log.Warn("restart resume: could not record turns cut off by an unclean exit", "err", recErr)
			}
			close(b.persistentHostsReconciled)
			// Reviewer-owned Chat controllers wait for persistent host reconciliation.
			// Recover them only after the listener is ready and the gate is closed, or
			// startup can deadlock before the daemon binds its port.
			if reconcileErr := b.reviewSvc.RecoverChatReviewers(b.ctx); reconcileErr != nil {
				b.log.Warn("reviewer chat recovery deferred", "err", reconcileErr)
			}
			close(b.reviewerChatsRecovered)
			if reconcileErr := b.sessMgr.ReconcileBackground(b.ctx); reconcileErr != nil {
				b.log.Error("background session reconciliation on boot failed", "err", reconcileErr)
			}
			if reconcileErr := b.lcStack.ReconcileRuntime(b.ctx); reconcileErr != nil {
				b.log.Error("background agent-process reconciliation on boot failed", "err", reconcileErr)
			}
			b.resumeTracker.Resume(b.ctx, b.sessMgr)
			b.resumeTracker.WakeIdle(b.ctx, b.sessMgr, func(id domain.SessionID) bool { return adoptedHosts[id] })
			b.resumeTracker.NotifyRestart(b.ctx, b.reportSvc)
		}()
		startIdleCompaction(b.ctx, b.store, b.chatSvc, b.bgTasks, b.sessionSvc.AutoHandoffOrchestrator, b.sessMgr, b.log)
		go func() {
			// The session operation gate skips reconciliation in progress, so
			// a slow provider reconnect must not stall hibernation of others.
			ticker := time.NewTicker(chatHibernateSweepTick)
			defer ticker.Stop()
			for {
				select {
				case <-b.ctx.Done():
					return
				case <-ticker.C:
					// Retried whatever the setting: a session that slept before
					// it was turned off must still get its queued messages.
					if err := b.chatSvc.RetryQueuedWakes(b.ctx); err != nil && b.ctx.Err() == nil {
						b.log.Warn("queued Chat wake retry failed", "err", err)
					}
					if !b.settingsSvc.ChatHibernationEnabled() {
						continue
					}
					if err := b.sessMgr.HibernateIdleChats(b.ctx); err != nil && b.ctx.Err() == nil {
						b.log.Warn("idle Chat hibernation failed", "err", err)
					}
				}
			}
		}()
		resumeDone := make(chan struct{})
		usageLimitResumeDone = resumeDone
		go func() {
			defer close(resumeDone)
			var sweeps sync.WaitGroup
			defer sweeps.Wait()
			// Recovery must attach existing provider work before scheduled retries
			// inspect controller readiness. The durable opt-in survives shutdown.
			select {
			case <-done:
			case <-b.ctx.Done():
				return
			}
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				if b.ctx.Err() != nil {
					return
				}
				// The service bounds overlapping scans and provider attempts. A wake
				// must not stop the next tick discovering another session's deadline.
				sweeps.Go(func() {
					if err := b.chatSvc.ResumeUsageLimitedTurns(b.ctx); err != nil && b.ctx.Err() == nil {
						b.log.Warn("usage-limit auto-resume failed", "err", err)
					}
				})
				select {
				case <-b.ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
		go runVersionRefreshSweep(b.ctx, b.cfg, b.settingsSvc, b.sessMgr, b.systemInstall, b.log)
	})

	// Both graceful shutdown paths (SIGTERM and POST /shutdown) funnel through
	// srv.Run returning. We deliberately do NOT tear down sessions here: they
	// survive the daemon exit and the next boot's Reconcile adopts them,
	// preserving session IDs. The narrowed sessionLifecycle interface makes
	// teardown-on-shutdown a compile error.

	return b.shutdownAfterRun(runErr, startupReconcileDone, usageLimitResumeDone)
}

// shutdownAfterRun stops the background goroutines in order once the server
// has returned, and passes runErr through.
func (b *boot) shutdownAfterRun(runErr error, startupReconcileDone, usageLimitResumeDone <-chan struct{}) error {
	// Shut the background goroutines down in order: cancel the context FIRST so
	// their loops exit, then wait for them to drain. Doing this explicitly (not
	// via defer) avoids the LIFO trap where a Stop() that blocks on ctx-cancel
	// runs before the cancel: a non-signal exit path would hang otherwise.
	//
	// A deliberate quit parks every agent further down. Releasing the signal
	// context restores the default SIGTERM action, which would kill the daemon
	// half way through and leave the remaining agents running, so hold signals
	// off from here until the bounded park is done.
	// A deliberate quit parks every agent, which is what cuts a turn short; record
	// the turns in flight now, before that, so the next boot can tell them. A plain
	// restart only detaches and leaves turns running.
	if recErr := b.resumeTracker.RecordIfQuit(context.Background(), b.srv.StopAgentsOnExit()); recErr != nil {
		b.log.Warn("restart resume: could not record in-flight turns at shutdown", "err", recErr)
	}
	var held chan os.Signal
	if b.srv.StopAgentsOnExit() {
		held = make(chan os.Signal, 1)
		signal.Notify(held, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	}
	b.stop()
	if usageLimitResumeDone != nil {
		<-usageLimitResumeDone
	}
	installStopCtx, installStopCancel := context.WithTimeout(context.Background(), b.cfg.ShutdownTimeout)
	if err := b.systemInstall.Close(installStopCtx); err != nil {
		b.log.Error("harness installer shutdown", "err", err)
	}
	installStopCancel()
	if startupReconcileDone != nil {
		<-startupReconcileDone
	}
	backgroundStopCtx, backgroundStopCancel := context.WithTimeout(context.Background(), b.cfg.ShutdownTimeout)
	if err := b.sessMgr.WaitBackgroundWorkers(backgroundStopCtx); err != nil {
		b.log.Error("session background worker shutdown", "err", err)
	}
	backgroundStopCancel()
	switchStopCtx, switchCancel := context.WithTimeout(context.Background(), b.cfg.ShutdownTimeout)
	if err := b.sessMgr.WaitAgentSwitchWorkers(switchStopCtx); err != nil {
		b.log.Error("interface recovery worker shutdown", "err", err)
	}
	switchCancel()
	codexSwitchStopCtx, codexSwitchCancel := context.WithTimeout(context.Background(), b.cfg.ShutdownTimeout)
	if err := b.agentSvc.Codex().WaitCodexAccountSwitchWorkers(codexSwitchStopCtx); err != nil {
		b.log.Error("Codex account switch worker shutdown", "err", err)
	}
	codexSwitchCancel()
	b.managedPreview.Close()
	<-b.previewDone
	// Detach chat controllers before stopping the lifecycle stack. Persistent
	// provider hosts deliberately survive this daemon and preserve in-flight
	// turns; the replacement daemon reconnects to the same initialized stream and
	// consumes host-replayed output. Explicit session termination, not daemon
	// shutdown, destroys them.
	if b.srv.StopAgentsOnExit() {
		// A deliberate quit (app quit, OS logout or restart, `tallpine stop
		// --stop-agents`): no agent may outlive this daemon. Each session is
		// put to sleep and resumes on demand under the next daemon.
		parkAgents(b.chatSvc, b.cfg.DataDir, b.cfg.ShutdownTimeout, b.log)
		signal.Stop(held)
	}
	chatStopCtx, chatCancel := context.WithTimeout(context.Background(), b.cfg.ShutdownTimeout)
	b.turnNotifier.Close()
	<-b.turnFallbackDone
	b.chatSvc.StopAll(chatStopCtx)
	chatCancel()
	if b.usageDone != nil {
		<-b.usageDone
	}
	if b.usagePricing != nil {
		b.usagePricing.Wait()
	}
	b.lcStack.Stop()
	// Tear the tailnet proxy down before the listener it fronts. `tailscale
	// serve --bg` state lives in tailscaled and outlives this process, so
	// leaving it would keep publishing a local port that no longer has the
	// authenticated LAN listener behind it. Best-effort and never blocking:
	// boot restore re-applies it against the next bound port.
	b.bs.ShutdownServe()
	// And the connector before that again, for the same reason: cloudflared is
	// a separate process that outlives this one, so leaving it would keep a
	// public hostname resolving to a port that is about to close. Stopping it
	// does not disable the bridge — boot restore starts a new one.
	b.bs.ShutdownTunnel()
	b.bs.ShutdownKeepAwake()
	lanStopCtx, lanCancel := context.WithTimeout(context.Background(), b.cfg.ShutdownTimeout)
	b.cleanup(lanCancel)
	if err := b.lan.Stop(lanStopCtx); err != nil {
		b.log.Error("mobile LAN listener shutdown", "err", err)
	}
	if err := b.cdcPipe.Stop(); err != nil {
		b.log.Error("cdc pipeline shutdown", "err", err)
	}
	return runErr
}
