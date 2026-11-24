/**
 * Wires Real Chrome mode (B849) into the app: one Chrome process for the app's
 * lifetime, closed on quit (a8), and a host per main window that shares the
 * window's agent-browser runtime and answers the Browser panel's card.
 */
import type { IpcMain, IpcMainInvokeEvent, WebContents } from "electron";
import { ctx } from "../app-context";
import { browserProfileStateDir } from "../window/browser-view-hosts";
import { ChromeProcessManager } from "./chrome-process";
import type { ChromeSessionState } from "../../shared/chrome-mode";
import { ChromeBrowserHost, type ChromeHostRuntime } from "./host";

export const CHROME_STATE_CHANNEL = "browser:chromeState";
export const CHROME_SHOW_CHANNEL = "browser:chromeShow";
export const CHROME_STATE_CHANGED_CHANNEL = "browser:chromeStateChanged";
export const CHROME_SIGN_IN_CHANNEL = "browser:chromeSignIn";
export const CHROME_SIGN_IN_DONE_CHANNEL = "browser:chromeSignInDone";

const log = (message: string) => console.log(`Tallpine: ${message}`);

function chromeProcessManager(): ChromeProcessManager {
	ctx.chromeProcessManager ??= new ChromeProcessManager({ dataDir: browserProfileStateDir(), log });
	return ctx.chromeProcessManager;
}

export type AppChromeBrowser = { host: ChromeBrowserHost; dispose(): Promise<void> };

export function createAppChromeBrowser(options: {
	ipcMain: Pick<IpcMain, "handle" | "removeHandler">;
	shellWebContents: WebContents;
	runtime: ChromeHostRuntime;
}): AppChromeBrowser {
	const { ipcMain, shellWebContents } = options;
	const host = new ChromeBrowserHost({
		process: chromeProcessManager(),
		runtime: options.runtime,
		log,
		onStateChange: (sessionId, state) => {
			if (!shellWebContents.isDestroyed()) shellWebContents.send(CHROME_STATE_CHANGED_CHANNEL, { sessionId, state });
		},
	});
	const fromShell = (event: IpcMainInvokeEvent, sessionId: unknown): sessionId is string =>
		event.sender === shellWebContents && typeof sessionId === "string" && sessionId.trim() !== "";
	ipcMain.handle(CHROME_STATE_CHANNEL, (event, sessionId: unknown): ChromeSessionState =>
		fromShell(event, sessionId) ? host.watch(sessionId) : { running: false, activeTab: null });
	ipcMain.handle(CHROME_SHOW_CHANNEL, (event, sessionId: unknown) =>
		fromShell(event, sessionId) ? host.showWindow(sessionId) : { shown: false });
	ipcMain.handle(CHROME_SIGN_IN_CHANNEL, (event, sessionId: unknown): Promise<ChromeSessionState> | ChromeSessionState =>
		fromShell(event, sessionId) ? host.signIn(sessionId) : { running: false, activeTab: null });
	ipcMain.handle(CHROME_SIGN_IN_DONE_CHANNEL, (event, sessionId: unknown): Promise<ChromeSessionState> | ChromeSessionState =>
		fromShell(event, sessionId) ? host.finishSignIn(sessionId) : { running: false, activeTab: null });
	return {
		host,
		dispose: () => {
			ipcMain.removeHandler(CHROME_STATE_CHANNEL);
			ipcMain.removeHandler(CHROME_SHOW_CHANNEL);
			ipcMain.removeHandler(CHROME_SIGN_IN_CHANNEL);
			ipcMain.removeHandler(CHROME_SIGN_IN_DONE_CHANNEL);
			return host.dispose();
		},
	};
}

/** Closes the Tallpine Chrome (and a sign-in Chrome) on quit; Chrome-mode tabs do not outlive the app. */
export async function closeAppChrome(): Promise<void> {
	await ctx.chromeProcessManager?.closeAll().catch((error: unknown) => console.warn("Tallpine: closing Google Chrome failed", error));
}
