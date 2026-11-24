/**
 * Per-session views of the Tallpine Chrome's page targets (B849 a5): a session
 * sees only the tabs it created and the popups they opened. Tabs the person
 * opened by hand are never listed to any agent.
 */
import { EventEmitter } from "node:events";
import type { AgentBrowserDebugger, AgentBrowserTarget, AgentBrowserTargetProvider } from "../agent-browser-cdp-bridge";
import type { ChromeConnection, ChromeEvent } from "./pipe-transport";

type TargetInfo = { targetId: string; type: string; url: string; title: string; openerId?: string };

/**
 * Over the pipe Chrome treats its client as browser-level, so a page session
 * reaches the whole Target and Browser domains: it could list every tab or
 * attach to one it does not own (a5). A page session may only auto-attach its
 * own children, detach one of them, and read its own target info.
 */
function assertPageSessionMethod(method: string, params: Record<string, unknown>, targetId: string, children: Set<string>): void {
	const allowed = method.startsWith("Target.")
		? method === "Target.setAutoAttach" ||
			(method === "Target.detachFromTarget" && typeof params.sessionId === "string" && children.has(params.sessionId)) ||
			(method === "Target.getTargetInfo" && (params.targetId === undefined || params.targetId === targetId))
		: !method.startsWith("Browser.") || method === "Browser.getVersion";
	if (!allowed) throw new Error(`CDP method is not permitted by Tallpine: ${method}`);
}

/**
 * AgentBrowserDebugger over one flattened CDP session on the pipe, so the
 * existing AgentBrowserCDPBridge drives a Chrome tab as it drives an Electron
 * WebContents. Events of the page session arrive with no sessionId and events
 * of its auto-attached children with theirs, as Electron reports them.
 */
export class ChromeTargetDebugger extends EventEmitter implements AgentBrowserDebugger {
	private sessionId: string | null = null;
	private attaching: Promise<string> | null = null;
	private readonly children = new Set<string>();
	private unsubscribe: (() => void) | null = null;

	constructor(private readonly connection: ChromeConnection, readonly targetId: string) {
		super();
	}

	/** Chrome's pipe speaks one protocol version, so protocolVersion is ignored. */
	attach(_protocolVersion?: string): void {
		if (this.attaching) return;
		this.unsubscribe = this.connection.on((event) => this.onEvent(event));
		this.attaching = this.connection
			.send("Target.attachToTarget", { targetId: this.targetId, flatten: true })
			.then((result) => {
				const sessionId = String(result.sessionId ?? "");
				if (!sessionId) throw new Error("Chrome returned no target session");
				this.sessionId = sessionId;
				return sessionId;
			});
		this.attaching.catch(() => this.released());
	}

	detach(): void {
		const sessionId = this.sessionId;
		this.released(false);
		if (sessionId && !this.connection.closed) {
			void this.connection.send("Target.detachFromTarget", { sessionId }).catch(() => undefined);
		}
	}

	isAttached(): boolean {
		return this.attaching !== null;
	}

	async sendCommand(method: string, params: Record<string, unknown> = {}, childSessionId?: string): Promise<unknown> {
		if (!this.attaching) throw new Error("Debugger is not attached");
		const sessionId = await this.attaching;
		if (childSessionId && !this.children.has(childSessionId)) throw new Error("Unknown child target session");
		assertPageSessionMethod(method, params, this.targetId, this.children);
		return this.connection.send(method, params, childSessionId ?? sessionId);
	}

	/** Releases the session; emits detach when it was attached. */
	released(emit = true): void {
		const wasAttached = this.attaching !== null;
		this.unsubscribe?.();
		this.unsubscribe = null;
		this.attaching = null;
		this.sessionId = null;
		this.children.clear();
		if (emit && wasAttached) this.emit("detach", {}, "target_closed");
	}

	private onEvent(event: ChromeEvent): void {
		if (!this.sessionId) return;
		if (event.method === "Target.detachedFromTarget" && event.params.sessionId === this.sessionId && !event.sessionId) {
			this.released();
			return;
		}
		const own = event.sessionId === this.sessionId;
		if (!own && !(event.sessionId && this.children.has(event.sessionId))) return;
		const child = typeof event.params.sessionId === "string" ? event.params.sessionId : "";
		if (event.method === "Target.attachedToTarget" && child) this.children.add(child);
		if (event.method === "Target.detachedFromTarget" && child) this.children.delete(child);
		this.emit("message", {}, event.method, event.params, own ? undefined : event.sessionId);
	}
}

/** Chrome's answer to joining a window when it has none open. */
function isNoBrowserOpen(error: unknown): boolean {
	return /no browser is open/i.test(error instanceof Error ? error.message : String(error));
}

/**
 * Every page target of one Chrome connection and which Tallpine session owns
 * it. Ownership comes only from a session's own createTarget and from a popup
 * whose opener that session owns.
 */
export class ChromeTargetRegistry {
	private readonly infos = new Map<string, TargetInfo>();
	private readonly owners = new Map<string, string>();
	private readonly debuggers = new Map<string, ChromeTargetDebugger>();
	private readonly listeners = new Set<(sessionId: string, change: "adopted" | "changed" | "closed", targetId: string) => void>();
	private readonly unsubscribe: () => void;
	readonly ready: Promise<void>;

	constructor(readonly connection: ChromeConnection) {
		this.unsubscribe = connection.on((event) => this.onEvent(event));
		this.ready = connection.send("Target.setDiscoverTargets", { discover: true }).then(() => undefined);
		connection.onClose(() => {
			for (const debug of this.debuggers.values()) debug.released();
			this.unsubscribe();
		});
	}

	onChange(listener: (sessionId: string, change: "adopted" | "changed" | "closed", targetId: string) => void): () => void {
		this.listeners.add(listener);
		return () => this.listeners.delete(listener);
	}

	forSession(sessionId: string): ChromeSessionTargets {
		return new ChromeSessionTargets(this, sessionId);
	}

	owned(sessionId: string): AgentBrowserTarget[] {
		const out: AgentBrowserTarget[] = [];
		for (const [targetId, owner] of this.owners) {
			if (owner !== sessionId) continue;
			const info = this.infos.get(targetId);
			out.push({ id: targetId, url: info?.url ?? "about:blank", title: info?.title ?? "", debugger: this.debuggerFor(targetId) });
		}
		return out;
	}

	ownerOf(targetId: string): string | undefined {
		return this.owners.get(targetId);
	}

	debuggerFor(targetId: string): ChromeTargetDebugger {
		let debug = this.debuggers.get(targetId);
		if (!debug) {
			debug = new ChromeTargetDebugger(this.connection, targetId);
			this.debuggers.set(targetId, debug);
		}
		return debug;
	}

	async create(sessionId: string, url: string): Promise<AgentBrowserTarget> {
		await this.ready;
		// The session's first tab opens its own window; later ones best-effort join
		// the last active window (a6). Once the person closes the session's last
		// tab, Chrome on macOS stays running with no window, and joining one fails
		// ("no browser is open"): the next tab opens a window again.
		const newWindow = this.owned(sessionId).length === 0;
		let result: Record<string, unknown>;
		try {
			result = await this.connection.send("Target.createTarget", { url, newWindow });
		} catch (error) {
			if (newWindow || !isNoBrowserOpen(error)) throw error;
			result = await this.connection.send("Target.createTarget", { url, newWindow: true });
		}
		const targetId = String(result.targetId ?? "");
		if (!targetId) throw new Error("Chrome returned no target");
		this.owners.set(targetId, sessionId);
		if (!this.infos.has(targetId)) this.infos.set(targetId, { targetId, type: "page", url, title: "" });
		const info = this.infos.get(targetId)!;
		return { id: targetId, url: info.url, title: info.title, debugger: this.debuggerFor(targetId) };
	}

	async close(sessionId: string, targetId: string): Promise<void> {
		if (this.owners.get(targetId) !== sessionId) throw new Error("Target is outside this Tallpine worker");
		await this.connection.send("Target.closeTarget", { targetId });
		this.forget(targetId);
	}

	async activate(sessionId: string, targetId: string): Promise<void> {
		if (this.owners.get(targetId) !== sessionId) throw new Error("Target is outside this Tallpine worker");
		await this.connection.send("Target.activateTarget", { targetId });
	}

	/** Closes every tab the session owns (a8). */
	async closeSession(sessionId: string): Promise<void> {
		const ids = [...this.owners].filter(([, owner]) => owner === sessionId).map(([id]) => id);
		await Promise.all(ids.map((id) => this.close(sessionId, id).catch(() => this.forget(id))));
	}

	/** Closes every session's tabs and stops following this connection. */
	async dispose(): Promise<void> {
		const sessions = new Set(this.owners.values());
		if (!this.connection.closed) await Promise.all([...sessions].map((sessionId) => this.closeSession(sessionId)));
		for (const debug of this.debuggers.values()) debug.released();
		this.debuggers.clear();
		this.listeners.clear();
		this.unsubscribe();
	}

	private forget(targetId: string): void {
		const owner = this.owners.get(targetId);
		this.owners.delete(targetId);
		this.infos.delete(targetId);
		this.debuggers.get(targetId)?.released();
		this.debuggers.delete(targetId);
		if (owner) this.notify(owner, "closed", targetId);
	}

	private notify(sessionId: string, change: "adopted" | "changed" | "closed", targetId: string): void {
		for (const listener of [...this.listeners]) listener(sessionId, change, targetId);
	}

	private onEvent(event: ChromeEvent): void {
		if (event.sessionId) return;
		const raw = event.params.targetInfo as Record<string, unknown> | undefined;
		if ((event.method === "Target.targetCreated" || event.method === "Target.targetInfoChanged") && raw) {
			if (raw.type !== "page" || typeof raw.targetId !== "string") return;
			const info: TargetInfo = {
				targetId: raw.targetId,
				type: "page",
				url: typeof raw.url === "string" ? raw.url : "",
				title: typeof raw.title === "string" ? raw.title : "",
				...(typeof raw.openerId === "string" ? { openerId: raw.openerId } : {}),
			};
			this.infos.set(info.targetId, info);
			const owner = this.owners.get(info.targetId);
			if (owner) {
				this.notify(owner, "changed", info.targetId);
				return;
			}
			const openerOwner = info.openerId ? this.owners.get(info.openerId) : undefined;
			if (event.method === "Target.targetCreated" && openerOwner) {
				this.owners.set(info.targetId, openerOwner);
				this.notify(openerOwner, "adopted", info.targetId);
			}
			return;
		}
		if (event.method === "Target.targetDestroyed" && typeof event.params.targetId === "string") {
			const targetId = event.params.targetId;
			if (this.owners.has(targetId)) this.forget(targetId);
			else this.infos.delete(targetId);
		}
	}
}

/** One session's AgentBrowserTargetProvider over the shared registry. */
export class ChromeSessionTargets implements AgentBrowserTargetProvider {
	constructor(private readonly registry: ChromeTargetRegistry, readonly sessionId: string) {}

	listTargets(): AgentBrowserTarget[] {
		return this.registry.owned(this.sessionId);
	}

	createTarget(url: string): Promise<AgentBrowserTarget> {
		return this.registry.create(this.sessionId, url);
	}

	activateTarget(targetId: string): Promise<void> {
		return this.registry.activate(this.sessionId, targetId);
	}

	closeTarget(targetId: string): Promise<void> {
		return this.registry.close(this.sessionId, targetId);
	}

	debuggerFor(targetId: string): AgentBrowserDebugger {
		if (this.registry.ownerOf(targetId) !== this.sessionId) throw new Error("Target is outside this Tallpine worker");
		return this.registry.debuggerFor(targetId);
	}
}
