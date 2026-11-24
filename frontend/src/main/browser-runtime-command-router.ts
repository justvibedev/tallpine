/**
 * Routes one daemon browser command to the host that runs it: the in-app
 * (Electron) browser, or Google Chrome when the daemon stamped backend=chrome
 * (Real Chrome mode, B849). Session teardown reaches both, since a project may
 * have switched backends during the session (a11).
 */
import type { BrowserRuntimeCommand } from "./browser-runtime-link";
import type { BrowserViewHost } from "./browser-view-host";
import type { ChromeBrowserHost } from "./chrome-browser/host";
import type { BrowserHandoffs } from "./browser-handoff";

export type BrowserCommandHosts = {
	viewHost: (Pick<BrowserViewHost, "execute"> & Partial<Pick<BrowserViewHost, "handoffPage">>) | null;
	chromeHost: (Pick<ChromeBrowserHost, "execute" | "destroySession"> & Partial<Pick<ChromeBrowserHost, "handoffPage">>) | null;
	/** Runs `handoff` (B858) for either backend; null while no window is open. */
	handoffs?: Pick<BrowserHandoffs, "check" | "run"> | null;
	log?: (message: string) => void;
};

const unavailable = (message: string) => Object.assign(new Error(message), { code: "BROWSER_TARGET_UNAVAILABLE" });

export async function routeBrowserRuntimeCommand(
	command: Pick<BrowserRuntimeCommand, "sessionId" | "action" | "args" | "backend">,
	signal: AbortSignal,
	hosts: BrowserCommandHosts,
): Promise<unknown> {
	if (command.action === "handoff") return routeHandoff(command, signal, hosts);
	if (command.action === "__destroy-session") {
		await hosts.chromeHost?.destroySession(command.sessionId).catch((error: unknown) => {
			hosts.log?.(`Chrome session cleanup failed: ${String(error)}`);
		});
	} else if (command.backend === "chrome") {
		if (!hosts.chromeHost) throw unavailable("Google Chrome mode is unavailable until the Tallpine window is open");
		return hosts.chromeHost.execute(command.sessionId, command.action, command.args, signal);
	}
	if (!hosts.viewHost) throw unavailable("Browser target owner is unavailable");
	return hosts.viewHost.execute(command.sessionId, command.action, command.args, signal);
}

// The session's backend brings its tab to the front and reports its URL; the
// shared registry shows the banner and waits on the person.
async function routeHandoff(
	command: Pick<BrowserRuntimeCommand, "sessionId" | "args" | "backend">,
	signal: AbortSignal,
	hosts: BrowserCommandHosts,
): Promise<unknown> {
	const chrome = command.backend === "chrome";
	const host = chrome ? hosts.chromeHost : hosts.viewHost;
	if (!host?.handoffPage || !hosts.handoffs) throw unavailable("Browser handoff is unavailable until the Tallpine window is open");
	// Refuse before preparing the page: a Google sign-in page opens plain Chrome (B854).
	hosts.handoffs.check(command.sessionId, command.args ?? {}, signal);
	const page = await host.handoffPage(command.sessionId, signal);
	return hosts.handoffs.run(command.sessionId, command.args ?? {}, page, signal);
}
