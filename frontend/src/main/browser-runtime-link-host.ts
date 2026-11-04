/* Split out of main.ts (B297, P52). */
declare const MAIN_WINDOW_VITE_DEV_SERVER_URL: string | undefined;
declare const MAIN_WINDOW_VITE_NAME: string;
declare const __TALLPINE_TEAM_ID__: string | undefined;
declare const __TALLPINE_BUILD_INFO__: { version: string; commit: string; date: string; dirty: string } | undefined;
import {
BrowserWindow
} from "electron";
import { readFileSync } from "node:fs";
import path from "node:path";
import { parseRunFile } from "../shared/daemon-discovery";
import {
browserRuntimeRecoveryDecision
} from "../shared/daemon-takeover";
import { ctx } from "./app-context";
import { getShellWebContents } from "./app-runtime-state";
import type {
BrowserRemoteInput,
BrowserRemoteNavigation,
BrowserRemoteTabAction,
} from "./browser-live-types";
import { sameBrowserRuntimeIdentity } from "./browser-runtime-identity";
import {
BROWSER_RUNTIME_DISCONNECTED,
connectBrowserRuntime
} from "./browser-runtime-link";
import {
connectBrowserStream,
type BrowserStreamControl
} from "./browser-stream-link";
import {
type BrowserRuntimeState
} from "./browser-view-host";
import { browserRuntimeToken } from "./bundled-runtime-staging";
import { runFilePath, setDaemonStatus, startDaemon, supervisorPipeFromRunFile } from "./daemon/daemon-lifecycle";
import { checkRender, measureRender } from "./render-check";
import { connectSupervisor } from "./supervisor-link";



export function dropBrowserRuntimeLink(): void {
	ctx.browserRuntimeLink?.dispose();
	ctx.browserRuntimeLink = null;
	ctx.browserRuntimeLinkIdentity = null;
}


export function disposeBrowserRuntimeLink(): void {
	const hadLink = ctx.browserRuntimeLink !== null;
	dropBrowserRuntimeLink();
	if (hadLink) publishBrowserRuntimeState(BROWSER_RUNTIME_DISCONNECTED);
}


export function publishBrowserRuntimeState(state: BrowserRuntimeState): void {
	getShellWebContents()?.send("browser:runtimeState", state);
}


// A replacement link reports "connecting" itself, so dropping the old one first
// does not flash a disconnect in the Browser panel.
export function reconnectBrowserRuntimeLink(): void {
	dropBrowserRuntimeLink();
	establishBrowserRuntimeLink();
	if (!ctx.browserRuntimeLink) publishBrowserRuntimeState(BROWSER_RUNTIME_DISCONNECTED);
}


export function establishBrowserRuntimeLink(): void {
	if (!ctx.browserViewHost) return;
	const rfp = runFilePath();
	if (!rfp) {
		console.warn("Tallpine: browser runtime link skipped; run-file path unavailable");
		return;
	}
	let runInfo: ReturnType<typeof parseRunFile> = null;
	try {
		runInfo = parseRunFile(readFileSync(rfp, "utf8"));
	} catch {
		// Daemon readiness will retry this after running.json becomes readable.
	}
	const address = runInfo?.browserRuntimeAddress;
	if (!address) {
		console.warn("Tallpine: browser runtime link skipped; daemon did not publish an address");
		return;
	}
	const token = browserRuntimeToken;
	const identity = {
		pid: runInfo?.pid ?? 0,
		startedAtMs: runInfo?.startedAtMs ?? 0,
		address,
		token,
	};
	if (ctx.browserRuntimeLink && ctx.browserRuntimeLinkIdentity && sameBrowserRuntimeIdentity(ctx.browserRuntimeLinkIdentity, identity)) {
		return;
	}
	if (ctx.browserRuntimeLink) dropBrowserRuntimeLink();
	ctx.browserRuntimeLink = connectBrowserRuntime(address, {
		token,
		execute: (command, signal) => {
			// A render check or measure uses its own hidden offscreen window,
			// never the main window or the session's Browser panel, so it does
			// not need (or disturb) the view host.
			if (command.action === "__render-check") {
				return checkRender({ BrowserWindow }, command.args ?? {}, signal);
			}
			if (command.action === "__render-measure") {
				return measureRender({ BrowserWindow }, command.args ?? {}, signal);
			}
			const host = ctx.browserViewHost;
			if (!host) {
				throw Object.assign(new Error("Browser target owner is unavailable"), {
					code: "BROWSER_TARGET_UNAVAILABLE",
				});
			}
			return host.execute(command.sessionId, command.action, command.args, signal);
		},
		log: (message) => console.log(`Tallpine: ${message}`),
		onStateChange: publishBrowserRuntimeState,
		onHandshakeRejected: handleBrowserRuntimeRejection,
	});
	ctx.browserRuntimeLinkIdentity = identity;
}


/**
 * A daemon we only adopted holds the runtime token of the launch that started it,
 * so an `invalid-token` refusal is permanent for this launch: retrying cannot
 * help, and the browser panel stays unusable. Retire that daemon and start our
 * own, which is handed the current token. Bounded to one attempt per launch by
 * browserRuntimeRecoveryDecision.
 */
export function handleBrowserRuntimeRejection(reason: string): void {
	const decision = browserRuntimeRecoveryDecision({
		rejectedReason: reason,
		spawnedHere: ctx.daemonProcess !== null,
		attempted: ctx.browserRuntimeRecoveryAttempted,
	});
	if (decision.action === "wait") return;
	ctx.browserRuntimeRecoveryAttempted = true;
	console.warn(`Tallpine: replacing an adopted daemon that refused this app's browser runtime (${decision.reason})`);
	disposeBrowserRuntimeLink();
	setDaemonStatus({
		state: "starting",
		message: "Restarting the daemon to restore the browser runtime.",
	});
	void startDaemon({ forceReplaceAdopted: true });
}


export function disposeBrowserStreamLink(): void {
	ctx.browserStreamLink?.dispose();
	ctx.browserStreamLink = null;
	ctx.browserStreamLinkIdentity = null;
}


export async function handleBrowserStreamControl(control: BrowserStreamControl): Promise<void> {
	const host = ctx.browserViewHost;
	if (!host) throw Object.assign(new Error("Browser target owner is unavailable"), { code: "BROWSER_TARGET_UNAVAILABLE" });
	const sessionId = control.sessionId?.trim() ?? "";
	if (!sessionId) throw Object.assign(new Error("sessionId is required"), { code: "INVALID_ARGUMENT" });
	switch (control.type) {
		case "start": {
			const streamId = control.streamId ?? 0;
			await host.startLiveStream(sessionId, streamId, {
				frame: (frame) => ctx.browserStreamLink?.sendFrame({ streamId, ...frame }),
				state: (state) => { ctx.browserStreamLink?.sendControl({ type: "state", streamId, sessionId, payload: state }); },
				error: (code, message) => { ctx.browserStreamLink?.sendControl({ type: "error", streamId, sessionId, code, message }); },
			});
			return;
		}
		case "stop":
			await host.stopLiveStream(sessionId, control.streamId ?? 0);
			return;
		case "input":
			await host.handleRemoteInput(sessionId, control.payload as BrowserRemoteInput);
			return;
		case "navigate":
			await host.handleRemoteNavigation(sessionId, control.payload as BrowserRemoteNavigation);
			return;
		case "tab":
			await host.handleRemoteTab(sessionId, control.payload as BrowserRemoteTabAction);
			return;
		case "preview": {
			const url = (control.payload as { url?: unknown } | undefined)?.url;
			if (typeof url === "string") await host.openLivePreview(sessionId, url);
			return;
		}
		default:
			throw Object.assign(new Error(`Unsupported browser stream command: ${control.type}`), { code: "INVALID_ARGUMENT" });
	}
}


export function establishBrowserStreamLink(): void {
	if (!ctx.browserViewHost) return;
	const rfp = runFilePath();
	if (!rfp) return;
	let runInfo: ReturnType<typeof parseRunFile> = null;
	try {
		runInfo = parseRunFile(readFileSync(rfp, "utf8"));
	} catch {
		return;
	}
	const address = runInfo?.browserStreamAddress;
	if (!address) return;
	const identity = { pid: runInfo?.pid ?? 0, startedAtMs: runInfo?.startedAtMs ?? 0, address, token: browserRuntimeToken };
	if (ctx.browserStreamLink && ctx.browserStreamLinkIdentity && sameBrowserRuntimeIdentity(ctx.browserStreamLinkIdentity, identity)) return;
	disposeBrowserStreamLink();
	ctx.browserStreamLink = connectBrowserStream(address, {
		token: browserRuntimeToken,
		onControl: handleBrowserStreamControl,
		onDisconnect: () => ctx.browserViewHost?.stopAllLiveStreams(),
		log: (message) => console.log(`Tallpine: ${message}`),
	});
	ctx.browserStreamLinkIdentity = identity;
}


export function establishSupervisorLink(): void {
	const rfp = runFilePath();
	const addr =
		process.platform === "win32"
			? supervisorPipeFromRunFile(rfp)
			: rfp
				? path.join(path.dirname(rfp), "supervise.sock")
				: null;
	if (addr) {
		ctx.supervisorLink?.dispose();
		ctx.supervisorLink = connectSupervisor(addr, {
			log: (msg) => console.log(`Tallpine: ${msg}`),
		});
	} else {
		console.warn("Tallpine: supervisor link skipped; run-file path unavailable");
	}
}
