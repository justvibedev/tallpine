/* Split out of main.ts (B297, P52). */
declare const MAIN_WINDOW_VITE_DEV_SERVER_URL: string | undefined;
declare const MAIN_WINDOW_VITE_NAME: string;
declare const __TALLPINE_TEAM_ID__: string | undefined;
declare const __TALLPINE_BUILD_INFO__: { version: string; commit: string; date: string; dirty: string } | undefined;
import {
app,
utilityProcess
} from "electron";
import { readFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { HOME_DIR_NAME } from "../../shared/brand";
import {
resolveDaemonFromRunFile,
type DaemonProbe
} from "../../shared/daemon-attach";
import { defaultRunFilePath, parseRunFile } from "../../shared/daemon-discovery";
import { resolveDaemonLaunch, type DaemonLaunchSpec } from "../../shared/daemon-launch";
import {
refreshSlowDaemonStartupDetails
} from "../../shared/daemon-startup-status";
import type { DaemonStatus } from "../../shared/daemon-status";
import {
browserDaemonOwnershipDecision
} from "../../shared/daemon-takeover";
import { repairAcpRuntime } from "../acp-runtime-repair";
import { APP_DISPLAY_NAME, desktopDataDir, DEV_STATE_SUBDIR, isDev } from "../app-bootstrap";
import { killDaemon } from "./daemon-process";
import { ctx } from "../app-context";
import { daemonToken, getShellWebContents } from "../app-runtime-state";
import { disposeBrowserRuntimeLink, disposeBrowserStreamLink, establishBrowserRuntimeLink, establishBrowserStreamLink } from "../browser-runtime-link-host";
import { appRunId } from "../bundled-runtime-staging";
import { DAEMON_TOKEN_HEADER } from "../daemon-auth";
import { keepDaemonAlive } from "../daemon-owner";
import { captureSideChatState } from "../ipc/daemon-ipc";
import { configuredLoginShell } from "../shell-env-host";
import { rendererUrl } from "../window/renderer-assets";
import { buildDesktopDaemonEnv, daemonLaunchEnv as daemonLaunchEnvFor, resolveDaemonPort } from "./daemon-env";
import { daemonIdentityError as daemonIdentityErrorFor, processAlive, readDaemonProbe } from "./daemon-probe";
import { startDaemonInner } from "./daemon-start";

export const SIDE_STATE_BACKSTOP_MS = 5_000;

export const closedSideChats = new Set<string>();

export function rememberSideLaunchState(state: { appRunId: string; sides: unknown[]; numbers?: Record<string, number> }): void {
	ctx.sideLaunchState = {
		...state,
		sides: state.sides.filter((record) => {
			const id = (record as { side?: { id?: string } })?.side?.id;
			return typeof id === "string" && ((record as { cleanupOnly?: boolean }).cleanupOnly || !closedSideChats.has(id));
		}),
	};
}

export function setDaemonStatus(nextStatus: DaemonStatus): void {
	if (nextStatus.state !== "ready" || ctx.daemonStatus.state !== "ready" || nextStatus.port !== ctx.daemonStatus.port || nextStatus.pid !== ctx.daemonStatus.pid) ctx.daemonAttachmentGeneration++;
	if (nextStatus.state !== "ready") disposeBrowserRuntimeLink();
	if (nextStatus.state !== "ready") {
		ctx.sideClaimedDaemonPID = undefined;
		if (ctx.sideStateTimer) clearInterval(ctx.sideStateTimer);
		ctx.sideStateTimer = undefined;
		disposeBrowserStreamLink();
	}
	ctx.daemonStatus = nextStatus;
	// A (re)started daemon may have created or rotated the token file.
	if (nextStatus.state === "ready") daemonToken.refresh();
	getShellWebContents()?.send("daemon:status", ctx.daemonStatus);
	if (nextStatus.state === "ready" && ctx.browserViewHost) {
		establishBrowserRuntimeLink();
		establishBrowserStreamLink();
	}
	const daemonIdentity = nextStatus.pid ?? ctx.daemonProcess?.pid;
	if (nextStatus.state === "ready" && nextStatus.port && daemonIdentity && ctx.sideClaimedDaemonPID !== daemonIdentity) {
		ctx.sideClaimedDaemonPID = daemonIdentity;
		const sideBase = `http://127.0.0.1:${nextStatus.port}/api/v1/side-chats/launch`;
		void fetch(`${sideBase}/claim`, {
			method: "POST",
			headers: { "content-type": "application/json" },
			body: JSON.stringify({ appRunId }),
		}).then(async (response) => {
			if (!response.ok) throw new Error(`side launch claim failed: ${response.status}`);
			if (ctx.sideLaunchState?.appRunId === appRunId && (ctx.sideLaunchState.sides.length > 0 || Object.keys(ctx.sideLaunchState.numbers ?? {}).length > 0)) {
				const restored = await fetch(`${sideBase}/state`, {
					method: "POST", headers: { "content-type": "application/json" },
					body: JSON.stringify(ctx.sideLaunchState),
				});
				if (!restored.ok) throw new Error(`side chat recovery failed: ${restored.status}`);
			}
			const readState = async () => {
				if (ctx.sideStatePolling || ctx.sideClaimedDaemonPID !== daemonIdentity) return;
				ctx.sideStatePolling = true;
				const request = ++ctx.sideStateRequest;
				try {
					const result = await fetch(`${sideBase}/state?appRunId=${encodeURIComponent(appRunId)}`);
					if (result.ok) {
						const state = await result.json() as { appRunId: string; sides: unknown[]; numbers?: Record<string, number> };
						if (ctx.sideClaimedDaemonPID === daemonIdentity && state.appRunId === appRunId && request > ctx.sideStateApplied) { ctx.sideStateApplied = request; rememberSideLaunchState(state); }
					}
				} catch (error) { console.warn("Tallpine: side chat memory snapshot failed", error); }
				finally { ctx.sideStatePolling = false; }
			};
			await readState();
			// Backstop only: the renderer captures this state after every side chat
			// change (sideChats:capture). Polling at 500ms cost ~7,200 daemon
			// requests an hour for the whole app lifetime.
			if (ctx.sideClaimedDaemonPID === daemonIdentity) ctx.sideStateTimer = setInterval(() => void readState(), SIDE_STATE_BACKSTOP_MS);
		}).catch((error) => { if (ctx.sideClaimedDaemonPID === daemonIdentity) ctx.sideClaimedDaemonPID = undefined; console.warn("Tallpine: side chat launch claim failed", error); });
	}
}

export const MAX_DAEMON_OUTPUT_CHARS = 12_000;

export function appendDaemonOutput(text: string): void {
	ctx.daemonOutput = (ctx.daemonOutput + text).slice(-MAX_DAEMON_OUTPUT_CHARS);
	const nextStatus = refreshSlowDaemonStartupDetails(ctx.daemonStatus, ctx.daemonOutput);
	if (nextStatus !== ctx.daemonStatus) setDaemonStatus(nextStatus);
}

// How long the supervisor waits for the daemon to confirm its bound port (via
// the listen log line or running.json). A daemon that cannot confirm startup in
// this window is reported with its captured output instead of being treated as
// ready on an assumed port.
export const PORT_DISCOVERY_TIMEOUT_MS = 30_000;

export const DAEMON_RESTART_STOP_TIMEOUT_MS = 5_000;

export const RUN_FILE_POLL_MS = 300;

// Accept run-files stamped slightly before our spawn timestamp: the daemon's
// clock reading and ours race within normal scheduling jitter.
export const RUN_FILE_FRESHNESS_SKEW_MS = 2_000;

export function runFilePath(): string | null {
	if (process.env.TALLPINE_RUN_FILE) return process.env.TALLPINE_RUN_FILE;
	if (isDev) return path.join(os.homedir(), HOME_DIR_NAME, DEV_STATE_SUBDIR, "running.json");
	return defaultRunFilePath(process.platform, process.env, os.homedir());
}

export function daemonEnv(forceKeep = keepDaemonAlive(process.env)): NodeJS.ProcessEnv {
	return buildDesktopDaemonEnv({
		env: process.env,
		platform: process.platform,
		isDev,
		isPackaged: app.isPackaged,
		appPath: app.getAppPath(),
		resourcesPath: process.resourcesPath,
		dataDir: desktopDataDir,
		appRunId,
		forceKeep,
		stagedTmuxBinary: ctx.stagedBundledTmuxBinary,
		runFilePath: runFilePath(),
		rendererUrl: rendererUrl(),
		cachedShellEnv: ctx.cachedShellEnv,
		loginShell: configuredLoginShell,
	});
}

export function daemonIdentityError(launch: DaemonLaunchSpec, probe: DaemonProbe): string | null {
	return daemonIdentityErrorFor(launch, probe, APP_DISPLAY_NAME, process.env.APPIMAGE);
}

/**
 * Establish (or re-establish) the OS-native liveness link to the daemon's
 * supervisor socket. Holding this connection keeps the daemon alive: when
 * Electron exits completely (tray quit, crash, SIGKILL), the OS closes the fd
 * and the daemon detects EOF, then self-stops after its ~5s grace period.
 *
 * Called unconditionally on the spawn path (we always own that daemon).
 * Called on the attach path only when the daemon is app-owned (owner === "app");
 * headless `tallpine start` daemons stay unlinked so they remain persistent after
 * app quit.
 */
export function supervisorPipeFromRunFile(rfp: string | null): string {
	if (!rfp) return "\\\\.\\pipe\\tallpine-supervise";
	const dir = path.basename(path.dirname(rfp));
	if (dir === HOME_DIR_NAME || dir === "." || dir === "") return "\\\\.\\pipe\\tallpine-supervise";
	return "\\\\.\\pipe\\tallpine-supervise-" + dir.replace(/[^a-zA-Z0-9-]/g, "-");
}

export async function inspectExistingDaemon(
	launch: DaemonLaunchSpec,
): Promise<{ status: DaemonStatus; owner: string | undefined; appRunId: string | undefined } | null> {
	const handshakePath = runFilePath();
	let runFileContents: string | null = null;
	if (handshakePath) {
		try {
			runFileContents = await readFile(handshakePath, "utf8");
		} catch {
			runFileContents = null;
		}
	}
	const status = await resolveDaemonFromRunFile({
		runFileContents,
		isProcessAlive: processAlive,
		probe: readDaemonProbe,
		identityError: (probe) => daemonIdentityError(launch, probe),
	});
	if (!status) return null;
	const info = runFileContents ? parseRunFile(runFileContents) : null;
	return { status, owner: info?.owner, appRunId: info?.appRunId };
}

export async function gracefullyReplaceDaemonForBrowser(status: DaemonStatus): Promise<void> {
	if (!status.port) throw new Error("the running daemon did not report a port");
	// This daemon is not attached yet, so name the token rather than relying
	// on the attached-port fetch wrapper.
	const token = daemonToken.token();
	const response = await fetch(`http://127.0.0.1:${status.port}/shutdown`, {
		method: "POST",
		headers: token ? { [DAEMON_TOKEN_HEADER]: token } : undefined,
	});
	if (!response.ok) throw new Error(`daemon shutdown returned HTTP ${response.status}`);

	const deadline = Date.now() + 8_000;
	while (Date.now() < deadline) {
		if (!(await readDaemonProbe(status.port, "healthz"))) return;
		await new Promise<void>((resolve) => setTimeout(resolve, 200));
	}
	throw new Error("the previous daemon did not stop within 8 seconds");
}

export async function refreshDaemonStatus(): Promise<DaemonStatus> {
	if (ctx.daemonProcess) {
		return ctx.daemonStatus;
	}
	const launch = resolveDaemonLaunch(
		process.env,
		app.isPackaged,
		process.resourcesPath,
		app.getAppPath(),
		os.homedir(),
		process.platform,
	);
	if (!launch) return ctx.daemonStatus;
	const existing = await inspectExistingDaemon(launch);
	if (existing) {
		if (browserDaemonOwnershipDecision(appRunId, existing).action === "replace") {
			return startDaemon();
		}
		setDaemonStatus(existing.status);
	} else if (
		ctx.daemonStatus.state === "ready" ||
		(ctx.daemonStatus.state === "error" && (ctx.daemonStatus.pid || ctx.daemonStatus.port))
	) {
		setDaemonStatus({
			state: "stopped",
			message: `${APP_DISPLAY_NAME} daemon is no longer reachable.`,
			code: "daemon_unreachable",
		});
	}
	return ctx.daemonStatus;
}

export async function startDaemon(options: { forceReplaceAdopted?: boolean } = {}): Promise<DaemonStatus> {
	// Sticky, not a parameter of the in-flight call: the renderer's first
	// daemon.getStatus() can attach while boot is still awaiting the shell
	// environment, so a forced replacement requested in that window must be
	// honored by the startup already running instead of being dropped by the
	// memoized promise. startDaemonInner reads it at each attach decision.
	if (options.forceReplaceAdopted) {
		ctx.forceReplaceAdoptedRequested = true;
	}
	// Never (re)launch a daemon, and with it every agent, once the app is
	// quitting: during an OS logout that would block the logout.
	if (ctx.browserQuitRequested) {
		return ctx.daemonStatus;
	}
	if (ctx.daemonStartPromise) {
		return ctx.daemonStartPromise;
	}
	const startEpoch = ctx.daemonStartEpoch;
	const promise = startDaemonInner(startEpoch).finally(() => {
		if (ctx.daemonStartPromise === promise) {
			ctx.daemonStartPromise = null;
		}
	});
	ctx.daemonStartPromise = promise;
	return ctx.daemonStartPromise;
}

// The port this Electron instance expects the daemon to bind. In dev mode a
// separate port isolates the dev daemon from the installed-app daemon.
// TALLPINE_PORT always wins if set explicitly.
export function resolvedDaemonPort(): number {
	return resolveDaemonPort(isDev, process.env);
}

export function daemonLaunchEnv(): NodeJS.ProcessEnv {
	return daemonLaunchEnvFor(isDev, process.platform, app.getAppPath(), process.env);
}

// `npm run dev` builds resources/acp-runtime in predev, but a cache cleaner or a
// plain `electron .` launch can leave it missing and every chat resume then fails
// with "entrypoint ... no such file". Rebuild it here. The script is idempotent
// and a no-op when the runtime is intact. Dev only: a packaged runtime is signed.
export async function repairDevAcpRuntime(): Promise<void> {
	if (app.isPackaged || process.env.TALLPINE_ACP_RUNTIME_DIR) return;
	await repairAcpRuntime(app.getAppPath(), (script, options) => utilityProcess.fork(script, [], options));
}

// Signal the daemon's whole process group so the kill reaches the real daemon
// behind the /bin/sh wrapper (and any PTY children it forked), not just the
// shell. Falls back to a direct kill if the group signal can't be delivered
// (e.g. the process already exited).
export function stopDaemon(): DaemonStatus {
	ctx.daemonStartEpoch += 1;
	ctx.daemonStartPromise = null;
	// An explicit stop (or a newer restart request) cancels any deferred restart
	// left waiting for a previously slow child to exit.
	ctx.daemonRestartAfterExitProcess = null;
	if (!ctx.daemonProcess) {
		setDaemonStatus({ state: "stopped" });
		return ctx.daemonStatus;
	}

	ctx.daemonStoppingProcess = ctx.daemonProcess;
	// Drop the liveness link: an explicit stop is not a frontend death, so stop
	// holding the socket open (and stop the reconnect loop retrying a dead daemon).
	// A later daemon:start re-establishes the link via reportBoundPort.
	ctx.supervisorLink?.dispose();
	ctx.supervisorLink = null;
	disposeBrowserRuntimeLink();
	killDaemon(ctx.daemonProcess);
	setDaemonStatus({ state: "stopped" });
	return ctx.daemonStatus;
}

export function reportDaemonRestartFailure(error: unknown): DaemonStatus {
	setDaemonStatus({
		state: "error",
		message: `Could not restart the ${APP_DISPLAY_NAME} daemon: ${error instanceof Error ? error.message : String(error)}`,
		details: ctx.daemonOutput.trim() || undefined,
		code: "spawn_failed",
	});
	return ctx.daemonStatus;
}

export async function startDaemonForRestart(): Promise<DaemonStatus> {
	try {
		return await startDaemon();
	} catch (error) {
		return reportDaemonRestartFailure(error);
	}
}

export async function restartDaemon(): Promise<DaemonStatus> {
	const child = ctx.daemonProcess;
	if (!child) return startDaemonForRestart();
	await captureSideChatState().catch((error) => console.warn("Tallpine: pre-restart side snapshot failed", error));

	const exited = new Promise<boolean>((resolve) => {
		let settled = false;
		const finish = (didExit: boolean) => {
			if (settled) return;
			settled = true;
			clearTimeout(timer);
			child.off("exit", onExit);
			resolve(didExit);
		};
		const onExit = () => finish(true);
		const timer = setTimeout(() => finish(false), DAEMON_RESTART_STOP_TIMEOUT_MS);
		child.once("exit", onExit);
	});

	stopDaemon();
	// Register the deferred intent immediately after signaling the child. The
	// exit can race the five-second UI timeout, so waiting until after the
	// timeout to set this would occasionally miss the only exit event.
	ctx.daemonRestartAfterExitProcess = child;
	if (!(await exited)) {
		// The process may still honor SIGTERM after the UI timeout. Keep the
		// restart intent attached to that exact child so its eventual exit starts
		// a replacement instead of collapsing back to a code-less stopped state.
		setDaemonStatus({
			state: "error",
			message: `${APP_DISPLAY_NAME} daemon is still stopping. It will restart automatically when shutdown completes.`,
			details: ctx.daemonOutput.trim() || undefined,
			code: "not_ready",
		});
		return ctx.daemonStatus;
	}
	return startDaemonForRestart();
}
