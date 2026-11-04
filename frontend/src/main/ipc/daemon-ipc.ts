/* Split out of main.ts (B297, P52). */
declare const MAIN_WINDOW_VITE_DEV_SERVER_URL: string | undefined;
declare const MAIN_WINDOW_VITE_NAME: string;
declare const __TALLPINE_TEAM_ID__: string | undefined;
declare const __TALLPINE_BUILD_INFO__: { version: string; commit: string; date: string; dirty: string } | undefined;
import {
app
} from "electron";
import { DAEMON_WS_CLOSE_CHANNEL, DAEMON_WS_OPEN_CHANNEL, DAEMON_WS_SEND_CHANNEL } from "../../shared/daemon-ws";
import { ctx } from "../app-context";
import { daemonWsBridge, getShellWebContents, ipcMain, RENDERER_ORIGIN } from "../app-runtime-state";
import { appRunId } from "../bundled-runtime-staging";
import { closedSideChats, refreshDaemonStatus, rememberSideLaunchState, reportDaemonRestartFailure, restartDaemon, startDaemon, stopDaemon } from "../daemon/daemon-lifecycle";
import { originOf } from "../ipc-guard";


export async function captureSideChatState(closedSideId?: string, cancelClose = false): Promise<void> {
	if (closedSideId) {
		if (cancelClose) closedSideChats.delete(closedSideId); else closedSideChats.add(closedSideId);
		if (ctx.sideLaunchState) rememberSideLaunchState(ctx.sideLaunchState);
	}
	if (ctx.daemonStatus.state !== "ready" || !ctx.daemonStatus.port) return;
	const daemonIdentity = ctx.sideClaimedDaemonPID;
	const request = ++ctx.sideStateRequest;
	const result = await fetch(
		`http://127.0.0.1:${ctx.daemonStatus.port}/api/v1/side-chats/launch/state?appRunId=${encodeURIComponent(appRunId)}`,
		{ signal: AbortSignal.timeout(5000) },
	);
	if (!result.ok) throw new Error(`side chat capture failed: ${result.status}`);
	const state = await result.json() as { appRunId: string; sides: unknown[]; numbers?: Record<string, number> };
	if (daemonIdentity === ctx.sideClaimedDaemonPID && state.appRunId === appRunId && request > ctx.sideStateApplied) {
		ctx.sideStateApplied = request;
		rememberSideLaunchState(state);
	}
}

export function registerDaemonIpc(): void {


	ipcMain.handle("daemon:getStatus", () => refreshDaemonStatus());

	ipcMain.handle(DAEMON_WS_OPEN_CHANNEL, (event, url: unknown) =>
		daemonWsBridge.open(event.sender, url, originOf(event.senderFrame?.url ?? "") ?? RENDERER_ORIGIN));

	ipcMain.on(DAEMON_WS_SEND_CHANNEL, (event, id: unknown, data: unknown) => daemonWsBridge.send(event.sender.id, id, data));

	ipcMain.on(DAEMON_WS_CLOSE_CHANNEL, (event, id: unknown) => daemonWsBridge.close(event.sender.id, id));

	app.once("will-quit", () => daemonWsBridge.closeAll());

	ipcMain.handle("daemon:start", () => startDaemon());

	ipcMain.handle("daemon:stop", () => stopDaemon());

	ipcMain.handle("daemon:restart", async () => {
		try {
			return await restartDaemon();
		} catch (error) {
			return reportDaemonRestartFailure(error);
		}
	});


	ipcMain.handle("sideChats:capture", async (event, closedSideId?: string, cancelClose = false) => {
		if (event.sender !== getShellWebContents()) throw new Error("Untrusted side chat capture request.");
		if (closedSideId !== undefined && (typeof closedSideId !== "string" || closedSideId.length > 255)) {
			throw new Error("Invalid side chat identity.");
		}
		if (typeof cancelClose !== "boolean") throw new Error("Invalid close cancellation.");
		return captureSideChatState(closedSideId, cancelClose);
	});
}
