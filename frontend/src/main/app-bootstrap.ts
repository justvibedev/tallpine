/* Split out of main.ts (B297, P52). */
declare const MAIN_WINDOW_VITE_DEV_SERVER_URL: string | undefined;
declare const MAIN_WINDOW_VITE_NAME: string;
declare const __TALLPINE_TEAM_ID__: string | undefined;
declare const __TALLPINE_BUILD_INFO__: { version: string; commit: string; date: string; dirty: string } | undefined;
import "./debug-switch-guard-startup"; // first: a packaged build refuses debugging switches before anything else runs
import {
app
} from "electron";
import { execFile } from "node:child_process";
import os from "node:os";
import { promisify } from "node:util";
import { BUNDLE_ID, PRODUCT_NAME } from "../shared/brand";
import { resolveDesktopDataDir } from "./desktop-data-dir";
import { DEV_REAL_DATA_ENV, isolateDevSessionEnv } from "./dev-session-isolation";
import { resolveUserDataDir } from "./user-data-path";



export const execFileAsync = promisify(execFile);


// Windows GUI launches (e.g. from a Start-menu/desktop shortcut) have no attached
// console, so process.stdout and process.stderr are dead pipes. The daemon-output
// console.log/console.error calls
// below then fail with EPIPE, and with no "error" listener that surfaces as an
// uncaught exception that crashes the main process. Swallow broken-pipe write
// errors on the std streams: a dropped log line is harmless, the crash is not.
export const ignoreStdStreamError = (err: NodeJS.ErrnoException): void => {
	if (err.code === "EPIPE") return;
};

process.stdout.on("error", ignoreStdStreamError);

process.stderr.on("error", ignoreStdStreamError);


// The name shown in window titles, the menu bar, the dock, and the About panel.
// Packaged bundle and installer filenames are unchanged.
export const APP_DISPLAY_NAME = PRODUCT_NAME;


// Must run before app ready so the About panel and default-menu role labels use it.
// Unpackaged runs get a distinct name so the dev window, dock menu, and About
// panel never impersonate the installed app (#3642).
// First, before anything reads TALLPINE_DATA_DIR/TALLPINE_RUN_FILE/TALLPINE_PORT: a dev build
// started inside an agent session must not run on the production state that
// session inherited.
export const devSessionIsolation = isolateDevSessionEnv(process.env, {
	isPackaged: app.isPackaged,
	homeDir: os.homedir(),
	checkoutDir: app.getAppPath(),
});

if (devSessionIsolation) {
	console.warn(
		`[dev] started inside agent session ${devSessionIsolation.sessionId}: using isolated dev state ${devSessionIsolation.stateDir} on port ${devSessionIsolation.port} (set ${DEV_REAL_DATA_ENV}=1 to keep the inherited environment)`,
	);
}

app.setName(app.isPackaged ? APP_DISPLAY_NAME : `${APP_DISPLAY_NAME} (dev)`);

// The native macOS About panel (menu role "about") shows the build commit too.
app.setAboutPanelOptions({ applicationVersion: app.getVersion(), credits: aboutDetail() });


// An unsigned dev Electron is a new app to macOS whenever its binary changes, so Chromium's
// keychain-held cookie key prompted on start. Dev runs use the stand-in key; packaged keep the keychain.
if (!app.isPackaged && process.platform === "darwin") {
	app.commandLine.appendSwitch("use-mock-keychain");
}


// Windows shows native toasts only when the app declares an AppUserModelID that
// matches its installer shortcut (the NSIS maker's appId). Without it,
// Notification.isSupported() still returns true but show() silently drops the
// toast, so notifications never appear. No-op on macOS/Linux.
if (process.platform === "win32") {
	app.setAppUserModelId(BUNDLE_ID);
}


// Escape hatch for hosts whose GPU driver stack crashes Chromium on startup
// (Linux/Wayland in practice, #3961). Chromium still runs a GPU process; what
// this drops is the vendor driver, falling the process back to Electron's
// bundled SwiftShader. There is no window yet when the crashes happen, so an
// in-app setting is unreachable and an env var is the only knob that works.
// Truthy allowlist mirrors keepDaemonAlive() so "off"/"no" cannot accidentally
// turn the GPU off. Must run before app ready — the call throws after that.
export const disableGpu = process.env.TALLPINE_DISABLE_GPU?.trim().toLowerCase();

if (disableGpu === "1" || disableGpu === "true" || disableGpu === "yes" || disableGpu === "on") {
	app.disableHardwareAcceleration();
}


// Pin ALL Electron-owned state (Chromium cache, cookies, local/session storage,
// crash dumps) under the canonical Tallpine home at ~/.tallpine instead of Electron's macOS
// default ~/Library/Application Support/<name>. Keeps the app's entire footprint
// inside ~/.tallpine alongside the daemon's data dir and running.json. sessionData and
// crashDumps derive from userData, so this one override reparents them all.
// Must run before app ready.
// Dev runs get their own profile under the same ~/.tallpine root: the packaged app
// keeps this directory open, and two Chromium instances sharing one profile
// corrupt its LevelDB stores. Mirrors how dev already isolates running.json and
// the daemon data dir into ~/.tallpine/dev.
// TALLPINE_DEV_ELECTRON_DIR overrides the dev profile location. The default dev path
// is shared by every checkout, and Chromium puts a singleton lock in a profile,
// so a second worktree's `npm run dev` loses requestSingleInstanceLock() and
// exits immediately. That is a real constraint for a tool built around parallel
// worktree sessions: two sessions could not both run the app. Packaged builds
// are deliberately NOT overridable — their profile is part of the install.
app.setPath(
	"userData",
	resolveUserDataDir({ isPackaged: app.isPackaged, home: os.homedir(), devOverride: process.env.TALLPINE_DEV_ELECTRON_DIR }),
);


// Resolve once against the launch cwd, before the daemon can chdir. The exact
// absolute value is shared by policy bootstrap and every daemon spawn.
export const desktopLaunchWorkingDirectory = process.cwd();

export const desktopDataDir = resolveDesktopDataDir(process.env, os.homedir(), desktopLaunchWorkingDirectory, app.isPackaged);


export const isDev = !app.isPackaged;


// Dev mode uses a separate port and state subdirectory so it never collides with
// a concurrently running installed-app daemon. The subdir also isolates supervise.sock
// on Unix (backend derives it as dir(RunFilePath)/supervise.sock) and the named pipe
// on Windows (supervisorPipeFromRunFile derives it from the same dir basename).
export const DEV_STATE_SUBDIR = "dev";
 // ~/.tallpine/dev/

export const RENDERER_SCHEME = "app";

export function aboutDetail(): string {
	const lines = [`Version ${app.getVersion()}`];
	const info = typeof __TALLPINE_BUILD_INFO__ === "undefined" ? undefined : __TALLPINE_BUILD_INFO__;
	if (info?.commit) lines.push(`Commit ${info.commit}${info.dirty === "true" ? " (dirty)" : ""}`);
	if (info?.date) lines.push(`Built ${info.date}`);
	return lines.join("\n");
}
