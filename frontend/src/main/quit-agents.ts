import { DAEMON_TOKEN_HEADER } from "./daemon-auth";
import type { TrayText } from "./tray-text";

/**
 * A full quit (the tray's Quit, an OS logout, restart or shutdown) ends the
 * app-owned daemon, so it must not leave agents running without one. This asks
 * the daemon to stop every agent's processes before it exits; each session
 * sleeps and resumes its conversation on its next message.
 *
 * Only an app-owned daemon, the one the supervisor link stops when the app
 * goes, is asked. A keep-alive or headless `tallpine start` daemon outlives the app
 * on purpose, and so do its agents. A daemon restart or takeover never comes
 * through here: it sends a bare /shutdown, so agents survive for the next
 * daemon to adopt.
 */
export function shouldStopAgentsOnQuit(daemon: {
	ready: boolean;
	port?: number;
	linked: boolean;
	keepAlive: boolean;
}): boolean {
	return daemon.ready && Boolean(daemon.port) && daemon.linked && !daemon.keepAlive;
}

/** Sends the deliberate-quit shutdown. Resolves false on any failure. */
export async function requestStopAgents(
	port: number,
	token: string | null | undefined,
	fetchImpl: typeof fetch = fetch,
	timeoutMs = 3000,
): Promise<boolean> {
	try {
		const response = await fetchImpl(`http://127.0.0.1:${port}/shutdown`, {
			method: "POST",
			headers: {
				"content-type": "application/json",
				...(token ? { [DAEMON_TOKEN_HEADER]: token } : {}),
			},
			body: JSON.stringify({ stopAgents: true }),
			signal: AbortSignal.timeout(timeoutMs),
		});
		return response.ok;
	} catch {
		return false;
	}
}

/**
 * What an interactive quit does with the agents still running (B753):
 * keep them running, pause them (each sleeps and resumes on its next message)
 * or stop them (each stays stopped until resumed).
 */
export type QuitAgentsChoice = "keep" | "pause" | "stop";

/**
 * Whether the quit asks the daemon to park its agents. A choice overrides the
 * default: pause parks whichever daemon is attached, even one that outlives the
 * app; keep never parks. Stop has already ended every running agent, so it
 * leaves the rest to the default.
 */
export function parksAgentsOnQuit(
	choice: QuitAgentsChoice | undefined,
	daemon: Parameters<typeof shouldStopAgentsOnQuit>[0],
): boolean {
	if (choice === "keep") return false;
	if (choice === "pause") return daemon.ready && Boolean(daemon.port);
	return shouldStopAgentsOnQuit(daemon);
}

function daemonHeaders(token: string | null | undefined): Record<string, string> {
	return { "content-type": "application/json", ...(token ? { [DAEMON_TOKEN_HEADER]: token } : {}) };
}

/** How many sessions still run an agent; null when the daemon cannot say. */
export async function fetchRunningAgentCount(
	port: number,
	token: string | null | undefined,
	fetchImpl: typeof fetch = fetch,
	timeoutMs = 2000,
): Promise<number | null> {
	try {
		const response = await fetchImpl(`http://127.0.0.1:${port}/api/v1/sessions/running-agents`, {
			headers: daemonHeaders(token),
			signal: AbortSignal.timeout(timeoutMs),
		});
		if (!response.ok) return null;
		const body = (await response.json()) as { sessionIds?: unknown };
		return Array.isArray(body.sessionIds) ? body.sessionIds.length : null;
	} catch {
		return null;
	}
}

export type StopAllAgentsResult = { stopped: number; failures: string[] };

/** Stops every running agent, keeping the sessions. Null when the request failed. */
export async function requestStopAllAgents(
	port: number,
	token: string | null | undefined,
	fetchImpl: typeof fetch = fetch,
	timeoutMs = 60_000,
): Promise<StopAllAgentsResult | null> {
	try {
		const response = await fetchImpl(`http://127.0.0.1:${port}/api/v1/sessions/running-agents/stop`, {
			method: "POST",
			headers: daemonHeaders(token),
			signal: AbortSignal.timeout(timeoutMs),
		});
		if (!response.ok) return null;
		const body = (await response.json()) as { stopped?: unknown; failures?: unknown };
		return {
			stopped: Array.isArray(body.stopped) ? body.stopped.length : 0,
			failures: Array.isArray(body.failures) ? body.failures.map(String) : [],
		};
	} catch {
		return null;
	}
}

type MessageBox = (options: {
	type: "info" | "warning";
	message: string;
	detail?: string;
	buttons?: string[];
	defaultId?: number;
	cancelId?: number;
}) => Promise<{ response: number }>;

/**
 * Asks what to do with the agents still running; "cancel" keeps the app open.
 * Pause parks through the daemon's shutdown, so when the daemon outlives the
 * app on purpose (keep-alive or headless) the dialog says Pause quits it too,
 * and Keep Running, which leaves it alone, is the default.
 */
export async function askQuitAgentsChoice(
	count: number,
	text: TrayText,
	showMessageBox: MessageBox,
	daemonOutlivesApp = false,
): Promise<QuitAgentsChoice | "cancel"> {
	const choices = ["pause", "stop", "keep", "cancel"] as const;
	const { response } = await showMessageBox({
		type: "warning",
		message: text.plural("tray.quitAgentsTitle", count),
		detail: daemonOutlivesApp
			? `${text.t("tray.quitAgentsDetail")}\n\n${text.t("tray.quitAgentsPauseQuitsService")}`
			: text.t("tray.quitAgentsDetail"),
		buttons: [text.t("tray.quitAgentsPause"), text.t("tray.stopAgentsConfirm"), text.t("tray.quitAgentsKeep"), text.t("tray.cancel")],
		defaultId: daemonOutlivesApp ? 2 : 0,
		cancelId: 3,
	});
	return choices[response] ?? "cancel";
}

/**
 * The tray's Stop All Agents: says when the daemon cannot be reached or nothing
 * runs, asks before stopping, and reports agents that would not stop.
 */
export async function confirmAndStopAllAgents(deps: {
	countRunning: () => Promise<number | null>;
	stopAll: () => Promise<StopAllAgentsResult | null>;
	text: TrayText;
	showMessageBox: MessageBox;
}): Promise<void> {
	const { text, showMessageBox } = deps;
	const count = await deps.countRunning();
	if (count === null) {
		await showMessageBox({ type: "warning", message: text.t("tray.daemonUnreachable") });
		return;
	}
	if (count === 0) {
		await showMessageBox({ type: "info", message: text.t("tray.noAgentsRunning") });
		return;
	}
	const { response } = await showMessageBox({
		type: "warning",
		message: text.plural("tray.stopAgentsTitle", count),
		detail: text.t("tray.stopAgentsDetail"),
		buttons: [text.t("tray.stopAgentsConfirm"), text.t("tray.cancel")],
		defaultId: 0,
		cancelId: 1,
	});
	if (response !== 0) return;
	const result = await deps.stopAll();
	if (result && result.failures.length === 0) return;
	await showMessageBox({ type: "warning", message: text.t("tray.stopAgentsFailed"), detail: result?.failures.join("\n") });
}
