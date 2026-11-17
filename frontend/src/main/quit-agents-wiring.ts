import { ctx } from "./app-context";
import { daemonForQuit, daemonToken, quitAgentsGate, showMainMessageBox } from "./app-runtime-state";
import {
	confirmAndStopAllAgents,
	fetchRunningAgentCount,
	parksAgentsOnQuit,
	requestStopAgents,
	requestStopAllAgents,
} from "./quit-agents";
import { trayText } from "./tray-text";

function readyPort(): number | undefined {
	return ctx.daemonStatus.state === "ready" ? ctx.daemonStatus.port : undefined;
}

/**
 * The agents' part of a full quit, after side chats retire. Stop ends every
 * running agent first; then the daemon parks its agents when the owner chose
 * pause, or by default when it goes with the app. Resolves true when the
 * daemon is parking them and will exit by itself.
 */
export async function stopAgentsForQuit(): Promise<boolean> {
	const choice = quitAgentsGate.choice();
	const port = readyPort();
	if (choice === "stop" && port) {
		const result = await requestStopAllAgents(port, daemonToken.token());
		if (!result || result.failures.length > 0) {
			console.warn("Tallpine: some agents did not stop on quit", result?.failures ?? "request failed");
		}
	}
	const daemon = daemonForQuit();
	if (!parksAgentsOnQuit(choice, daemon) || !daemon.port) return false;
	if (!(await requestStopAgents(daemon.port, daemonToken.token()))) {
		console.warn("Tallpine: could not ask the daemon to stop agents on quit");
		return false;
	}
	return true;
}

/** The tray's Stop All Agents. */
export function stopAllAgentsFromTray(): void {
	const port = readyPort();
	void confirmAndStopAllAgents({
		countRunning: async () => (port ? fetchRunningAgentCount(port, daemonToken.token()) : null),
		stopAll: async () => (port ? requestStopAllAgents(port, daemonToken.token()) : null),
		text: trayText(ctx.uiLocale),
		showMessageBox: showMainMessageBox,
	}).catch((error) => console.warn("Tallpine: stop all agents failed", error));
}
