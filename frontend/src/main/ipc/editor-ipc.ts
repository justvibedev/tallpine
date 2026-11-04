/* Split out of main.ts (B297, P52). */
declare const MAIN_WINDOW_VITE_DEV_SERVER_URL: string | undefined;
declare const MAIN_WINDOW_VITE_NAME: string;
declare const __TALLPINE_TEAM_ID__: string | undefined;
declare const __TALLPINE_BUILD_INFO__: { version: string; commit: string; date: string; dirty: string } | undefined;
import {
app,
clipboard,
shell
} from "electron";
import { realpath, stat } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { APP_DISPLAY_NAME } from "../app-bootstrap";
import { ctx } from "../app-context";
import { getShellWebContents, ipcMain } from "../app-runtime-state";
import { runFilePath } from "../daemon/daemon-lifecycle";
import { DAEMON_PROBE_TIMEOUT_MS } from "../daemon/daemon-probe";
import { createEditorHandoff } from "../editor-handoff";
import { readEditorSettings, writeEditorPreference } from "../editor-settings";
import { launchCommand } from "../launch-command";
import { createLocalPathActions } from "../local-path-actions";
import { discoverMacTerminalApps } from "../mac-terminal-apps";



export function editorStateDir(): string {
	const runFile = runFilePath();
	if (!runFile) throw new Error(`${APP_DISPLAY_NAME} state directory is not available.`);
	return path.dirname(runFile);
}


// Tagged so the renderer reads these as "couldn't check" rather than "the worktree is gone".
export const workspaceCheckUnavailable = (message: string) =>
	Object.assign(new Error(message), { code: "SERVICE_UNAVAILABLE" });


// Reads one absolute session path from the loopback-only desktop routes.
export async function fetchDesktopSessionPath(
	sessionId: string,
	route: "workspace" | "artifacts",
	field: "workspacePath" | "artifactDir",
	unavailableMessage: string,
): Promise<string> {
	if (ctx.daemonStatus.state !== "ready" || !ctx.daemonStatus.port) {
		throw workspaceCheckUnavailable(`${APP_DISPLAY_NAME} daemon is not ready.`);
	}
	const controller = new AbortController();
	const timer = setTimeout(() => controller.abort(), DAEMON_PROBE_TIMEOUT_MS);
	try {
		// Electron net.fetch adds browser metadata without Origin, which the daemon rejects.
		const response = await fetch(
			`http://127.0.0.1:${ctx.daemonStatus.port}/api/v1/desktop/sessions/${encodeURIComponent(sessionId)}/${route}`,
			{ signal: controller.signal },
		).catch((error: unknown) => {
			if (error instanceof Error && error.name === "AbortError") throw error;
			throw workspaceCheckUnavailable(`${APP_DISPLAY_NAME} daemon is not reachable.`);
		});
		const body = await response.json().catch((error: unknown) => {
			if (response.status >= 500) throw workspaceCheckUnavailable(`${APP_DISPLAY_NAME} daemon is not ready.`);
			throw error;
		}) as Record<string, unknown>;
		if (!response.ok) {
			const message = typeof body.message === "string" ? body.message : unavailableMessage;
			throw Object.assign(new Error(message), typeof body.code === "string" ? { code: body.code } : {});
		}
		const sessionPath = body[field];
		if (typeof sessionPath !== "string" || !path.isAbsolute(sessionPath)) {
			throw new Error(unavailableMessage);
		}
		return sessionPath;
	} catch (error) {
		if (error instanceof Error && error.name === "AbortError") {
			throw workspaceCheckUnavailable("Timed out while resolving the session workspace.");
		}
		throw error;
	} finally {
		clearTimeout(timer);
	}
}


export function resolveSessionWorkspaceForDesktop(sessionId: string): Promise<string> {
	return fetchDesktopSessionPath(sessionId, "workspace", "workspacePath", "Session workspace is not available.");
}


// The daemon owns each session's artifact directory, which can live outside
// this app's data dir when the daemon runs with its own TALLPINE_DATA_DIR.
export function resolveSessionArtifactDirForDesktop(sessionId: string): Promise<string> {
	return fetchDesktopSessionPath(sessionId, "artifacts", "artifactDir", "This session has no artifacts folder yet.");
}


export const editorHandoff = createEditorHandoff({
	platform: process.platform,
	env: process.env,
	homeDir: os.homedir(),
	resolveWorkspace: resolveSessionWorkspaceForDesktop,
	resolveArtifactDir: resolveSessionArtifactDirForDesktop,
	readPreference: async () => (await readEditorSettings(editorStateDir())).preferredEditorId,
	writePreference: async (editorId) => {
		await writeEditorPreference(editorStateDir(), editorId);
	},
	launch: (command, args, cwd) => launchCommand(command, args, cwd),
	openDirectory: async (workspacePath) => {
		const error = await shell.openPath(workspacePath);
		if (error) throw new Error(error);
	},
	revealPath: async (filePath) => {
		if (!(await stat(filePath).catch(() => undefined))) throw new Error(`${filePath} does not exist`);
		shell.showItemInFolder(filePath);
	},
	realPath: (candidatePath) => realpath(candidatePath),
	copyText: (text) => clipboard.writeText(text),
	discoverTerminalApps: discoverMacTerminalApps,
	readIcon: async (appPath) => (await app.getFileIcon(appPath, { size: "normal" })).toDataURL(),
	logError: (message, error) => console.error(message, error),
});


export const localPathActions = createLocalPathActions({
	platform: process.platform,
	homeDir: os.homedir(),
	stat: (target) => stat(target),
	realPath: (target) => realpath(target),
	revealPath: (target) => shell.showItemInFolder(target),
	openPath: (target) => shell.openPath(target),
	copyText: (text) => clipboard.writeText(text),
	resolveArtifactDir: resolveSessionArtifactDirForDesktop,
});

export function registerEditorIpc(): void {


	ipcMain.handle("editorHandoff:getState", (event, sessionId: string, options) => {
		if (event.sender !== getShellWebContents()) throw new Error("Untrusted editor handoff request.");
		return editorHandoff.getState(typeof sessionId === "string" ? sessionId : "", { refreshTerminals: options?.refreshTerminals === true });
	});

	ipcMain.handle("editorHandoff:open", (event, input) => {
		if (event.sender !== getShellWebContents()) throw new Error("Untrusted editor handoff request.");
		return editorHandoff.open(input && typeof input === "object" ? input : { sessionId: "" });
	});

	ipcMain.handle("editorHandoff:openArtifacts", (event, input) => {
		if (event.sender !== getShellWebContents()) throw new Error("Untrusted editor handoff request.");
		return editorHandoff.openArtifacts(input && typeof input === "object" ? input : { sessionId: "", targetId: "file-manager" });
	});

	ipcMain.handle("editorHandoff:copyWorkspacePath", (event, sessionId: string) => {
		if (event.sender !== getShellWebContents()) throw new Error("Untrusted editor handoff request.");
		return editorHandoff.copyWorkspacePath(typeof sessionId === "string" ? sessionId : "");
	});

	ipcMain.handle("localPath:inspect", (event, filePath: unknown) => {
		if (event.sender !== getShellWebContents()) throw new Error("Untrusted local path request.");
		return localPathActions.inspect(filePath);
	});

	ipcMain.handle("localPath:perform", (event, filePath: unknown, action: unknown) => {
		if (event.sender !== getShellWebContents()) throw new Error("Untrusted local path request.");
		return localPathActions.perform(filePath, action);
	});
}
