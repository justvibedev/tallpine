/** In-memory ChromeConnection and host fakes for chrome-browser tests. */
import { vi } from "vitest";
import type { AgentBrowserTargetProvider } from "../agent-browser-cdp-bridge";
import { ChromeBrowserHost, type ChromeHostRuntime } from "./host";
import { chromeClosedError, type ChromeConnection, type ChromeEvent } from "./pipe-transport";

type Handler = (params: Record<string, unknown>, sessionId?: string) => Record<string, unknown> | Promise<Record<string, unknown>>;

export class FakeChromeConnection implements ChromeConnection {
	closed = false;
	readonly sent: { method: string; params: Record<string, unknown>; sessionId?: string }[] = [];
	readonly handlers: Record<string, Handler> = {};
	private readonly listeners = new Set<(event: ChromeEvent) => void>();
	private readonly closeListeners = new Set<() => void>();
	private nextTarget = 1;

	constructor() {
		this.handlers["Target.createTarget"] = (params) => {
			const targetId = `T${this.nextTarget++}`;
			this.emit({ method: "Target.targetCreated", params: { targetInfo: { targetId, type: "page", url: params.url, title: "" } } });
			return { targetId };
		};
		this.handlers["Target.attachToTarget"] = (params) => ({ sessionId: `S-${String(params.targetId)}` });
		this.handlers["Target.closeTarget"] = (params) => {
			this.emit({ method: "Target.targetDestroyed", params: { targetId: params.targetId } });
			return { success: true };
		};
	}

	async send(method: string, params: Record<string, unknown> = {}, sessionId?: string): Promise<Record<string, unknown>> {
		if (this.closed) throw chromeClosedError();
		this.sent.push({ method, params, ...(sessionId ? { sessionId } : {}) });
		const handler = this.handlers[method];
		return handler ? handler(params, sessionId) : {};
	}

	get listenerCount(): number {
		return this.listeners.size;
	}

	on(listener: (event: ChromeEvent) => void): () => void {
		this.listeners.add(listener);
		return () => this.listeners.delete(listener);
	}

	onClose(listener: () => void): () => void {
		this.closeListeners.add(listener);
		return () => this.closeListeners.delete(listener);
	}

	close(): void {
		if (this.closed) return;
		this.closed = true;
		for (const listener of [...this.closeListeners]) listener();
	}

	emit(event: ChromeEvent): void {
		for (const listener of [...this.listeners]) listener(event);
	}

	/** A page the person opened by hand, or a popup of openerId. */
	openPage(targetId: string, url: string, openerId?: string): void {
		this.emit({ method: "Target.targetCreated", params: { targetInfo: { targetId, type: "page", url, title: "", ...(openerId ? { openerId } : {}) } } });
	}

	methods(): string[] {
		return this.sent.map((entry) => entry.method);
	}
}

/** A native runtime fake; native answers actions it does not model. */
export function fakeChromeRuntime(native: (action: string, args: Record<string, unknown>) => Record<string, unknown> = () => ({ ok: true })) {
	const calls: { key: string; action: string; args: Record<string, unknown>; tabs: string[] }[] = [];
	const closed: string[] = [];
	const runtime = {
		runAction: vi.fn(async (key: string, action: string, args: Record<string, unknown>, provider: AgentBrowserTargetProvider) => {
			calls.push({ key, action, args, tabs: provider.listTargets().map((target) => target.id) });
			switch (action) {
				case "open":
					return { url: String(args.url), title: "Example" };
				case "snapshot":
					return { snapshot: '- button "Save" [ref=e1]', refs: { e1: { role: "button", name: "Save" } } };
				case "tab-select":
					return { targetId: args.tabId, tabId: args.tabId };
				case "tab-close":
					await provider.closeTarget(String(args.tabId));
					return { closed: true };
				default:
					return native(action, args);
			}
		}),
		screenshot: vi.fn(async () => ({ data: "iVBOR", width: 2, height: 1, untrustedExternalContent: true as const })),
		closeSession: vi.fn(async (key: string) => {
			closed.push(key);
		}),
	};
	return { runtime: runtime as unknown as ChromeHostRuntime & typeof runtime, calls, closed };
}

/** A ChromeBrowserHost over a FakeChromeConnection that relaunches after quit(). */
export function setupChromeHost(native?: (action: string, args: Record<string, unknown>) => Record<string, unknown>) {
	let connection = new FakeChromeConnection();
	const exits = new Set<() => void>();
	const notify = () => {
		for (const l of exits) l();
	};
	let signingIn = false;
	const process = {
		ensure: vi.fn(async () => {
			if (signingIn) throw Object.assign(new Error("signing in"), { code: "BROWSER_CHROME_SIGNING_IN" });
			if (connection.closed) connection = new FakeChromeConnection();
			return connection;
		}),
		onChange: (listener: () => void) => {
			exits.add(listener);
			return () => exits.delete(listener);
		},
		signIn: vi.fn(async () => {
			signingIn = true;
			connection.close();
			notify();
		}),
		finishSignIn: vi.fn(async () => {
			signingIn = false;
			connection = new FakeChromeConnection();
			notify();
		}),
		get running() {
			return !connection.closed;
		},
		get signingIn() {
			return signingIn;
		},
	};
	const { runtime, calls, closed } = fakeChromeRuntime(native);
	const states: unknown[] = [];
	const host = new ChromeBrowserHost({ process, runtime, onStateChange: (sessionId, state) => states.push({ sessionId, ...state }) });
	return { host, runtime, calls, closed, states, process, connection: () => connection, quit: () => { connection.close(); notify(); } };
}
