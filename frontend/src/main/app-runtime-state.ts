/* Split out of main.ts (B297, P52). */
declare const MAIN_WINDOW_VITE_DEV_SERVER_URL: string | undefined;
declare const MAIN_WINDOW_VITE_NAME: string;
declare const __TALLPINE_TEAM_ID__: string | undefined;
declare const __TALLPINE_BUILD_INFO__: { version: string; commit: string; date: string; dirty: string } | undefined;
import {
app,
ipcMain as electronIpcMain,
type WebContents
} from "electron";
import {
QUIT_CONFIRM_REQUEST_CHANNEL,
type QuitConfirmRequest
} from "../shared/shortcuts";
import { RENDERER_SCHEME } from "./app-bootstrap";
import { ctx } from "./app-context";
import { createAwakeDownloadBridge } from "./awake-download-bridge";
import { daemonTokenPath, DaemonTokenSource, installDaemonAuthHeaders, withDaemonToken } from "./daemon-auth";
import { daemonSocketPath } from "./daemon-socket-fetch";
import { DaemonWsBridge } from "./daemon-ws-bridge";
import { runFilePath } from "./daemon/daemon-lifecycle";
import { guardIpcMain, originOf } from "./ipc-guard";
import { createQuitConfirmController } from "./quit-confirm";
import { createTrayLifecycle } from "./tray-lifecycle";
import { focusMainWindow } from "./window/window-focus";


export const trustedShellWebContents = new Map<number, WebContents>();

export const trayLifecycle = createTrayLifecycle({
	getWindow: () => null,
	getContents: () => getShellWebContents(),
	getTrayController: () => ctx.trayController,
	focusWindow: () => focusMainWindow(),
});

// Icon/taskbar-shortcut folder drop, mirroring VS Code: relayed to the
// renderer's global drop handling so it opens the same create-project flow.
export const OPEN_FOLDER_PATH_CHANNEL = "app:openFolderPath";

// The daemon's per-install token (daemon.token next to running.json). Main
// attaches it to daemon requests; the renderer never sees it.
export const daemonToken = new DaemonTokenSource(() => {
	const runFile = runFilePath();
	return runFile ? daemonTokenPath(runFile) : null;
});

export const daemonAuth = {
	// Only a live attachment's port: a stale one may now belong to another server.
	port: () => (ctx.daemonStatus.state === "ready" ? ctx.daemonStatus.port : undefined),
	token: () => daemonToken.token(),
	isTrustedWebContents: (id: number) => trustedShellWebContents.has(id),
};

// Every main-process Node fetch to the attached daemon carries the token,
// including helpers that take fetch as an injectable default.
export function daemonSocketFile(): string | null {
	const runFile = runFilePath();
	return runFile ? daemonSocketPath(runFile) : null;
}

// The shell's /mux and event-stream WebSockets over the daemon socket (B176).
export const daemonWsBridge = new DaemonWsBridge({ port: daemonAuth.port, token: daemonAuth.token, socketPath: daemonSocketFile });

export const daemonAuthSessions = new WeakSet<Electron.Session>();

export function ensureDaemonAuthHeaders(target: Electron.Session): void {
	if (daemonAuthSessions.has(target)) return;
	daemonAuthSessions.add(target);
	installDaemonAuthHeaders(target, daemonAuth);
}

export const awakeDownloads = createAwakeDownloadBridge({
 baseURL: () => ctx.daemonStatus.state === "ready" && ctx.daemonStatus.port ? `http://127.0.0.1:${ctx.daemonStatus.port}` : null,
 // Electron net.fetch adds browser metadata without Origin, which the daemon rejects.
 request: (url, init) => fetch(url, init),
});

export const browserCleanupPromises = new Set<Promise<void>>();

// macOS app menu Quit. ⌘Q asks the shell to confirm; with no visible window
// there is nowhere to show the dialog, so the shortcut quits directly.
export const quitConfirm = createQuitConfirmController({
	canConfirm: () => {
		const shell = getShellWebContents();
		return Boolean(ctx.mainWindow?.isVisible() && !ctx.mainWindow.isMinimized() && shell && !shell.isDestroyed());
	},
	requestConfirm: () => {
		const shell = getShellWebContents();
		shell?.focus();
		// Quit always exits the app (the tray goes with it); closing the window
		// is what leaves Tallpine in the menu bar.
		const request: QuitConfirmRequest = { keepsRunning: false };
		shell?.send(QUIT_CONFIRM_REQUEST_CHANNEL, request);
	},
	quit: () => app.quit(),
});

export const RENDERER_HOST = "renderer";

export const RENDERER_ORIGIN = `${RENDERER_SCHEME}://${RENDERER_HOST}`;

export const NATIVE_WINDOW_BACKGROUND_DARK = "#0c0c0e";

export const NATIVE_WINDOW_BACKGROUND_LIGHT = "#fbfbfb";


export function getShellWebContents(): WebContents | null {
	return ctx.windowComposition?.shellWebContents ?? null;
}


// Every ipcMain registration goes through this guard: only the shell view on
// the app origin (or the dev server) may call; see main/ipc-guard.ts.
// The shell renderer's origins: the app scheme, or the dev server.
export const SHELL_ORIGINS: readonly string[] = [
	RENDERER_ORIGIN,
	...(typeof MAIN_WINDOW_VITE_DEV_SERVER_URL !== "undefined" && MAIN_WINDOW_VITE_DEV_SERVER_URL
		? [originOf(MAIN_WINDOW_VITE_DEV_SERVER_URL) ?? ""]
		: []),
];

export const SHELL_ORIGIN_SET: ReadonlySet<string> = new Set(SHELL_ORIGINS);


// Every ipcMain registration goes through this guard: only the shell view on
// the app origin (or the dev server) may call; see main/ipc-guard.ts.
export const ipcMain = guardIpcMain(electronIpcMain, {
	getShellWebContents,
	allowedOrigins: SHELL_ORIGINS,
});

export function registerAppRuntimeState(): void {

	globalThis.fetch = withDaemonToken(globalThis.fetch.bind(globalThis), {
		...daemonAuth,
		socketPath: daemonSocketFile,
	});

	app.once("will-quit", () => awakeDownloads.dispose());
}
