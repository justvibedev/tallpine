declare const MAIN_WINDOW_VITE_DEV_SERVER_URL: string | undefined;
import {
app,
BaseWindow,
dialog,
Menu,
nativeTheme,
protocol,
session,
shell,
WebContentsView
} from "electron";
import os from "node:os";
import path from "node:path";
import { isAgentPageUrl } from "../../shared/agent-page-url";
import { HOME_DIR_NAME } from "../../shared/brand";
import type { BrowserProfileMenuInput } from "../../shared/browser-profiles";
import { AgentBrowserRuntime } from "../agent-browser-runtime/runtime";
import { createAgentLoginsConfirm } from "../agent-logins-confirm";
import { APP_DISPLAY_NAME, desktopDataDir, isDev, RENDERER_SCHEME } from "../app-bootstrap";
import { ctx } from "../app-context";
import { toggleAppDevTools } from "../app-devtools";
import { awakeDownloads, ensureDaemonAuthHeaders, getShellWebContents, ipcMain, NATIVE_WINDOW_BACKGROUND_DARK, NATIVE_WINDOW_BACKGROUND_LIGHT, quitConfirm, trayLifecycle, trustedShellWebContents } from "../app-runtime-state";
import { attachAppShortcuts } from "../app-shortcuts";
import { createBrowserCloseDiagnostics } from "../browser-close-diagnostics";
import { createBrowserDownloadManager } from "../browser-download-manager";
import { BrowserHistoryStore } from "../browser-history-store";
import { BrowserProfileImportService } from "../browser-profile-import/service";
import {
registerBrowserProfileIpc,
type BrowserProfileMenuItem
} from "../browser-profile-ipc";
import { createBrowserProfileStore } from "../browser-profile-store";
import { disposeBrowserRuntimeLink, establishBrowserRuntimeLink, establishBrowserStreamLink } from "../browser-runtime-link-host";
import {
createBrowserViewHost,
shouldHandleAppShortcutInBrowserContext
} from "../browser-view-host";
import { attachProcessRecovery } from "./process-recovery";
import { createProcessRecoveryLog } from "./process-recovery-log";
import { helperSignatureOptions } from "../bundled-runtime-staging";
import {
confirmUnsafeChatDraftLeave,
shouldPreventUnsafeChatDraftClose,
} from "../chat-draft-unload";
import { isAllowedAppExternalURL } from "../external-open";
import { openInSystemBrowser } from "../external-open-runtime";
import { verifyHelperSignature } from "../helper-signature";
import { remoteRegistry } from "../ipc/app-ipc";
import { cancelDockBounce } from "../ipc/notifications-ipc";
import { buildMacAppMenuTemplate } from "../menu";
import { blocksRenderFrameNavigation } from "../render-frame-guard";
import { agentPageRequestAllowed } from "../render-frame-network";
import { syncMacWindowButtons } from "../window-chrome";
import { createWindowComposition } from "../window-composition";
import { buildLinuxAppMenu, buildWindowsAppMenu } from "./app-menu";
import { browserProfileStateDir, clearElectronBrowserProfileData, disposeAllBrowserViewHosts, disposeBrowserViewHost } from "./browser-view-hosts";
import { annotatePreloadPath, preloadPath, rendererUrl, writeClipboardImage } from "./renderer-assets";
import { showWhenRendererLoaded } from "./window-focus";
import { mainWindowOptions } from "./window-options";
declare const MAIN_WINDOW_VITE_NAME: string;
declare const __TALLPINE_TEAM_ID__: string | undefined;
declare const __TALLPINE_BUILD_INFO__: { version: string; commit: string; date: string; dirty: string } | undefined;

export function syncNativeWindowBackground(): void {
	if (!ctx.windowComposition || !ctx.mainWindow || ctx.mainWindow.isDestroyed()) return;
	// Keep vibrancy and the native background in sync after window/renderer
	// lifecycle changes, as well as theme updates.
	if (process.platform === "darwin") {
		ctx.mainWindow.setVibrancy(ctx.windowTranslucent ? "under-window" : null);
	}
	if (ctx.windowTranslucent) {
		ctx.mainWindow.setBackgroundColor("#00000000");
		return;
	}
	ctx.mainWindow.setBackgroundColor(
		ctx.rendererWindowBackground ??
			(nativeTheme.shouldUseDarkColors ? NATIVE_WINDOW_BACKGROUND_DARK : NATIVE_WINDOW_BACKGROUND_LIGHT),
	);
}


export async function createWindowInternal(): Promise<void> {
	await disposeAllBrowserViewHosts();
	const agentBrowserPath = resolveAgentBrowserBinaryPath();
	if (app.isPackaged && !process.env.TALLPINE_AGENT_BROWSER_PATH?.trim()) {
		const sig = await verifyHelperSignature(agentBrowserPath, helperSignatureOptions());
		if (sig.status === "failed") {
			dialog.showErrorBox(
				`${APP_DISPLAY_NAME} helper failed its code-signature check`,
				`${agentBrowserPath} failed verification (${sig.reason}). Reinstall ${APP_DISPLAY_NAME} into /Applications or ~/Applications and relaunch.`,
			);
			app.quit();
			return;
		}
	}
	const agentBrowserRuntime = new AgentBrowserRuntime({
		binaryPath: agentBrowserPath,
		// Agent Browser creates Unix sockets below each run root. Keep this base
		// deliberately short so the namespace/session suffix stays below macOS's
		// 103-byte sockaddr_un limit; all Tallpine state remains under ~/.tallpine.
		dataDir: path.join(os.homedir(), HOME_DIR_NAME, ...(app.isPackaged ? ["br"] : ["dev", "br"])),
		log: (message) => console.log(`Tallpine: ${message}`),
	});
	await agentBrowserRuntime.prepare();
	if (ctx.browserQuitRequested) {
		await agentBrowserRuntime.dispose();
		return;
	}
	const browserProfileStore = await createBrowserProfileStore({ stateDir: browserProfileStateDir() });
	const browserHistoryStore = new BrowserHistoryStore({ stateDir: browserProfileStateDir() });
	const profileImporter = new BrowserProfileImportService({
		stateDir: browserProfileStateDir(),
		profileStore: browserProfileStore,
		historyStore: browserHistoryStore,
		fromPartition: (partition) => session.fromPartition(partition),
	});
	await profileImporter.initialize();
	const windowOptions = mainWindowOptions();
	ctx.mainWindow = new BaseWindow(windowOptions);
	const composition = createWindowComposition({
		mainWindow: ctx.mainWindow,
		WebContentsView,
		preload: preloadPath(),
		platform: process.platform,
	});
	ctx.windowComposition = composition;
	syncNativeWindowBackground();
	const shellWebContents = getShellWebContents();
	if (!shellWebContents) throw new Error(`${APP_DISPLAY_NAME} shell WebContents was not created`);
	trustedShellWebContents.set(shellWebContents.id, shellWebContents);
	shellWebContents.once("destroyed", () => {
		trustedShellWebContents.delete(shellWebContents.id);
		quitConfirm.reset();
	});
	// A reload drops any open quit dialog, so the next ⌘Q should ask again.
	shellWebContents.on("did-start-loading", () => quitConfirm.reset());

	// On Windows the app paints its own title bar (WindowTitlebar), so the native
	// menu bar is hidden (autoHideMenuBar above). The role-based menu is still
	// installed so its accelerators keep working and act on the focused pane;
	// setMenuBarVisibility(false) keeps the strip itself out of view. macOS gets
	// an explicit menu so DevTools avoids Electron's unsafe built-in role; Linux
	// installs the role-based menu so accelerators and guarded DevTools work,
	// while autoHideMenuBar and setMenuBarVisibility(false) hide the menu strip.
	if (process.platform === "win32") {
		Menu.setApplicationMenu(buildWindowsAppMenu());
		ctx.mainWindow.setMenuBarVisibility(false);
	} else if (process.platform === "darwin") {
		Menu.setApplicationMenu(
			Menu.buildFromTemplate(
				buildMacAppMenuTemplate(() => {
					const fallback = () => getShellWebContents()?.toggleDevTools();
					const host = ctx.browserViewHost;
					if (!host) {
						fallback();
						return;
					}
					void host.toggleDevToolsForLastFocused().then((state) => {
						if (!state) fallback();
					}).catch(fallback);
				}, { appName: app.name, onQuit: quitConfirm.menuQuit }),
			),
		);
	} else if (process.platform === "linux") {
		Menu.setApplicationMenu(buildLinuxAppMenu());
		ctx.mainWindow.setMenuBarVisibility(false);
	}

	// Harden navigation: never let renderer/terminal content open in-app windows or
	// navigate the privileged window away from the app origin. External links go to
	// the OS browser. Keep this in place before exposing any daemon output to the renderer.
	shellWebContents.setWindowOpenHandler(({ url }) => {
		if (isAllowedAppExternalURL(url)) {
			void openInSystemBrowser(url).catch(() => undefined);
		}
		return { action: "deny" };
	});

	shellWebContents.on("will-navigate", (event, url) => {
		if (url !== shellWebContents.getURL()) {
			event.preventDefault();
		}
	});

	shellWebContents.on("will-frame-navigate", (event) => {
		if (event.isMainFrame || !event.frame) return;
		// The app moving its own frame (to the daemon's new port, or to an
		// artifact's inline origin) is not the page navigating away.
		const app = shellWebContents.mainFrame;
		if (event.initiator?.processId === app.processId && event.initiator.routingId === app.routingId) return;
		if (blocksRenderFrameNavigation(event.frame.url, event.url)) event.preventDefault();
	});

	// Agent pages framed in the chat (renders, HTML artifacts) stay off this
	// computer and its network, as the render check's window does: they may
	// load public addresses and their own files. A request is judged by the
	// nearest agent page around its frame; the app's own pass untouched. Pages
	// cannot start workers (their CSP has worker-src 'none'), whose requests
	// carry no frame.
	const blockedPageHosts = new Set<string>();
	// The daemon token rides on the shell's daemon requests (REST, SSE, media,
	// /mux) and main's own net.fetch, never on agent pages' API calls.
	ensureDaemonAuthHeaders(shellWebContents.session);
	ensureDaemonAuthHeaders(session.defaultSession);
	shellWebContents.session.webRequest.onBeforeRequest((details, callback) => {
		// Loading an agent page into a frame is always allowed: the page's own
		// CSP sandboxes it, and it is judged by its own rule from then on.
		if (details.resourceType === "subFrame" && isAgentPageUrl(details.url)) return callback({});
		let pageUrl: string | undefined;
		try {
			for (let frame = details.frame; frame && !pageUrl; frame = frame.parent) {
				if (isAgentPageUrl(frame.url)) pageUrl = frame.url;
			}
		} catch {
			// The frame went away mid-request; nothing of its page is left to load.
		}
		if (!pageUrl) return callback({});
		void agentPageRequestAllowed(details.url, pageUrl).then(
			(allowed) => {
				if (!allowed) {
					const host = URL.canParse(details.url) ? new URL(details.url).host : details.url.slice(0, 80);
					if (blockedPageHosts.size < 100 && !blockedPageHosts.has(host)) {
						blockedPageHosts.add(host);
						console.warn(`Tallpine: blocked an agent page request to ${host}`);
					}
				}
				callback({ cancel: !allowed });
			},
			() => callback({ cancel: true }),
		);
	});

	shellWebContents.on("will-prevent-unload", (event) => {
		if (
			ctx.chatDraftRisks.length > 0 &&
			(ctx.chatDraftQuitConfirmed ||
				ctx.chatDraftWindowCloseConfirmed ||
				confirmUnsafeChatDraftLeave(ctx.chatDraftRisks, (options) => dialog.showMessageBoxSync(options), ctx.chatDraftDialog))
		) {
			// Electron uses preventDefault here to ignore beforeunload and continue
			// leaving.
			event.preventDefault();
			return;
		}
	});

	ctx.mainWindow.on("close", (event) => {
		const preventClose = shouldPreventUnsafeChatDraftClose(
			ctx.chatDraftRisks,
			ctx.chatDraftQuitConfirmed || ctx.chatDraftWindowCloseConfirmed,
			(options) => dialog.showMessageBoxSync(options),
			ctx.chatDraftDialog,
		);
		if (preventClose) {
			event.preventDefault();
			return;
		}
		if (ctx.chatDraftRisks.length > 0) ctx.chatDraftWindowCloseConfirmed = true;
	});

	// Application shortcuts are handled here so they fire no matter which web
	// contents holds focus — the shell renderer, xterm's helper textarea, or a
	// browser-preview view (wired per-view in the browser host).
	const isMac = process.platform === "darwin";
	attachAppShortcuts(
		shellWebContents,
		isMac,
		shellWebContents,
		false,
		() => ctx.keybindingOverrides,
		() => ctx.keybindingRecordingActive,
		(id, chord) =>
			!ctx.browserViewHost?.isLastUsedBrowser() ||
			shouldHandleAppShortcutInBrowserContext(id, chord, isMac),
		(id) => {
			if (id !== "toggle-browser-devtools") return;
			void toggleAppDevTools(ctx.browserViewHost, getShellWebContents);
		},
		() => ctx.terminalFocused,
		() => ctx.terminalFontSizeFocused,
	);

	ctx.browserViewHost = createBrowserViewHost({
		onTabCloseFailure: createBrowserCloseDiagnostics(browserProfileStateDir(), app.getVersion()),
		// Agent tabs: only a connected host's own agent-browser viewer may
		// report the remote page a Browser tab shows.
		isAgentBrowserUrl: (url) => remoteRegistry.isAgentBrowserUrl(url),
		mainWindow: ctx.mainWindow,
		shellWebContents,
		ipcMain,
		shell,
		WebContentsView,
		annotatePreloadPath: annotatePreloadPath(),
		isPackaged: app.isPackaged,
		rendererOrigin: new URL(rendererUrl()).origin,
		isMac,
		getKeybindingOverrides: () => ctx.keybindingOverrides,
		isKeybindingRecording: () => ctx.keybindingRecordingActive,
		agentBrowserRuntime,
		isCloseShellTerminalShortcutEnabled: () => ctx.closeShellTerminalShortcutEnabled,
		browserProfileStore,
		confirmAgentLogins: createAgentLoginsConfirm((options) => dialog.showMessageBox(options)),
		browserHistoryStore,
		browserDownloadManager: createBrowserDownloadManager({
			downloadsDirectory: app.getPath("downloads"),
			historyPath: path.join(desktopDataDir, "browser-downloads.json"),
			shell,
			notify: (state) => { awakeDownloads.update(state); shellWebContents.send("browser:downloadsChanged", state); },
		}),
		clearBrowserProfileData: clearElectronBrowserProfileData,
		clipboard: { writeImage: writeClipboardImage },
		onNativeViewAdded: () => ctx.windowComposition?.restackShell(),
	});
	ctx.browserProfileImporter = profileImporter;
	ctx.browserProfileIpc = registerBrowserProfileIpc({
		ipcMain,
		shellWebContents,
		mainWindow: ctx.mainWindow,
		store: browserProfileStore,
		importer: profileImporter,
		host: ctx.browserViewHost,
		buildMenu: (items: BrowserProfileMenuItem[]) => {
			const menu = Menu.buildFromTemplate(items as Electron.MenuItemConstructorOptions[]);
			return { popup: (options) => menu.popup(options as Electron.PopupOptions) };
		},
		confirmSwitch: async (labels: BrowserProfileMenuInput["labels"]) => {
			const result = await dialog.showMessageBox({
				type: "warning",
				title: labels.switchTitle,
				message: labels.switchMessage,
				detail: labels.switchDetail,
				buttons: [labels.cancel, labels.confirm],
				defaultId: 0,
				cancelId: 0,
			});
			return result.response === 1;
		},
		reportSwitchFailure: (message, labels) => {
			if (!ctx.mainWindow || ctx.mainWindow.isDestroyed()) return;
			void dialog.showMessageBox(ctx.mainWindow, {
				type: "error",
				title: labels.switchTitle,
				message,
			}).catch((error) => console.error("browser profile error dialog failed:", error));
		},
	});
	if (ctx.daemonStatus.state === "ready") {
		establishBrowserRuntimeLink();
		establishBrowserStreamLink();
	}

	showWhenRendererLoaded(ctx.mainWindow, shellWebContents);
	void shellWebContents.loadURL(rendererUrl());

	if (isDev && process.env.TALLPINE_OPEN_DEVTOOLS === "1") {
		shellWebContents.once("did-frame-finish-load", () => {
			shellWebContents.openDevTools({ mode: "detach" });
		});
	}

	const syncWindowChrome = () => {
		if (!ctx.mainWindow || shellWebContents.isDestroyed()) return;
		syncNativeWindowBackground();
		if (process.platform === "darwin") syncMacWindowButtons(ctx.mainWindow, shellWebContents);
		shellWebContents.send("window:zoom", shellWebContents.getZoomFactor());
	};
	// Resize/zoom and fullscreen exit can reset AppKit's button placement.
	ctx.mainWindow.on("resize", syncWindowChrome);
	ctx.mainWindow.on("show", syncWindowChrome);
	ctx.mainWindow.on("restore", syncWindowChrome);
	shellWebContents.on("did-finish-load", syncWindowChrome);
	shellWebContents.on("zoom-changed", () => setTimeout(syncWindowChrome, 0));

	// Native fullscreen removes the traffic-light horizontal reserve, while
	// the renderer keeps the same header height and navigation centerline.
	const pushFullScreen = () => {
		if (!ctx.mainWindow) return;
		syncNativeWindowBackground();
		getShellWebContents()?.send("window:fullscreen", ctx.mainWindow.isFullScreen());
	};
	const pushMaximized = () => {
		if (!ctx.mainWindow) return;
		getShellWebContents()?.send("window:maximized", ctx.mainWindow.isMaximized());
	};
	ctx.mainWindow.on("enter-full-screen", pushFullScreen);
	ctx.mainWindow.on("leave-full-screen", () => {
		syncWindowChrome();
		pushFullScreen();
	});
	ctx.mainWindow.on("maximize", pushMaximized);
	ctx.mainWindow.on("unmaximize", pushMaximized);
	ctx.mainWindow.on("blur", () => {
		ctx.keybindingRecordingActive = false;
	});
	shellWebContents.on("render-process-gone", () => {
		ctx.keybindingRecordingActive = false;
		ctx.terminalFocused = false;
		ctx.terminalFontSizeFocused = false;
	});
	shellWebContents.on("did-start-loading", () => {
		ctx.terminalFocused = false;
		ctx.terminalFontSizeFocused = false;
		trayLifecycle.clear();
	});
	shellWebContents.on("render-process-gone", () => trayLifecycle.clear());
	const detachProcessRecovery = attachProcessRecovery({
		app,
		contents: shellWebContents,
		log: createProcessRecoveryLog(browserProfileStateDir(), app.getVersion()),
		hasUnsavedDrafts: () => ctx.chatDraftRisks.length > 0,
	});

	ctx.mainWindow.on("closed", () => {
		detachProcessRecovery();
		ctx.chatDraftRisks = [];
		ctx.chatDraftDialog = undefined;
		ctx.chatDraftQuitConfirmed = false;
		ctx.chatDraftWindowCloseConfirmed = false;
		disposeBrowserRuntimeLink();
		ctx.keybindingRecordingActive = false;
		if (ctx.windowComposition === composition) ctx.windowComposition = null;
		void disposeBrowserViewHost()
			.finally(() => {
				composition.dispose();
			})
			.catch((error) => {
				console.error("Tallpine: window teardown failed:", error);
			});
		ctx.mainWindow = null;
		// Drop any pending dock bounce with the window it was attached to: its
		// focus listener died with the window, so leaving the id set would make
		// the next bounce skip attaching one to the replacement window.
		cancelDockBounce();
		trayLifecycle.clearPendingTarget();
	});
}

export function createWindow(): Promise<void> {
	if (ctx.createWindowPromise) return ctx.createWindowPromise;
	// A background quit hid the Dock icon; bring it back with the window.
	void app.dock?.show();
	const pending = createWindowInternal();
	ctx.createWindowPromise = pending;
	void pending.then(
		() => {
			if (ctx.createWindowPromise === pending) ctx.createWindowPromise = null;
		},
		() => {
			if (ctx.createWindowPromise === pending) ctx.createWindowPromise = null;
		},
	);
	return pending;
}

export function resolveAgentBrowserBinaryPath(): string {
	const override = process.env.TALLPINE_AGENT_BROWSER_PATH?.trim();
	if (override) return path.resolve(override);
	const binary = process.platform === "win32" ? "agent-browser.exe" : "agent-browser";
	return app.isPackaged
		? path.join(process.resourcesPath, "agent-browser", binary)
		: path.join(app.getAppPath(), "agent-browser", binary);
}

export function registerMainWindow(): void {

	nativeTheme.on("updated", syncNativeWindowBackground);

	// The packaged renderer is served from a custom standard scheme, not file://.
	// A file:// page has the opaque "null" origin, which the daemon must never
	// trust (every sandboxed iframe on any website also presents "null"), so its
	// fetch/EventSource calls to the loopback API would be CORS-blocked.
	// app://renderer is an origin only this app can present, so the daemon's CORS
	// allowlist can name it. A standard scheme also makes the build's absolute
	// asset URLs (/assets/…) and history-API routing resolve, which file:// breaks.
	// codeCache lets V8 keep compiled bytecode for app:// scripts across launches;
	// Chromium only does that for http(s) by default, so without it every launch
	// recompiles the whole renderer bundle. Must run before app ready.
	protocol.registerSchemesAsPrivileged([
		{
			scheme: RENDERER_SCHEME,
			privileges: { standard: true, secure: true, supportFetchAPI: true, codeCache: true },
		},
	]);

	if (!app.requestSingleInstanceLock()) {
		app.exit(0);
	}
}
