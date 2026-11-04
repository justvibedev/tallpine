/* Split out of main.ts (B297, P52). */
declare const MAIN_WINDOW_VITE_DEV_SERVER_URL: string | undefined;
declare const MAIN_WINDOW_VITE_NAME: string;
declare const __TALLPINE_TEAM_ID__: string | undefined;
declare const __TALLPINE_BUILD_INFO__: { version: string; commit: string; date: string; dirty: string } | undefined;
import {
app,
clipboard,
dialog,
nativeImage,
shell,
type OpenDialogOptions
} from "electron";
import { mkdir, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import {
parseChatDraftBoundaryKinds,
parseChatDraftDialogCopy,
SET_CHAT_DRAFT_RISK_CHANNEL
} from "../../shared/chat-draft-risk";
import { assertDroppedFileSize } from "../../shared/dropped-file";
import {
QUIT_CONFIRM_RESULT_CHANNEL,
SET_CLOSE_SHELL_TERMINAL_SHORTCUT_ENABLED_CHANNEL,
SET_TERMINAL_FOCUSED_CHANNEL,
type KeybindingOverrides
} from "../../shared/shortcuts";
import { desktopDataDir } from "../app-bootstrap";
import { ctx } from "../app-context";
import { daemonAuth, getShellWebContents, ipcMain, quitConfirm, RENDERER_ORIGIN } from "../app-runtime-state";
import {
BROWSER_RUNTIME_DISCONNECTED
} from "../browser-runtime-link";
import { reconnectBrowserRuntimeLink } from "../browser-runtime-link-host";
import {
type BrowserRuntimeState
} from "../browser-view-host";
import { registerChatImageIpc } from "../chat-image-ipc";
import { registerCliLinkIpc } from "../cli-link-ipc";
import { probeDaemonTcp } from "../daemon-tcp-probe";
import { runFilePath } from "../daemon/daemon-lifecycle";
import { openAllowedAppExternalURL } from "../external-open";
import { readManagedFirstRunAccepted } from "../first-run-policy";
import { writeKeybindingOverrides } from "../keybinding-settings";
import { startRemoteProxy } from "../remote-proxy/proxy";
import { RemoteRegistry } from "../remote-registry";
import { createLocalHostIdentity } from "../remote-request";
import { createPasswordExchange } from "../remote-token";
import { computerName, registerRemotesIpc, remotesFilePath } from "../remotes-main";
import {
coerceUiSettings,
DEFAULT_UI_SETTINGS,
readUiSettings,
writeUiSettings,
type UiSettings,
} from "../ui-settings";
import { syncMacWindowButtons } from "../window-chrome";
import { writeClipboardImage } from "../window/renderer-assets";


export async function chooseDirectory(title: string, defaultPath?: string): Promise<string | null> {
	if (defaultPath) await mkdir(defaultPath, { recursive: true });
	const options: OpenDialogOptions = {
		defaultPath,
		properties: ["openDirectory", "createDirectory"],
		title,
	};
	// On Windows, parenting the common file dialog forces a repaint of the main
	// window while Explorer initializes, which produces a visible white flash.
	// The unparented native dialog remains foregrounded by Electron without that
	// compositor handoff.
	const result = await dialog.showOpenDialog(options);

	if (result.canceled) return null;
	return result.filePaths[0] ?? null;
}


export const remoteRegistry = new RemoteRegistry((entry) => {
	// Node reports "null" for the custom app:// origin; only Vite's HTTP URL
	// needs parsing. Never reflect an arbitrary request Origin here.
	const devUrl = typeof MAIN_WINDOW_VITE_DEV_SERVER_URL === "undefined" ? undefined : MAIN_WINDOW_VITE_DEV_SERVER_URL;
	return startRemoteProxy(entry, devUrl ? new URL(devUrl).origin : RENDERER_ORIGIN);
});

export function registerAppIpc(): void {


	registerCliLinkIpc({
		ipcMain,
		isPackaged: () => app.isPackaged,
		resourcesPath: process.resourcesPath,
		appImage: process.env.APPIMAGE,
		dataDir: desktopDataDir,
		appVersion: app.getVersion(),
		shellPath: () => ctx.cachedShellEnv?.PATH ?? process.env.PATH,
	});

	ipcMain.handle("app:getVersion", () => app.getVersion());

	ipcMain.handle("app:daemonTcpOpen", () => probeDaemonTcp(daemonAuth.port()));

	ipcMain.handle("app:openExternal", async (_event, url: string) => {
		await openAllowedAppExternalURL(url, shell);
	});


	ipcMain.handle("window:getZoomFactor", () => {
		const shell = getShellWebContents();
		if (process.platform === "darwin" && ctx.mainWindow && shell) syncMacWindowButtons(ctx.mainWindow, shell);
		return shell?.getZoomFactor() ?? 1;
	});

	ipcMain.handle("window:isFullScreen", () => ctx.mainWindow?.isFullScreen() ?? false);

	ipcMain.handle("window:isMaximized", () => ctx.mainWindow?.isMaximized() ?? false);


	// Renderer calls this when focus lands on real shell UI (not the titlebar menu), so menu:action's panel fallback below doesn't go stale.
	ipcMain.on("shell:focus", () => ctx.browserViewHost?.forgetLastFocusedPanel());


	ipcMain.handle("browser:runtime:reconnect", (event) => {
			if (event.sender !== getShellWebContents()) throw new Error("Untrusted browser runtime request.");
			reconnectBrowserRuntimeLink();
	});


	ipcMain.handle("browser:runtime:state", (event): BrowserRuntimeState => {
		if (event.sender !== getShellWebContents()) throw new Error("Untrusted browser runtime request.");
		return ctx.browserRuntimeLink?.state ?? BROWSER_RUNTIME_DISCONNECTED;
	});


	ipcMain.on("browser:overlay", (event, open: unknown) => {
		if (event.sender !== getShellWebContents() || typeof open !== "boolean") return;
		ctx.windowComposition?.setOverlayOpen(open);
		// Refresh the live page's surface only on macOS: refreshLastFocusedPanelSurface
		// is a macOS-specific compositor workaround (its own docstring says so). On
		// Windows, hiding/restoring the native view under the raised shell causes a
		// brief black flash, and window-composition.ts gates its equivalent nudge to
		// darwin for the same reason.
		if (open && process.platform === "darwin") {
			ctx.browserViewHost?.refreshLastFocusedPanelSurface();
		}
	});


	ipcMain.on(SET_CLOSE_SHELL_TERMINAL_SHORTCUT_ENABLED_CHANNEL, (_event, enabled: unknown) => {
		ctx.closeShellTerminalShortcutEnabled = enabled === true;
	});


	ipcMain.on(QUIT_CONFIRM_RESULT_CHANNEL, (event, confirmed: unknown) => {
		if (event.sender !== getShellWebContents() || typeof confirmed !== "boolean") return;
		quitConfirm.resolve(confirmed);
	});


	ipcMain.on(SET_TERMINAL_FOCUSED_CHANNEL, (event, focused: unknown, handlesFontSize: unknown) => {
		if (event.sender !== getShellWebContents() || typeof focused !== "boolean") return;
		ctx.terminalFocused = focused;
		ctx.terminalFontSizeFocused = focused && handlesFontSize === true;
	});


	ipcMain.on(SET_CHAT_DRAFT_RISK_CHANNEL, (event, risks: unknown, dialogCopy: unknown) => {
		if (event.sender !== getShellWebContents()) return;
		const parsed = parseChatDraftBoundaryKinds(risks);
		const parsedCopy = parseChatDraftDialogCopy(dialogCopy);
		if (!parsed || (parsed.length > 0 && !parsedCopy)) return;
		if (
			ctx.chatDraftRisks.length === parsed.length &&
			ctx.chatDraftRisks.every((risk, index) => risk === parsed[index]) &&
			JSON.stringify(ctx.chatDraftDialog) === JSON.stringify(parsedCopy)
		) return;
		ctx.chatDraftRisks = [...parsed];
		ctx.chatDraftDialog = parsedCopy;
		ctx.chatDraftQuitConfirmed = false;
		ctx.chatDraftWindowCloseConfirmed = false;
	});

	registerRemotesIpc(ipcMain, {
		file: remotesFilePath(),
		registry: remoteRegistry,
		localIdentity: createLocalHostIdentity(() => ctx.daemonStatus.state === "ready" && ctx.daemonStatus.port
			? { port: ctx.daemonStatus.port, pid: ctx.daemonStatus.pid, generation: ctx.daemonAttachmentGeneration }
			: null),
		exchange: createPasswordExchange(fetch, () => computerName()),
	});


	ipcMain.handle("app:chooseDirectory", async (_event, input?: string | { title?: string; defaultPath?: string }) => {
		const title = typeof input === "string"
			? input
			: input?.title;
		const defaultPath = typeof input === "object" && input !== null && typeof input.defaultPath === "string"
			? input.defaultPath.trim()
			: "";
		return chooseDirectory(title?.trim() || "Choose a git repository", defaultPath === "~/tallpine/projects" ? path.join(os.homedir(), "tallpine", "projects") : undefined);
	});

	ipcMain.handle("clipboard:writeText", async (_event, text: string) => {
		// Electron 44: writeText is async and the Linux selection clipboard moved
		// to the clipboard.selection sub-namespace.
		await clipboard.writeText(text);
		if (process.platform === "linux") {
			await clipboard.selection.writeText(text);
		}
	});

	ipcMain.handle("clipboard:readText", () => clipboard.readText());

	registerChatImageIpc({
		ipcMain,
		clipboard: { writeImage: writeClipboardImage },
		createImage: (bytes) => nativeImage.createFromBuffer(bytes),
		dialog,
		downloadsDirectory: () => app.getPath("downloads"),
	});


	// A file dropped onto the terminal is delivered as raw bytes (its original path
	// is unavailable to the sandboxed renderer on macOS — see webUtils.getPathForFile
	// regressions in Electron 30-33). Stash the bytes under the app's own state dir
	// and return the path so the terminal can insert it, mirroring how a native
	// terminal inserts a dropped file's path.
	ipcMain.handle("terminal:saveDroppedFile", async (_event, input: { name: string; bytes: Uint8Array }) => {
		assertDroppedFileSize(input?.bytes);
		const dir = path.join(app.getPath("userData"), "terminal-drops");
		await mkdir(dir, { recursive: true });
		const base = path.basename(input.name || "").replace(/[^\w.-]+/g, "_") || "dropped";
		const target = path.join(dir, `${Date.now()}-${base}`);
		await writeFile(target, Buffer.from(input.bytes));
		return target;
	});


	ipcMain.handle("uiSettings:get", async (): Promise<UiSettings> => {
		const runFile = runFilePath();
		const managedAccepted = await readManagedFirstRunAccepted();
		const settings = runFile ? await readUiSettings(path.dirname(runFile)) : { ...DEFAULT_UI_SETTINGS };
		// IT pre-acceptance is applied on read, never written to the user's file.
		return managedAccepted ? { ...settings, firstRunNoticeAccepted: true } : settings;
	});

		ipcMain.handle("uiSettings:set", async (_event, settings: Partial<UiSettings>): Promise<UiSettings> => {
			const runFile = runFilePath();
		const result = !runFile ? coerceUiSettings(settings) : await writeUiSettings(path.dirname(runFile), settings);
		ctx.trayController?.setLocale(result.locale);
		ctx.soundNotificationsEnabled = result.soundNotificationsEnabled;
		ctx.terminalShellPreference = result.terminalShell;
		ctx.shellEnvPromise = null;
		ctx.cachedShellEnv = null;
		return result;
		});


	ipcMain.handle("keybindings:get", (): KeybindingOverrides => ctx.keybindingOverrides);

	ipcMain.handle("keybindings:set", async (_event, overrides: KeybindingOverrides): Promise<KeybindingOverrides> => {
		const runFile = runFilePath();
		if (!runFile) return ctx.keybindingOverrides;
		ctx.keybindingOverrides = await writeKeybindingOverrides(path.dirname(runFile), overrides);
		return ctx.keybindingOverrides;
	});

	ipcMain.handle("keybindings:setRecording", (event, active: unknown): void => {
		if (event.sender !== getShellWebContents() || typeof active !== "boolean") return;
		ctx.keybindingRecordingActive = active;
	});
}
