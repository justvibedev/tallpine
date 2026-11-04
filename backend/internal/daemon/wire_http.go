package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"tallpine.eu/tallpine/backend/internal/adapters/workspace/gitworktree"
	"tallpine.eu/tallpine/backend/internal/browserruntime"
	"tallpine.eu/tallpine/backend/internal/browserstream"
	"tallpine.eu/tallpine/backend/internal/buildinfo"
	"tallpine.eu/tallpine/backend/internal/daemon/supervisor"
	"tallpine.eu/tallpine/backend/internal/httpd"
	"tallpine.eu/tallpine/backend/internal/mobilebridge"
	"tallpine.eu/tallpine/backend/internal/preview"
	"tallpine.eu/tallpine/backend/internal/releaseinstall"
	cuesvc "tallpine.eu/tallpine/backend/internal/service/cue"
	devimportsvc "tallpine.eu/tallpine/backend/internal/service/devimport"
	fsbrowsersvc "tallpine.eu/tallpine/backend/internal/service/fsbrowser"
	"tallpine.eu/tallpine/backend/internal/service/githubcred"
	"tallpine.eu/tallpine/backend/internal/service/githubpat"
	healthsvc "tallpine.eu/tallpine/backend/internal/service/health"
	"tallpine.eu/tallpine/backend/internal/service/hookprocess"
	importsvc "tallpine.eu/tallpine/backend/internal/service/importer"
	linkpreviewsvc "tallpine.eu/tallpine/backend/internal/service/linkpreview"
	"tallpine.eu/tallpine/backend/internal/service/localland"
	officialimportsvc "tallpine.eu/tallpine/backend/internal/service/officialimport"
	"tallpine.eu/tallpine/backend/internal/service/sessioncommit"
	updatesvc "tallpine.eu/tallpine/backend/internal/service/update"
	usagesvc "tallpine.eu/tallpine/backend/internal/service/usage"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// wireHTTP: the HTTP server, browser and supervisor listeners and the mobile LAN.
func (b *boot) wireHTTP() error {
	var err error
	// Terminal hooks pass the hook process gate: a Claude process other than
	// the session's own must not replace its activity or transcript.
	// The turn observer sits inside the gate (gate -> observer -> lifecycle)
	// so hooks the gate drops never reach it.
	hookActivity := hookprocess.New(b.turnNotifier, b.store, b.runtimeAdapter, nil, b.log)
	b.srv, err = httpd.NewWithDeps(b.cfg, b.log, b.termMgr, httpd.APIDeps{
		SettingsAudit:        b.settingsAudit,
		SettingsChanges:      b.store,
		SettingsValues:       b.settingsValues,
		SettingsWriter:       b.settingsWriter,
		Awake:                b.awakeSvc,
		Projects:             b.projectSvc,
		DefaultBranch:        gitworktree.NewDefaultBranchResolver(""),
		GitHubAccountCheck:   githubAccountChecker{accounts: b.ghAccounts, projects: b.store},
		ArtifactVersions:     b.lcStack.ArtifactVersions,
		HostID:               b.hostIdentity.HostID,
		Endpoints:            b.bs,
		Agents:               b.agentSvc,
		CodexAccounts:        b.agentSvc.Codex(),
		AgentAccounts:        b.agentAccountSvc,
		AccountAdopt:         b.accountAdoptSvc,
		SystemChecks:         b.systemChecks,
		Installer:            b.systemInstall,
		Sessions:             b.sessionSvc,
		Automations:          b.automationSvc,
		DesktopWorkspaces:    b.sessionSvc,
		PRs:                  b.prActions,
		Reviews:              b.reviewSvc,
		Notifications:        b.notifier,
		Reports:              b.reportSvc,
		NotificationStream:   b.notificationHub,
		ApprovalNotifier:     b.notificationWriter,
		TerminalApprovals:    b.terminalApprovals,
		TerminalUsage:        b.terminalUsage,
		Push:                 b.pushRegistry,
		Presence:             b.presenceTracker,
		DeviceRoster:         b.deviceRoster,
		DeviceLive:           b.presenceTracker,
		Import:               importsvc.New(),
		Directories:          fsbrowsersvc.New(),
		ShellTerminals:       b.shellTermSvc,
		Backlog:              b.backlogSvc,
		BacklogSpawner:       b.backlogSvc,
		BacklogRuns:          newBacklogRunService(b.cfg, b.store, b.sessionSvc, b.planSvc),
		BacklogPlans:         b.planSvc,
		ReleaseInstall:       releaseinstall.New(filepath.Dir(b.cfg.RunFilePath)),
		ReleaseBuild:         releaseinstall.NewBuildRequester(filepath.Dir(b.cfg.RunFilePath), b.releaseProjectPath),
		Merges:               newMergesService(b.store),
		Intents:              b.store,
		Cues:                 cuesvc.New(cuesvc.Deps{Store: b.store, Sessions: b.sessionSvc, Terminals: b.shellTermSvc}),
		AgentAuth:            b.agentAuthSvc,
		GitHub:               newGitHubStatusService(githubpat.New(b.cfg.DataDir), b.prReader),
		Conversations:        b.chatSvc,
		Settings:             b.settingsSvc,
		AgentPolicy:          b.agentPolicySvc,
		AgentPolicyDecisions: b.store,
		GitHubCredentials:    githubcred.New(b.agentPolicySvc, b.store, b.store, b.ghAccounts, b.log),
		SessionCommit:        sessioncommit.New(b.store, b.log),
		SessionLand:          localland.New(b.store, b.store, gitworktree.NewDefaultBranchResolver(""), b.log),
		ArtifactStorage:      b.artifactStorage,
		Cleanup:              b.cleanupSvc,
		Storage:              newStorageUsage(b.cfg.DataDir, b.cfg.RunFilePath, b.store, b.sessMgr, b.cleanupSvc),
		Health:               &healthsvc.Service{Source: b.store, Sessions: b.store, Chat: b.chatSvc, Reports: b.reportCoordinator},
		Update:               &updatesvc.Service{Installed: buildinfo.Commit, StateDir: b.cfg.StateDir, Port: b.cfg.Port, Socket: updateSocket(b.cfg.RunFilePath)},
		CDC:                  b.store,
		Events:               b.cdcPipe.Broadcaster,
		StatusReadiness:      statusReadinessSource(b.sessMgr),
		Activity:             hookActivity,
		UsageHooks:           b.usageCollector,
		UsageSummary:         usagesvc.NewSummaryReader(b.store),
		SessionMemory:        b.memoryReader,
		LastMessages:         b.store,
		Models:               b.chatSvc,
		Instructions:         b.sessMgr,
		BackgroundTasks:      b.bgTasks,
		UsageLimits:          b.chatSvc,
		MessageSent:          b.turnNotifier.MessageSent,
		SessionSteps:         b.lcStack.LCM,
		Mobile:               b.mc,
		MobileFeatureEnabled: func() bool {
			st, err := mobilebridge.Load(b.bs.ConfigPath)
			return err == nil && st.FeatureEnabled && !b.bs.ManagedOffNow()
		},
		BrowserLive: b.browserLive,
		AOImport: officialimportsvc.New(officialimportsvc.Deps{
			Store:         b.store,
			Settings:      b.settingsSvc,
			TargetDataDir: b.cfg.DataDir,
		}),
		DevImport: devimportsvc.New(devimportsvc.Deps{
			Store:         b.store,
			TargetDataDir: b.cfg.DataDir,
			OpenSource: func(ctx context.Context, dataDir string) (devimportsvc.SourceStore, error) {
				return sqlite.OpenReadOnly(ctx, dataDir)
			},
		}),
		Browser:                  b.browserService,
		LinkPreview:              linkpreviewsvc.New(nil),
		PreviewServer:            b.managedPreview,
		SessionCapabilities:      b.browserAuthority,
		SessionTokens:            b.sessionTokens,
		AgentScopes:              b.store,
		PRScope:                  b.store,
		ShellPreviewCapabilities: b.shellTermSvc,
	})
	if err != nil {
		b.stop()
		b.lcStack.Stop()
		if cdcErr := b.cdcPipe.Stop(); cdcErr != nil {
			b.log.Error("cdc pipeline shutdown", "err", cdcErr)
		}
		return err
	}
	b.browserLive.SetLoopbackBaseURL("http://" + b.srv.Addr().String())
	b.previewDone = preview.NewPoller(b.store, b.sessionSvc, "http://"+b.srv.Addr().String(), preview.PollerConfig{Logger: b.log}).Start(b.ctx)
	if err := b.wireBrowserListeners(); err != nil {
		return err
	}
	b.wireMobileLAN()
	if b.usagePipeline != nil {
		b.usageDone = b.usagePipeline.Start(b.ctx)
	}
	// ponytail: 5s tolerates a brief frontend restart; tune if dev hot-reload trips it.
	const supervisorGrace = 5 * time.Second

	if ln, addr, err := supervisor.Listen(b.cfg.RunFilePath); err != nil {
		// Non-fatal: without the link the daemon still works (e.g. headless "tallpine start"),
		// it just will not auto-stop when a frontend dies. Do not block startup on it.
		b.log.Warn("supervisor: listener unavailable; frontend-death auto-stop disabled", "err", err)
	} else {
		b.log.Info("supervisor: listening", "addr", addr)
		sup := supervisor.New(supervisorGrace, b.srv.RequestShutdown, b.log)
		go func() {
			if err := sup.Serve(b.ctx, ln); err != nil {
				b.log.Warn("supervisor: serve stopped with error", "err", err)
			}
		}()
	}
	return nil
}

// wireBrowserListeners starts the browser-runtime and browser-stream listeners.
func (b *boot) wireBrowserListeners() error {
	_ = os.Unsetenv(browserruntime.RuntimeAddressEnv)
	if ln, addr, err := browserruntime.Listen(b.cfg.RunFilePath); err != nil {
		b.log.Warn("browser runtime: listener unavailable; agent browser control disabled", "err", err)
	} else {
		if err := os.Setenv(browserruntime.RuntimeAddressEnv, addr); err != nil {
			_ = ln.Close()
			return fmt.Errorf("publish browser runtime address: %w", err)
		}
		b.log.Info("browser runtime: listening", "addr", addr)
		go func() {
			if err := b.browserBroker.Serve(b.ctx, ln); err != nil {
				b.log.Warn("browser runtime: serve stopped with error", "err", err)
			}
		}()
	}
	_ = os.Unsetenv(browserstream.RuntimeAddressEnv)
	if ln, addr, err := browserstream.Listen(b.cfg.RunFilePath); err != nil {
		b.log.Warn("browser stream: listener unavailable; mobile live browser disabled", "err", err)
	} else {
		if err := os.Setenv(browserstream.RuntimeAddressEnv, addr); err != nil {
			_ = ln.Close()
			return fmt.Errorf("publish browser stream address: %w", err)
		}
		b.log.Info("browser stream: listening", "addr", addr)
		go func() {
			if err := b.browserStreamBroker.Serve(b.ctx, ln); err != nil {
				b.log.Warn("browser stream: serve stopped with error", "err", err)
			}
		}()
	}
	return nil
}

// wireMobileLAN builds the LAN listener over the loopback router and restores the bridge.
func (b *boot) wireMobileLAN() {
	// Late-bind: the LAN listener shares the exact loopback router instance so
	// the LAN surface and loopback surface never drift apart.
	b.lan = httpd.NewMobileLAN(b.srv.Handler(), b.hostIdentity.HostID, mobilebridge.DefaultPort, b.log)
	b.bs.LAN = b.lan
	// TLS for the phone gateway: a self-signed key pair the phone pins via the
	// pairing code. Plain http from the network is always refused (426), so a
	// failure to create the key leaves the gateway reachable only through
	// loopback proxies (Tailscale Serve, the tunnel), never plain.
	if gw, tlsErr := mobilebridge.EnsureGatewayTLS(b.cfg.DataDir); tlsErr != nil {
		b.log.Error("mobile gateway TLS unavailable; refusing network connections from phones", "err", tlsErr)
	} else {
		b.lan.SetTLS(gw.Cert)
		b.bs.TLSPin = gw.Pin
	}
	b.deviceRev.bind(b.lan)

	// Restore Connect Mobile across a daemon restart: if the bridge was left
	// enabled, re-arm the listener on its last port with the same password
	// hash so an already-paired phone keeps working with no new password, and
	// (via bs.RestoreOnBoot) re-apply the secure-pairing proxy against the
	// port Start actually bound. Routed through bs, not lan directly, so the
	// proxy never gets pinned to a dead port after an ephemeral fallback.
	// Best-effort: never blocks boot.
	if err := restoreMobileOnBoot(mobilebridge.Path(b.cfg.DataDir), b.bs); err != nil {
		b.log.Warn("restore mobile bridge on boot failed", "err", err)
	}
	// The managed file is re-read while the daemon runs; enforce it live.
	go enforceManagedMobile(b.ctx, managedMobileInterval, b.bs, b.log)
}

// releaseProjectPath resolves a project's registered checkout for the
// daemon-run release request-build.
func (b *boot) releaseProjectPath(ctx context.Context, projectID string) (string, error) {
	rec, ok, err := b.store.GetProject(ctx, projectID)
	if err != nil || !ok {
		return "", err
	}
	return rec.Path, nil
}
