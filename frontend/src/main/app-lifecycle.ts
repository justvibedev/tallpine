/* Split out of main.ts (B297, P52). */
declare const MAIN_WINDOW_VITE_DEV_SERVER_URL: string | undefined;
declare const MAIN_WINDOW_VITE_NAME: string;
declare const __TALLPINE_TEAM_ID__: string | undefined;
declare const __TALLPINE_BUILD_INFO__: { version: string; commit: string; date: string; dirty: string } | undefined;
import {
app,
BaseWindow,
dialog,
powerMonitor,
session,
shell
} from "electron";
import os from "node:os";
import path from "node:path";
import { APP_DISPLAY_NAME } from "./app-bootstrap";
import { killDaemon } from "./daemon/daemon-process";
import { ctx } from "./app-context";
import { daemonAuth, daemonSocketFile, daemonToken, getShellWebContents, ipcMain, OPEN_FOLDER_PATH_CHANNEL, SHELL_ORIGIN_SET, trayLifecycle } from "./app-runtime-state";
import { writeAppStateMarker } from "./app-state";
import { initAppUpdates } from "./app-updates";
import { disposeBrowserRuntimeLink, disposeBrowserStreamLink } from "./browser-runtime-link-host";
import { appRunId } from "./bundled-runtime-staging";
import {
confirmUnsafeChatDraftLeave
} from "./chat-draft-unload";
import { rewriteCookiesForEncryption } from "./cookie-encryption-migration";
import { keepDaemonAlive } from "./daemon-owner";
import { installDaemonProtocol } from "./daemon-protocol";
import { shouldShutdownDaemonForUpdate, shutdownDaemonForUpdate } from "./daemon-update-shutdown";
import { cancelDaemonAutoRestart } from "./daemon/daemon-auto-restart-host";
import { runFilePath, startDaemon, stopDaemon } from "./daemon/daemon-lifecycle";
import { remoteRegistry } from "./ipc/app-ipc";
import { refreshGitHubOwners } from "./ipc/github-ipc";
import { readKeybindingOverrides } from "./keybinding-settings";
import { removeStaleAppSupportDir } from "./legacy-app-support-cleanup";
import { parseOpenFolderPathArg } from "./open-folder-arg";
import { requestStopAgents, shouldStopAgentsOnQuit } from "./quit-agents";
import {
decideRelocation,
inspectInstalledBundle,
installedBundlePath,
isDevOrTestLaunch,
} from "./relocation";
import { installShellPermissionPolicy } from "./shell-permissions";
import { cleanupTemporaryBrowserPartitions } from "./temporary-partition-cleanup";
import { createTrayController } from "./tray";
import { isTrayEnabled } from "./tray-lifecycle";
import {
DEFAULT_UI_SETTINGS,
readUiSettings
} from "./ui-settings";
import { disposeAllBrowserViewHosts } from "./window/browser-view-hosts";
import { createWindow } from "./window/main-window";
import { focusMainWindow } from "./window/window-focus";
import { applyRuntimeAppIcon, registerRendererProtocol } from "./window/renderer-assets";



// Resolve the bundle path `tallpine start` will later `open` and stat as a usable app.
// On macOS process.execPath is .../Tallpine.app/Contents/MacOS/<exe>;
// the thing `tallpine start` opens is the enclosing `.app` directory, so walk up three
// levels (MacOS -> Contents -> .app). app.getAppPath() is WRONG here: it returns
// the app.asar archive path inside the bundle, not the bundle itself.
// On win32/linux there is no .app wrapper, so record execPath; a richer
// resolveApp() for those platforms lands in T6/T7.
export function resolveBundlePath(): string {
	if (process.platform === "darwin") {
		return path.resolve(process.execPath, "..", "..", "..");
	}
	return process.execPath;
}


// `tallpine start` opens the app with `--installed-via=<value>` so the app can record
// how it arrived on first marker creation. Parse it out of argv; absent => the
// marker defaults installSource to "unknown".
export function parseInstalledVia(argv: string[]): string | undefined {
	const flag = argv.find((a) => a.startsWith("--installed-via="));
	return flag ? flag.slice("--installed-via=".length) : undefined;
}


// Write ~/.tallpine/app-state.json so `tallpine start`'s resolveApp() can find this bundle
// (spec §7.1). The app is the sole writer (invariant 3) and writes every launch.
// A failure here must NOT block startup, so the caller wraps this in try/catch;
// we still surface it via the log.
export async function writeAppStateOnLaunch(): Promise<void> {
	// Reuse the same ~/.tallpine resolution as running.json; the marker lives beside it
	// (the Go side computes its dir as dirname(RunFilePath)). runFilePath() returns
	// null only when the home dir is unresolvable, in which case we cannot place
	// the marker; the caller's try/catch logs it.
	const runFile = runFilePath();
	if (!runFile) {
		throw new Error("cannot resolve ~/.tallpine run-file path; skipping app-state marker");
	}
	const stateDir = path.dirname(runFile);
	await writeAppStateMarker({
		stateDir,
		updateRestartProtocol: 1,
		appPath: resolveBundlePath(),
		version: app.getVersion(),
		installedVia: parseInstalledVia(process.argv),
		now: () => new Date(),
	});
}


export const DAEMON_QUIT_STOP_TIMEOUT_MS = 3_000;

// A daemon stopping its agents exits by itself once every agent tree is down.
export const DAEMON_AGENT_STOP_WAIT_MS = 15_000;


// Explicit quit stops the daemon this app spawned instead of waiting out the
// supervisor-link grace period. A keep-alive ("persistent") daemon is left alone.
// When the daemon is already stopping its agents, give it time to finish rather
// than signal it mid-way, which would leave agents running without a daemon.
export async function stopAppOwnedDaemonOnQuit(agentsStopping = false): Promise<void> {
	const child = ctx.daemonProcess;
	if (!child || keepDaemonAlive(process.env)) return;
	const waitForExit = (ms: number) => new Promise<boolean>((resolve) => {
		if (child.exitCode !== null || child.signalCode !== null) return resolve(true);
		const timer = setTimeout(() => resolve(false), ms);
		child.once("exit", () => {
			clearTimeout(timer);
			resolve(true);
		});
	});
	if (agentsStopping && await waitForExit(DAEMON_AGENT_STOP_WAIT_MS)) return;
	const exited = waitForExit(DAEMON_QUIT_STOP_TIMEOUT_MS);
	stopDaemon();
	await exited;
}

export function registerAppLifecycle(): void {


	app.on("second-instance", (_event, argv) => {
		// A folder dropped on the taskbar icon/shortcut while already running.
		// Usually the renderer is already mounted and listening, so send directly
		// — but this can still fire while the first instance is early in
		// app.whenReady() or creating its window, before any shell WebContents
		// exists. Fall back to the same pendingFolderPath queue the cold-start
		// path uses rather than silently dropping it.
		const folderPath = parseOpenFolderPathArg(argv);
		if (folderPath) {
			const window = BaseWindow.getAllWindows()[0];
			if (window) {
				if (window.isMinimized()) window.restore();
				window.show();
				window.focus();
			}
			const contents = getShellWebContents();
			if (contents) {
				contents.send(OPEN_FOLDER_PATH_CHANNEL, folderPath);
			} else {
				ctx.pendingFolderPath = folderPath;
			}
			return;
		}
		const window = BaseWindow.getAllWindows()[0];
		if (!window) return;
		if (window.isMinimized()) window.restore();
		window.show();
		window.focus();
	});


	app.whenReady().then(async () => {
		// B176: daemon URLs load over the daemon's user-only socket, not loopback
		// TCP. The shell's session (the default one) gets the token; every other
		// session (browser profiles, render checks) is served without it.
		installDaemonProtocol(session.defaultSession, {
			port: daemonAuth.port,
			socketPath: daemonSocketFile,
			token: daemonAuth.token,
			trustedOrigins: () => SHELL_ORIGIN_SET,
		});
		app.on("session-created", (created) => installDaemonProtocol(created, { port: daemonAuth.port, socketPath: daemonSocketFile }));
		installShellPermissionPolicy(session.defaultSession);
		void refreshGitHubOwners();
		// Leftovers from a crashed or force-quit run; no browser view exists yet.
		void cleanupTemporaryBrowserPartitions(app.getPath("userData")).catch((error) =>
			console.warn("temporary browser partition cleanup failed:", error));
		// An old build or an un-pinned dev run may have left caches in Electron's
		// default location; app state belongs under ~/.tallpine only.
		if (process.platform === "darwin") {
			void removeStaleAppSupportDir(app.getPath("appData")).catch((error) =>
				console.warn("stale Application Support cleanup failed:", error));
		}
		// Rows written before cookie encryption was enabled stay plaintext until rewritten.
		if (app.isPackaged) {
			void rewriteCookiesForEncryption({
				userDataDir: app.getPath("userData"),
				defaultCookies: session.defaultSession.cookies as never,
				partitionCookies: (name) => session.fromPartition(`persist:${name}`).cookies as never,
			}).catch((error) => console.warn("cookie encryption rewrite failed:", error));
		}
		// Capture install provenance BEFORE relocation. moveToApplicationsFolder()
		// relaunches from /Applications WITHOUT forwarding our --installed-via arg, and
		// code past a successful move never runs in this instance, so a post-move-only
		// write would record installSource="unknown" and the sticky logic in
		// writeAppStateMarker would then lock it there forever. Writing now (only when
		// the arg is present, i.e. the npm-bootstrap launch) persists the source so the
		// post-move instance preserves it while refreshing appPath to /Applications.
		if (parseInstalledVia(process.argv)) {
			try {
				await writeAppStateOnLaunch();
			} catch (err) {
				console.error("failed to write pre-relocation app-state marker:", err);
			}
		}

		if (process.platform === "darwin" && app.isPackaged) {
			const bundlePath = resolveBundlePath();
			const action = decideRelocation({
				inApplicationsFolder: app.isInApplicationsFolder(),
				runningVersion: app.getVersion(),
				devOrTestLaunch: isDevOrTestLaunch({
					env: process.env,
					homeDir: os.homedir(),
					bundlePath,
				}),
				...inspectInstalledBundle(bundlePath),
			});
			if (action === "handoff") {
				// A stale copy (the original download, a mounted dmg) launching while a
				// newer build sits in /Applications. Relocating here would trash that
				// build and pin the user to this bundle's version forever, so open the
				// install and quit instead. Return before the marker write below: the
				// instance we just launched records the path and version.
				const installed = installedBundlePath(bundlePath);
				console.info(`newer install at ${installed}; handing off and quitting`);
				await shell.openPath(installed);
				app.quit();
				return;
			}
			if (action === "relocate") {
				try {
					// On success this restarts the app from /Applications, so code past
					// here only runs when no move happened (already there, or declined).
					app.moveToApplicationsFolder();
				} catch (err) {
					console.error("relocation to Applications failed:", err);
				}
			}
		}

		// Refresh the marker post-relocation so appPath records the final bundle path;
		// the sticky installSource preserves the value captured above. A marker-write
		// failure is non-fatal: log and continue so the app still boots.
		try {
			await writeAppStateOnLaunch();
		} catch (err) {
			console.error("failed to write app-state marker:", err);
		}

		// A pre-fix bundle cannot be patched retroactively. After the maintained
		// /Applications build runs, offer to retire older Tallpine copies that can still
		// overwrite it if Finder, Spotlight, or an old Dock tile launches them.
		try {
			const { formatStaleAppCopies, retireStaleMacAppCopies } = await import("./stale-app-copies");
			await retireStaleMacAppCopies({
				platform: process.platform,
				isPackaged: app.isPackaged,
				runningPath: resolveBundlePath(),
				runningVersion: app.getVersion(),
				confirm: async (copies) => {
					const result = await dialog.showMessageBox({
						type: "warning",
						buttons: ["Move old copies to Trash", "Not now"],
						defaultId: 0,
						cancelId: 1,
						title: `Remove old ${APP_DISPLAY_NAME} copies`,
						message: `Old copies of ${APP_DISPLAY_NAME} can replace your updated app.`,
						detail: `${formatStaleAppCopies(copies)}\n\nMove these copies to Trash to prevent another downgrade. Your ${APP_DISPLAY_NAME} projects and sessions will not be removed.`,
						noLink: true,
					});
					return result.response === 0;
				},
				trashItem: (candidate) => shell.trashItem(candidate),
				reportFailures: async (paths) => {
					await dialog.showMessageBox({
						type: "warning",
						buttons: ["OK"],
						defaultId: 0,
						title: "Some old copies could not be removed",
						message: `Move these copies to Trash manually before launching ${APP_DISPLAY_NAME} again.`,
						detail: paths.join("\n"),
						noLink: true,
					});
				},
			});
		} catch (err) {
			console.warn("stale Tallpine copy cleanup failed:", err);
		}

		const keybindingRunFile = runFilePath();
		if (keybindingRunFile) {
			ctx.keybindingOverrides = await readKeybindingOverrides(path.dirname(keybindingRunFile));
		}

		registerRendererProtocol();
		applyRuntimeAppIcon();
		const initialUiSettings = keybindingRunFile
			? await readUiSettings(path.dirname(keybindingRunFile))
			: { ...DEFAULT_UI_SETTINGS };
		ctx.soundNotificationsEnabled = initialUiSettings.soundNotificationsEnabled;
		ctx.terminalShellPreference = initialUiSettings.terminalShell;
		if (ctx.browserQuitRequested) return;
		if (isTrayEnabled(process.platform)) {
			ctx.trayController = createTrayController({
				focusWindow: focusMainWindow,
				openSession: trayLifecycle.openSession,
				quitCompletely: () => app.quit(),
				locale: initialUiSettings.locale,
			});
		}
		if (process.platform === "darwin") {
			powerMonitor.on("shutdown", () => app.quit());
		}
		// Checks GitHub Releases for a newer signed build; inert on dev and local builds.
		const updateRunFile = runFilePath();
		initAppUpdates({
			ipcMain,
			stateDir: updateRunFile ? path.dirname(updateRunFile) : null,
			shell: getShellWebContents,
			onUpdateRestart: () => { ctx.updateRestartRequested = true; },
			// Squirrel rejected the update: a later ordinary quit is not an update restart.
			onUpdateRestartFailed: () => { ctx.updateRestartRequested = false; },
			daemonPort: () => (ctx.daemonStatus.state === "ready" ? ctx.daemonStatus.port ?? null : null),
		});
		// Boot the daemon alongside the window, not after it: the board cannot show
		// until the daemon answers. createWindow links the browser runtime itself
		// when the daemon is already ready by the time the window exists. Windows
		// keeps the old order until this path has been tried there.
		const daemonStartsWithWindow = process.platform !== "win32";
		if (daemonStartsWithWindow) void startDaemon();
		await createWindow();
		if (ctx.browserQuitRequested) return;
		if (!daemonStartsWithWindow) void startDaemon();

		// Windows/Linux: a folder dropped on the taskbar icon/shortcut while the
		// app was not running launches it with the folder's path in argv. The
		// renderer isn't mounted yet — queue it, flushed on TRAY_RENDERER_READY_CHANNEL.
		const folderPathArg = parseOpenFolderPathArg(process.argv);
		if (folderPathArg) {
			ctx.pendingFolderPath = folderPathArg;
		}

		app.on("activate", () => {
			if (ctx.browserQuitRequested) return;
			if (BaseWindow.getAllWindows().length === 0) {
				void createWindow().catch((error) => console.error("failed to recreate main window:", error));
			}
		});
	});


	// Daemon teardown is now handled via the OS-native supervisor socket: the daemon
	// self-stops ~5s after the last client (this process) drops its connection.
	// Quit (menu, Cmd-Q, Dock, osascript, SIGTERM) exits the whole app, tray included;
	// only closing the window leaves Tallpine resident in the menu bar. An app-owned
	// daemon is stopped explicitly before exit (the dropped supervisor socket is the
	// backstop). Sessions survive either way.
	app.on("before-quit", (event) => {
		if (ctx.chatDraftRisks.length > 0 && !ctx.chatDraftQuitConfirmed) {
			event.preventDefault();
			if (confirmUnsafeChatDraftLeave(
				ctx.chatDraftRisks,
				(options) => dialog.showMessageBoxSync(options),
				ctx.chatDraftDialog,
			)) {
				ctx.chatDraftQuitConfirmed = true;
				app.quit();
			}
			return;
		}
		ctx.browserQuitRequested = true;
		// Never respawn the daemon while the app is shutting down.
		cancelDaemonAutoRestart();
		disposeBrowserRuntimeLink();
		disposeBrowserStreamLink();
		trayLifecycle.dispose();
		ctx.trayController = null;
		if (!ctx.browserCleanupComplete) {
			event.preventDefault();
			if (!ctx.browserQuitCleanupPromise) {
				const retireSides = async () => {
					if (ctx.daemonStatus.state !== "ready" || !ctx.daemonStatus.port) return;
					try {
						const response = await fetch(`http://127.0.0.1:${ctx.daemonStatus.port}/api/v1/side-chats/launch/retire`, {
							method: "POST", headers: { "content-type": "application/json" },
							body: JSON.stringify({ appRunId }), signal: AbortSignal.timeout(3000),
						});
						if (!response.ok) console.warn("Tallpine: side chat cleanup on quit failed", response.status);
					} catch (error) { console.warn("Tallpine: side chat cleanup on quit failed", error); }
					ctx.sideLaunchState = undefined;
				};
				// A full quit ends the app-owned daemon; stop its agents first so no
				// claude or node process outlives it. Side chats retire before that.
				const stopAgents = async (): Promise<boolean> => {
					if (!shouldStopAgentsOnQuit({
						ready: ctx.daemonStatus.state === "ready",
						port: ctx.daemonStatus.port,
						linked: ctx.supervisorLink !== null,
						keepAlive: keepDaemonAlive(process.env),
					}) || !ctx.daemonStatus.port) return false;
					if (!(await requestStopAgents(ctx.daemonStatus.port, daemonToken.token()))) {
						console.warn("Tallpine: could not ask the daemon to stop agents on quit");
						return false;
					}
					return true;
				};
				const cleanup = Promise.all([
					retireSides().then(stopAgents),
					disposeAllBrowserViewHosts(),
					remoteRegistry.closeAll(),
				]);
				const finishQuit = () => {
					ctx.browserCleanupComplete = true;
					ctx.browserQuitCleanupPromise = null;
					app.quit();
				};
				// Captured before the app-owned stop clears the state it reads.
				const daemonForUpdate = {
					updateRestart: ctx.updateRestartRequested,
					appOwned: Boolean(ctx.daemonProcess) && !keepDaemonAlive(process.env),
					ready: ctx.daemonStatus.state === "ready",
					port: ctx.daemonStatus.port,
				};
				ctx.browserQuitCleanupPromise = cleanup
					.then(([agentsStopping]) => stopAppOwnedDaemonOnQuit(agentsStopping))
					.then(async () => {
						if (!shouldShutdownDaemonForUpdate(daemonForUpdate) || !daemonForUpdate.port) return;
						if (!(await shutdownDaemonForUpdate(daemonForUpdate.port, daemonToken.token()))) {
							console.warn("Tallpine: could not stop the daemon for the update; the new app may attach to the old daemon");
						}
					})
					.catch((error) => console.warn("Tallpine: quit cleanup failed", error))
					.finally(finishQuit);
			}
			return;
		}
	});


	// SIGTERM/SIGINT/SIGHUP (kill, `osascript quit` fallbacks, terminal close) take
	// the same path as menu Quit so the daemon and browser views are torn down.
	// A second signal while shutting down exits immediately.
	for (const signal of ["SIGTERM", "SIGINT", "SIGHUP"] as const) {
		process.on(signal, () => {
			if (ctx.browserQuitRequested) app.exit(0);
			else {
				// A signal is not interactive: no unsaved-draft dialog to answer.
				ctx.chatDraftQuitConfirmed = true;
				app.quit();
			}
		});
	}


	// Last resort: if the OS-native supervisor link is not actually connected
	// (daemon socket never bound, e.g. UDS path-length limit, or addr was null),
	// the dropped fd will NOT stop the daemon on quit, so kill it here to avoid an
	// orphan. Safe because Phase A made the daemon's SIGTERM non-destructive: it
	// exits without tearing down sessions, which survive for the next boot to adopt.
	// When the link IS connected we do nothing here and rely on the OS closing the
	// fd on exit, which covers crash and SIGKILL uniformly.
	//
	// TALLPINE_KEEP_DAEMON opts out entirely: the daemon is deliberately spawned without a
	// supervisor link so it persists across app quit, so this orphan-cleanup kill
	// must be skipped — otherwise it would defeat the whole point on quit.
	process.on("exit", () => {
		if (ctx.daemonProcess && !ctx.supervisorLink?.connected && !keepDaemonAlive(process.env)) {
			killDaemon(ctx.daemonProcess);
		}
	});


	app.on("window-all-closed", () => {
		if (process.platform !== "darwin") {
			app.quit();
		}
	});
}
