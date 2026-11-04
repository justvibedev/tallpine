package daemon

import (
	"context"
	"fmt"
	"os"
	"time"

	"tallpine.eu/tallpine/backend/internal/autoreview"
	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/mobilebridge"
	bridgesvc "tallpine.eu/tallpine/backend/internal/mobilebridge/service"
	usagepipeline "tallpine.eu/tallpine/backend/internal/observe/usage"
	"tallpine.eu/tallpine/backend/internal/presence"
	"tallpine.eu/tallpine/backend/internal/push"
	"tallpine.eu/tallpine/backend/internal/restartresume"
	"tallpine.eu/tallpine/backend/internal/service/agentauth"
	awakesvc "tallpine.eu/tallpine/backend/internal/service/awake"
	browsersvc "tallpine.eu/tallpine/backend/internal/service/browser"
	prsvc "tallpine.eu/tallpine/backend/internal/service/pr"
	reviewsvc "tallpine.eu/tallpine/backend/internal/service/review"
	usagesvc "tallpine.eu/tallpine/backend/internal/service/usage"
	"tallpine.eu/tallpine/backend/internal/service/waitescalation"
)

// wireMobile: the mobile bridge, shell terminals, usage, SCM observers and automations.
func (b *boot) wireMobile() error {
	// Connect Mobile: the bridge service needs the LAN listener, but the LAN
	// listener needs the built router's handler, which only exists once srv is
	// constructed — and srv's router mounts the mobile controller, which needs
	// the bridge service. Break the cycle with late binding: build bs with LAN
	// left nil, hand its controller into NewWithDeps, then once srv exists,
	// build the LAN listener over srv.Handler() and assign it onto bs.LAN.
	b.wireMobileBridge()
	b.wireShellTerminals()
	b.wireUsage()
	b.lcStack.scmDone = startSCMObserver(b.ctx, b.store, b.lcStack.LCM, b.sessionSvc, b.cfg.GitLab, b.log)

	b.wirePRActions()
	// Codex switch recovery is best effort and never blocks unrelated daemon
	// startup. Its own credential gate still protects any local mutation.
	if reconcileErr := b.agentSvc.Codex().ReconcileCodexAccountSwitches(b.ctx); reconcileErr != nil {
		b.log.Warn("Codex account switch recovery deferred", "err", reconcileErr)
	}

	// Durable interface-transition recovery is the startup
	// safety boundary. The in-memory input fence disappeared with the previous
	// daemon; every active saga must be closed or explicitly quarantined before
	// binding a usable API, without accidentally reopening input. Runtime/worktree
	// restoration follows in the background after the listener is live.
	// Workers whose turn a deliberate quit cut short were recorded at that
	// shutdown; they get a resume message once startup settles. Sessions noted
	// at a bare shutdown or still stored as mid-turn after a crash are recorded
	// at boot (RecordUnclean) only if their chat host did not survive; a
	// surviving host kept its turn, so activity alone proves nothing.
	b.resumeTracker = restartresume.New(b.cfg.DataDir, b.store)
	if reconcileErr := b.sessMgr.ReconcileStartupSafety(b.ctx); reconcileErr != nil {
		b.stop()
		b.managedPreview.Close()
		b.lcStack.Stop()
		if cdcErr := b.cdcPipe.Stop(); cdcErr != nil {
			b.log.Error("cdc pipeline shutdown", "err", cdcErr)
		}
		return fmt.Errorf("reconcile sessions on boot: %w", reconcileErr)
	}
	b.agentSvc.Codex().WarmCodexAccounts()
	var automationDone <-chan struct{}
	b.automationSvc, automationDone = startAutomations(b.ctx, b.store, b.sessionSvc, b.log)
	b.lcStack.automationDone = automationDone
	b.autoReview = autoreview.New(b.store, b.reviewSvc, autoreview.Config{Logger: b.log, Escalate: b.escalateReviewRounds})
	b.lcStack.autoReviewDone = b.autoReview.Start(b.ctx)
	b.lcStack.reviewSweepDone = b.reviewNotices.StartSweep(b.ctx, reviewsvc.DefaultSweepInterval)
	b.lcStack.waitEscalationDone = waitescalation.New(b.store, b.notificationWriter, nil, b.log).Start(b.ctx, waitescalation.DefaultInterval)
	b.wirePushDevices()
	b.wireMobileTunnel()
	b.wireHostIdentityAndPush()
	if mobilebridge.KeepAwakeSupported() {
		ka := mobilebridge.NewKeepAwake(os.Getpid())
		ka.SetLidController(mobilebridge.NewLidAwake(os.Getpid(), brand.Getenv("WAKE_HELPER"), b.cfg.DataDir))
		b.bs.KeepAwake = ka
	}
	awakeHost := awakesvc.NewHost(os.Getpid(), brand.Getenv("WAKE_HELPER"), b.cfg.DataDir)
	var stopAwake func()
	b.awakeSvc, stopAwake = startAwake(b.ctx, b.store, awakeHost, awakesvc.BindingFuncs{
		Session: func(ctx context.Context, id string) (bool, error) {
			row, found, err := b.store.GetSession(ctx, domain.SessionID(id))
			return found && !row.IsTerminated, err
		},
		Agent:       b.store.HasAwakeAgentActivity,
		Interrupted: b.chatSvc.InterruptedAwakeAgents,
		Reconnect:   b.sessMgr.ReconnectAwakeAgent,
		App:         awakeHost.AppAlive,
	}, b.log)
	b.cleanup(stopAwake)
	return nil
}

// wireMobileBridge builds the bridge service, its controller and the browser live hub.
func (b *boot) wireMobileBridge() {
	b.bs = &bridgesvc.BridgeService{
		ConfigPath:  mobilebridge.Path(b.cfg.DataDir),
		DefaultPort: mobilebridge.DefaultPort,
	}
	b.bs.ManagedOff = b.mobileManagedOff
	// HostID is assigned below, once the identity file has been read.
	b.mc = &controllers.MobileController{Bridge: b.bs}
	b.browserService = browsersvc.New(b.sessionSvc, b.browserBroker, b.browserAuthority)
	b.browserLive = httpd.NewBrowserLiveHub(b.browserStreamBroker, b.sessionSvc, func() bool {
		state, err := mobilebridge.Load(b.bs.ConfigPath)
		return err == nil && state.FeatureEnabled && state.Enabled && state.BrowserRemoteAccess && !b.bs.ManagedOffNow()
	}, b.log)
	b.bs.OnBrowserRemoteAccessDisabled = b.browserLive.CloseAll
}

// wireShellTerminals starts the standalone shell terminals and hands them to their consumers.
func (b *boot) wireShellTerminals() {
	// Standalone shell terminals: user-opened shells with no agent session
	// behind them. They reuse the same runtime adapter (and therefore the same
	// terminal mux) as session panes, but keep their own ids, storage, and
	// lifetime — see internal/service/shellterm.
	b.shellTermSvc = startShellTerminals(b.ctx, b.cfg, b.runtimeAdapter, b.store, b.projectSvc, b.sessionSvc, b.log)
	b.systemChecks.SetGitHubAuthTerminalOpener(b.shellTermSvc)
	b.agentAuthSvc = agentauth.NewWithAgentResolver(b.hostCommands, b.agentSvc, b.shellTermSvc, b.cfg.DataDir)
	b.agentSvc.Codex().SetCodexAccountLoginTerminalOpener(b.shellTermSvc)
	b.agentAccountSvc.SetTerminals(b.shellTermSvc)
	// Late-bound so Kill/Cleanup close a session's scoped shells before its
	// worktree is torn down (shellTermSvc cannot exist before sessMgr does; see
	// SetShellTerminalCloser).
	b.sessMgr.SetShellTerminalCloser(b.shellTermSvc)
}

// wireUsage wires usage pricing, collection and the ingestion pipeline.
func (b *boot) wireUsage() {
	if pricingRuntime, pricingErr := newUsagePricingRuntime(usagePricingRuntimeConfig{
		DataDir: b.cfg.DataDir,
		Store:   b.store,
		Logger:  b.log,
	}); pricingErr != nil {
		b.log.Warn("usage pricing disabled", "err", pricingErr)
	} else {
		b.usagePricing = pricingRuntime
		if pricingErr := pricingRuntime.Start(b.ctx); pricingErr != nil {
			b.log.Warn("usage pricing startup failed; continuing without catalog enrichment", "err", pricingErr)
		}
		b.cleanup(func() {
			b.stop()
			b.usagePricing.Wait()
		})
	}
	if roots, rootsErr := usageSourceRoots(b.ctx); rootsErr != nil {
		b.log.Warn("usage collection disabled", "err", rootsErr)
	} else {
		b.usageCollector = usagesvc.NewCollector(b.store, roots, func(reconcile bool) {
			if b.usagePipeline == nil {
				return
			}
			if reconcile {
				b.usagePipeline.NotifySourcesChanged()
			} else {
				b.usagePipeline.NotifyInventoryChanged()
			}
		})
		if b.usagePricing != nil {
			b.usageCollector.OnRouteResolved(b.usagePricing.RepairLegacyAttribution)
		}
		ingestorConfig := usagepipeline.IngestorConfig{}
		if b.usagePricing != nil {
			ingestorConfig.Pricing = b.usagePricing.Manager()
			ingestorConfig.OnPricingError = func(err error) {
				b.log.Warn("usage event pricing failed", "err", err)
			}
			ingestorConfig.RequestAttributionRepair = b.usagePricing.RepairLegacyAttribution
		}
		ingestor := usagepipeline.NewIngestor(b.store, ingestorConfig)
		b.usagePipeline = usagepipeline.NewPipeline(b.store, ingestor, usagePipelineWatchRoots(roots), usagepipeline.CoordinatorConfig{
			Logger:     b.log,
			Initialize: b.usageCollector.BackfillActive,
			Reconcile: func(reconcileCtx context.Context) error {
				return b.usageCollector.ReconcileSources(reconcileCtx, 0)
			},
			ReconcilePath: b.usageCollector.ReconcilePath,
		})
		// Safety-net reconcile tick. The pipeline is event-driven otherwise:
		// a silently lost fsnotify watch or a resume transcript that arrived
		// without a hook would otherwise never be re-checked. NotifySourcesChanged
		// fans into the coordinator's refresh, which reconciles sources (registering
		// continuation transcripts) and rebuilds the watch set. Survives
		// coordinator restarts because the pipeline forwards to the live instance.
		go func() {
			ticker := time.NewTicker(usageReconcileTick)
			defer ticker.Stop()
			for {
				select {
				case <-b.ctx.Done():
					return
				case <-ticker.C:
					b.usagePipeline.NotifySourcesChanged()
				}
			}
		}()
		b.lcStack.LCM.SetUsageFinalizer(b.usageCollector)
	}
}

// wirePRActions builds the SCM reader and the PR action service.
func (b *boot) wirePRActions() {
	b.prReader = newMultiSCMProvider(b.cfg.GitLab, b.log)
	prMerger := newMultiSCMMerger(b.cfg.GitLab, b.log)
	if b.prReader != nil && prMerger != nil {
		b.prActions = prsvc.NewActionService(prsvc.ActionDeps{
			Store:        b.store,
			Merger:       prMerger,
			Reader:       b.prReader,
			Resolver:     b.prReader,
			Writer:       b.store,
			MergeWriter:  b.store,
			ThreadWriter: b.store,
			Lifecycle:    b.lcStack.LCM,
			MergePolicy:  b.store,
			Logger:       b.log,
		})
	} else {
		b.log.Warn("pr action service disabled: no usable SCM provider")
	}
}

// wirePushDevices loads the push-device registry and the presence tracker.
func (b *boot) wirePushDevices() {
	// Push-device registry: persisted phones that receive OS push notifications.
	// A load failure must not block boot — degrade to no push rather than refusing
	// to start the daemon. pushRegistry (interface) is assigned only when load
	// succeeds so a failure leaves a true nil interface (not a non-nil interface
	// wrapping a nil pointer), which the controller's nil guard relies on to
	// return 501. pushDevices keeps the concrete registry for the dispatcher.
	// deviceRoster (interface) mirrors the same nil-guard as pushRegistry: it is
	// assigned only when load succeeds, so a failed load leaves a true nil
	// interface rather than a non-nil interface wrapping a nil *DeviceRegistry
	// (which would panic on first method call). The roster controller answers
	// 503 DEVICE_REGISTRY_UNAVAILABLE in that state instead of crashing or
	// silently no-oping.

	b.deviceRev = loadDeviceTokens(b.cfg.DataDir, b.log)
	if reg, regErr := mobilebridge.LoadRegistry(mobilebridge.PushDevicesPath(b.cfg.DataDir)); regErr != nil {
		b.log.Warn("load push device registry failed; push notifications disabled", "err", regErr)
	} else {
		b.pushRegistry = revokingPush{PushRegistry: reg, roster: reg, rev: b.deviceRev}
		b.pushDevices = reg
		b.deviceRoster = revokingRoster{DeviceRoster: reg, rev: b.deviceRev}
	}

	// One presence tracker instance shared by APIDeps.Presence (the
	// heartbeat middleware that touches it) and APIDeps.DeviceLive (the roster
	// controller that reads it) — must be the same instance or every device
	// would silently report offline.
	b.presenceTracker = presence.NewTracker()
}

// wireMobileTunnel reaps stale tunnels and installs the cloudflared resolvers.
func (b *boot) wireMobileTunnel() {
	// Managed remote-access connector. Reap first: a daemon that died without
	// stopping its connector leaves a public tunnel to this machine running
	// with nobody watching it.
	tunnelPID := mobilebridge.TunnelPIDPath(b.cfg.DataDir)
	if reapErr := mobilebridge.ReapStaleTunnel(tunnelPID, mobilebridge.IsLiveCloudflared, mobilebridge.KillProcess); reapErr != nil {
		b.log.Warn("could not reap a stale mobile tunnel", "error", reapErr)
	}
	// Looked up again whenever the bridge is enabled, so a cloudflared the user
	// installs from Connect Mobile is picked up without restarting Tallpine.
	b.bs.ResolveTunnel = func() bridgesvc.TunnelController {
		res := mobilebridge.ResolveCloudflared(mobilebridge.LocalCloudflaredLookup(b.cfg.DataDir))
		if res.NeedsInstall {
			return nil
		}
		b.log.Info("mobile remote access available", "cloudflared", res.Path, "source", res.Source)
		return mobilebridge.NewManagedTunnel(mobilebridge.ManagedTunnelDeps{
			Binary: res.Path, PIDPath: tunnelPID, Log: b.log,
		})
	}
	// A system cloudflared is version-checked by running it (30-120ms, up to a
	// 5s timeout), so resolve off the boot path. Status reads and anything that
	// needs a connector, such as restoring an enabled bridge, wait for this
	// lookup rather than racing it.
	//
	// Boot-time probing runs `cloudflared --version`; do it only for an install
	// that has opted in to mobile and to the quick tunnel. Anyone else resolves
	// lazily (ensureTunnel) at the moment they opt in.
	if st, loadErr := mobilebridge.Load(b.bs.ConfigPath); loadErr == nil && st.FeatureEnabled && st.QuickTunnel {
		b.bs.ResolveTunnelInBackground(func() bridgesvc.TunnelController {
			res := mobilebridge.ResolveCloudflared(mobilebridge.LocalCloudflaredLookup(b.cfg.DataDir))
			if res.NeedsInstall {
				// Not fatal: the LAN and Tailscale endpoints still work, and Connect
				// Mobile behaves exactly as it did before remote access existed.
				b.log.Info("mobile remote access unavailable; cloudflared not installed",
					"rejectedSystemPath", res.SystemPath)
				return nil
			}
			b.log.Info("mobile remote access available", "cloudflared", res.Path, "source", res.Source)
			return mobilebridge.NewManagedTunnel(mobilebridge.ManagedTunnelDeps{
				Binary: res.Path, PIDPath: tunnelPID, Log: b.log,
			})
		})
	}
}

// wireHostIdentityAndPush establishes the host identity and starts the push dispatcher.
func (b *boot) wireHostIdentityAndPush() {
	// Stable, machine-bound host identity, served by the unauthenticated
	// GET /api/v1/identity probe. A failure here is not fatal: the probe then
	// answers 501 and the phone falls back to pairing without identity
	// verification, which is how it behaved before the probe existed.
	var identityErr error
	b.hostIdentity, identityErr = mobilebridge.EnsureLocalIdentity(b.cfg.DataDir)
	if identityErr != nil {
		b.log.Warn("could not establish host identity; /api/v1/identity will report unimplemented", "error", identityErr)
	}

	b.bs.HostID = b.hostIdentity.HostID
	// Pushes include the authoritative host identity so a phone can reject
	// same-numbered sessions on another machine. No identity means no push.
	if b.pushDevices != nil && b.hostIdentity.HostID != "" {
		dispatcher := push.NewDispatcher(b.notificationHub, b.pushDevices, push.NewExpoClient(os.Getenv("EXPO_ACCESS_TOKEN")), b.hostIdentity.HostID, b.log)
		// Push relay is part of the opt-in mobile feature: checked per send so a
		// runtime switch-off stops it without a restart.
		dispatcher.SetEnabled(func() bool {
			st, loadErr := mobilebridge.Load(b.bs.ConfigPath)
			return loadErr == nil && st.FeatureEnabled && !b.bs.ManagedOffNow()
		})
		dispatcher.SetSessionNames(func(ctx context.Context, id domain.SessionID) string {
			row, found, getErr := b.store.GetSession(ctx, id)
			if getErr != nil || !found {
				return ""
			}
			return row.DisplayName
		})
		go dispatcher.Run(b.ctx)
	}
}
