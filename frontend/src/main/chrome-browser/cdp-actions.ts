/**
 * Chrome mode's CDP-direct agent browser commands (B849 PR 2): network
 * capture, console/errors, viewport, the devtools hint, and back/forward/
 * reload/eval/upload (B857). They run through
 * the same backend-neutral helpers as the in-app browser, on the session's
 * own Chrome tab debugger.
 */
import type { AgentBrowserDebugger } from "../agent-browser-cdp-bridge";
import { networkDurationArg } from "../browser-view-host/args";
import { applyViewport, messagesAction } from "../browser-view-host/agent-action-cdp";
import type { NativeRunner } from "../browser-view-host/agent-action-neutral";
import { PAGE_CDP_ACTIONS, pageCdpAction } from "../browser-view-host/agent-action-page-cdp";
import {
	clearNetworkCapture,
	disposeNetworkCapture,
	handleNetworkDebuggerEvent,
	networkCaptureResult,
	networkCaptureStatus,
	startTabNetworkCapture,
	stopNetworkCapture,
	type NetworkCaptureTab,
} from "../browser-view-host/network-capture";

export const CDP_ACTIONS: ReadonlySet<string> = new Set([
	"network-start",
	"network-status",
	"network-list",
	"network-stop",
	"network-clear",
	"console",
	"errors",
	"viewport",
	"devtools-open",
	"devtools-close",
	...PAGE_CDP_ACTIONS,
]);

/** What a CDP-direct command needs from the Chrome host for one session. */
export type ChromeCdpSession = {
	id: string;
	/** The session's front tab, opening its first one when it has none. */
	ensurePage(): Promise<string>;
	activeTabId(): string | undefined;
	debuggerFor(tabId: string): AgentBrowserDebugger;
	activate(tabId: string): Promise<void>;
	runNative: NativeRunner;
	target(): unknown;
};

type CaptureTab = NetworkCaptureTab & { debug: AgentBrowserDebugger; listener: (...args: unknown[]) => void };

/**
 * Chrome has no DevTools surface Tallpine can dock, so devtools-open/close
 * bring the tab to the front and tell the agent the keystroke (a12).
 */
export function devtoolsHint(open: boolean, platform: NodeJS.Platform = process.platform): string {
	const keys = platform === "darwin" ? "Cmd+Opt+I" : "Ctrl+Shift+I";
	return open
		? `Chrome mode cannot open DevTools for you: the tab is in front, press ${keys} in Chrome to open them.`
		: `Chrome mode cannot close DevTools for you: press ${keys} in Chrome to close them.`;
}

/** Network capture state of one session's Chrome tabs. */
export class ChromeSessionCapture {
	private readonly tabs = new Map<string, CaptureTab>();
	private networkTabId?: string;

	/** The tab the session captures, or its front tab. */
	private captureTab(session: ChromeCdpSession): NetworkCaptureTab {
		if (this.networkTabId) {
			const captured = this.tabs.get(this.networkTabId);
			if (captured) return captured;
			this.networkTabId = undefined;
		}
		const tabId = session.activeTabId() ?? "";
		return this.tabs.get(tabId) ?? { tabId };
	}

	private tab(session: ChromeCdpSession, tabId: string): CaptureTab {
		let tab = this.tabs.get(tabId);
		if (!tab) {
			const debug = session.debuggerFor(tabId);
			const created: CaptureTab = {
				tabId,
				debug,
				// Only the page's own session: child sessions (frames, workers) reuse request ids.
				listener: (_event, method, params, childSessionId) => {
					if (childSessionId || typeof method !== "string") return;
					handleNetworkDebuggerEvent(created, method, (params ?? {}) as Record<string, unknown>);
				},
			};
			debug.on("message", created.listener);
			this.tabs.set(tabId, created);
			tab = created;
		}
		return tab;
	}

	/** The tab closed: its capture ends and its listener goes. */
	forget(tabId: string, reason = "tab-closed"): void {
		const tab = this.tabs.get(tabId);
		if (!tab) return;
		disposeNetworkCapture(tab, reason);
		tab.debug.off?.("message", tab.listener);
		this.tabs.delete(tabId);
		if (this.networkTabId === tabId) this.networkTabId = undefined;
	}

	dispose(reason: string): void {
		for (const tabId of [...this.tabs.keys()]) this.forget(tabId, reason);
	}

	async run(session: ChromeCdpSession, action: string, args: Record<string, unknown>): Promise<unknown> {
		switch (action) {
			case "network-start": {
				const durationSeconds = networkDurationArg(args.durationSeconds);
				const tabId = await session.ensurePage();
				const tab = this.tab(session, tabId);
				return startTabNetworkCapture(this.captureTab(session), tab, durationSeconds, async () => tab.debug, () => {
					this.networkTabId = tabId;
				});
			}
			case "network-status":
				return networkCaptureStatus(this.captureTab(session));
			case "network-list":
				return networkCaptureResult(this.captureTab(session));
			case "network-stop":
				return stopNetworkCapture(this.captureTab(session), "stopped");
			case "network-clear":
				return clearNetworkCapture(this.captureTab(session));
			case "console":
			case "errors":
				return messagesAction(action, session.runNative);
			case "viewport": {
				const result = await applyViewport(async () => session.debuggerFor(await session.ensurePage()), args);
				return { ...result, target: session.target() };
			}
			case "back":
			case "forward":
			case "reload":
			case "eval":
			case "upload":
				// eval needs no further grant here: Chrome mode is the person's own
				// per-project choice to hand agents that Chrome profile's sign-ins.
				return pageCdpAction(action, args, {
					tab: async () => {
						const tabId = await session.ensurePage();
						return { tabId, debug: session.debuggerFor(tabId) };
					},
					runNative: session.runNative,
					target: () => session.target(),
				});
			case "devtools-open":
			case "devtools-close": {
				const tabId = await session.ensurePage();
				await session.activate(tabId);
				return {
					viewId: `chrome-${session.id}`,
					open: false,
					activeTabId: tabId,
					hint: devtoolsHint(action === "devtools-open"),
					target: session.target(),
				};
			}
		}
		throw new Error(`Unhandled Chrome CDP action: ${action}`);
	}
}
