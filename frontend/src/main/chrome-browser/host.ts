/**
 * Runs agent browser commands in the installed Google Chrome (Real Chrome
 * mode, B849). Each Tallpine session drives only its own Chrome tabs, through
 * the same AgentBrowserRuntime and CDP bridge as the in-app browser, and gets
 * the same command surface and result shapes.
 */
import type { ActCandidate } from "../browser-act-matcher";
import type { BrowserHandoffPage } from "../browser-handoff";
import { CHROME_SIGNING_IN, GOOGLE_SIGN_IN_HINT, googleSignInStartURL, isGoogleSignInPage, type ChromeSessionState } from "../../shared/chrome-mode";
import type { AgentBrowserRuntime } from "../agent-browser-runtime/runtime";
import { chromeRuntimeSessionId } from "../agent-browser-runtime/common";
import { browserError, stringArg, throwIfAborted } from "../browser-view-host/args";
import { isAgentBrowserCommandFailure } from "../browser-view-host/agent-results";
import { normalizeAgentCommandURL } from "../browser-view-host/agent-url-policy";
import {
	actAction,
	getAction,
	inputNativeArgs,
	snapshotAction,
	type ActStep,
	type NativeResult,
	type NativeRunner,
	type SnapshotBaseline,
} from "../browser-view-host/agent-action-neutral";
import { sanitizeBrowserTitle, sanitizeBrowserURL } from "../browser-view-host/url";
import { chromeClosedError, type ChromeConnection } from "./pipe-transport";
import { ChromeTargetRegistry, type ChromeSessionTargets } from "./target-provider";
import { CDP_ACTIONS, ChromeSessionCapture, type ChromeCdpSession } from "./cdp-actions";
import { ChromeGoogleSignIn } from "./google-sign-in";

export type ChromeHostRuntime = Pick<AgentBrowserRuntime, "runAction" | "screenshot" | "closeSession">;

export type ChromeHostProcess = {
	ensure(): Promise<ChromeConnection>;
	onChange(listener: () => void): () => void;
	signIn(url?: string): Promise<void>;
	finishSignIn(): Promise<void>;
	readonly running: boolean;
	readonly signingIn: boolean;
};

export type ChromeBrowserHostOptions = {
	process: ChromeHostProcess;
	runtime: ChromeHostRuntime | null;
	onStateChange?: (sessionId: string, state: ChromeSessionState) => void;
	log?: (message: string) => void;
};

type ChromeSession = {
	id: string;
	targets: ChromeSessionTargets;
	capture: ChromeSessionCapture;
	active?: string;
	nativeActive?: string;
	baseline?: SnapshotBaseline;
};

const INPUT_ACTIONS = new Set(["click", "dblclick", "focus", "hover", "highlight", "scrollintoview", "check", "uncheck", "fill", "type", "press", "drag", "scroll", "select"]);
const PASS_THROUGH_ACTIONS = new Set(["wait", "frame", "dialog"]);
const OTHER_ACTIONS = new Set(["open", "snapshot", "act", "unhighlight", "tabs", "tab-new", "tab-select", "tab-close", "get", "screenshot"]);

/** Every agent browser action Chrome mode runs. */
export const CHROME_ACTIONS: ReadonlySet<string> = new Set([...INPUT_ACTIONS, ...PASS_THROUGH_ACTIONS, ...OTHER_ACTIONS, ...CDP_ACTIONS]);

const errorOf = (error: unknown) => ({
	code: String((error as { code?: unknown }).code ?? "BROWSER_CHROME_FAILED"),
	message: error instanceof Error ? error.message : String(error),
});

function targetFacts(tabId: string, rawURL: string): { tabId: string; url: string; origin: string } {
	let origin = "";
	try {
		const parsed = new URL(rawURL || "about:blank");
		if (parsed.protocol === "http:" || parsed.protocol === "https:") origin = parsed.origin;
	} catch {
		// A malformed transient URL keeps a bounded identity without an origin.
	}
	return { tabId, url: sanitizeBrowserURL(rawURL || "about:blank"), origin };
}

export class ChromeBrowserHost {
	private registry: ChromeTargetRegistry | null = null;
	private readonly sessions = new Map<string, ChromeSession>();
	private readonly queues = new Map<string, Promise<unknown>>();
	private readonly errors = new Map<string, { code: string; message: string }>();
	/** Sessions whose Browser panel shows the Chrome mode card. */
	private readonly watched = new Set<string>();
	private readonly unsubscribeChange: () => void;
	private readonly googleSignIn: ChromeGoogleSignIn;

	constructor(private readonly options: ChromeBrowserHostOptions) {
		this.googleSignIn = new ChromeGoogleSignIn(options.process, options.log ?? (() => undefined));
		this.unsubscribeChange = options.process.onChange(() => {
			if (!options.process.signingIn) {
				for (const [sessionId, error] of this.errors) if (error.code === CHROME_SIGNING_IN) this.errors.delete(sessionId);
			}
			for (const sessionId of new Set([...this.sessions.keys(), ...this.watched])) this.publish(sessionId);
		});
	}

	async execute(sessionId: string, action: string, args: Record<string, unknown> = {}, signal?: AbortSignal): Promise<unknown> {
		throwIfAborted(signal);
		if (!sessionId.trim()) throw browserError("INVALID_ARGUMENT", "sessionId is required");
		if (action === "__destroy-session") {
			await this.destroySession(sessionId);
			return { destroyed: true };
		}
		if (!CHROME_ACTIONS.has(action)) {
			throw browserError("BROWSER_CHROME_ACTION_UNSUPPORTED", `The ${action} command is not available in Chrome mode yet`);
		}
		const runtime = this.options.runtime;
		if (!runtime) throw browserError("BROWSER_AUTOMATION_UNAVAILABLE", "Browser automation runtime is unavailable");
		// One command at a time per session, like native operations in the in-app browser (n3).
		return this.serialize(sessionId, async () => {
			let session: ChromeSession;
			try {
				session = await this.session(sessionId);
			} catch (error) {
				this.errors.set(sessionId, errorOf(error));
				this.publish(sessionId);
				throw error;
			}
			const connection = this.registry!.connection;
			try {
				const result = await this.dispatch(runtime, session, action, args, signal);
				this.errors.delete(sessionId);
				return this.withSignInHint(session, result);
			} catch (error) {
				// Chrome quit under the command (a10): the next command relaunches it.
				if (connection.closed) throw chromeClosedError();
				throw error;
			} finally {
				this.publish(sessionId);
			}
		});
	}

	/** Closes the session's Chrome tabs and its native runtime (a8). */
	async destroySession(sessionId: string): Promise<void> {
		await this.serialize(sessionId, async () => {
			const session = this.sessions.get(sessionId);
			this.sessions.delete(sessionId);
			this.errors.delete(sessionId);
			this.watched.delete(sessionId);
			session?.capture.dispose("session-closed");
			await this.options.runtime?.closeSession(chromeRuntimeSessionId(sessionId)).catch(() => undefined);
			if (session && this.registry && !this.registry.connection.closed) await this.registry.closeSession(sessionId);
		});
		this.queues.delete(sessionId);
	}

	state(sessionId: string): ChromeSessionState {
		const session = this.sessions.get(sessionId);
		const running = this.options.process.running;
		const tab = running && session?.active ? session.targets.listTargets().find((target) => target.id === session.active) : undefined;
		const error = this.errors.get(sessionId);
		const signingIn = this.options.process.signingIn;
		return { running, signingIn, activeTab: tab ? { title: tab.title, url: tab.url } : null, ...(error ? { error } : {}) };
	}

	/** The card's state; the card then hears every Chrome change, not only its own commands. */
	watch(sessionId: string): ChromeSessionState {
		this.watched.add(sessionId);
		return this.state(sessionId);
	}

	/** Opens the shared profile in plain Chrome so the person can sign in (B854). */
	async signIn(sessionId: string): Promise<ChromeSessionState> {
		this.watched.add(sessionId);
		try {
			// On a Google sign-in page, plain Chrome opens where that sign-in continues.
			await this.options.process.signIn(googleSignInStartURL(this.state(sessionId).activeTab?.url ?? ""));
			this.errors.delete(sessionId);
		} catch (error) {
			this.errors.set(sessionId, errorOf(error));
		}
		this.publish(sessionId);
		return this.state(sessionId);
	}

	/** Quits the sign-in Chrome and relaunches the controlled one. */
	async finishSignIn(sessionId: string): Promise<ChromeSessionState> {
		await this.options.process.finishSignIn();
		this.publish(sessionId);
		return this.state(sessionId);
	}

	/** Brings the session's tab to the front of its Chrome window. */
	async showWindow(sessionId: string): Promise<{ shown: boolean }> {
		const session = this.sessions.get(sessionId);
		if (!session?.active || !this.options.process.running) return { shown: false };
		await session.targets.activateTarget(session.active);
		return { shown: true };
	}

	/** The session's front tab for a browser handoff (B858), brought to the front of Chrome. */
	handoffPage(sessionId: string, signal?: AbortSignal): Promise<BrowserHandoffPage> {
		return this.serialize(sessionId, async () => {
			throwIfAborted(signal);
			const session = await this.session(sessionId);
			if (!session.active) throw browserError("INVALID_ARGUMENT", "Open the page the person should use before handing off");
			await session.targets.activateTarget(session.active);
			const url = session.targets.listTargets().find((target) => target.id === session.active)?.url ?? "";
			if (isGoogleSignInPage(url)) return this.googleSignIn.handoffPage(url);
			return {
				url: () => {
					const current = this.sessions.get(sessionId);
					return current?.active ? current.targets.listTargets().find((target) => target.id === current.active)?.url : undefined;
				},
			};
		});
	}

	/**
	 * The window closed (the app may keep running on macOS): the sessions' tabs
	 * close with it rather than outliving their owner in the shared Chrome (a8).
	 */
	dispose(): Promise<void> {
		this.unsubscribeChange();
		this.watched.clear();
		for (const [sessionId, session] of this.sessions) {
			session.capture.dispose("session-closed");
			void this.options.runtime?.closeSession(chromeRuntimeSessionId(sessionId)).catch(() => undefined);
		}
		this.sessions.clear();
		this.queues.clear();
		this.errors.clear();
		const registry = this.registry;
		this.registry = null;
		return registry ? registry.dispose().catch(() => undefined) : Promise.resolve();
	}

	private serialize<T>(sessionId: string, operation: () => Promise<T>): Promise<T> {
		const previous = this.queues.get(sessionId) ?? Promise.resolve();
		const next = previous.catch(() => undefined).then(operation);
		this.queues.set(sessionId, next);
		return next;
	}

	private async session(sessionId: string): Promise<ChromeSession> {
		const connection = await this.options.process.ensure();
		if (!this.registry || this.registry.connection !== connection) this.resetRegistry(connection);
		await this.registry!.ready;
		let session = this.sessions.get(sessionId);
		if (!session) {
			session = { id: sessionId, targets: this.registry!.forSession(sessionId), capture: new ChromeSessionCapture() };
			this.sessions.set(sessionId, session);
		}
		return session;
	}

	// A relaunched Chrome has none of the old tabs: forget them and the native
	// runtimes whose bridges pointed at them.
	private resetRegistry(connection: ChromeConnection): void {
		for (const [sessionId, session] of this.sessions) {
			session.capture.dispose("chrome-closed");
			void this.options.runtime?.closeSession(chromeRuntimeSessionId(sessionId)).catch(() => undefined);
		}
		this.sessions.clear();
		this.registry = new ChromeTargetRegistry(connection);
		this.registry.onChange((sessionId, change, targetId) => {
			const session = this.sessions.get(sessionId);
			if (!session) return;
			if (change === "adopted") this.select(session, targetId);
			if (change === "closed") session.capture.forget(targetId);
			if (change === "closed" && session.active === targetId) this.select(session, session.targets.listTargets().at(-1)?.id);
			if (change !== "closed") this.googleSignIn.tabNavigated(session.targets.listTargets().find((target) => target.id === targetId)?.url ?? "");
			this.publish(sessionId);
		});
	}

	private select(session: ChromeSession, targetId: string | undefined): void {
		if (session.active === targetId) return;
		session.active = targetId;
		session.baseline = undefined;
	}

	private publish(sessionId: string): void {
		this.options.onStateChange?.(sessionId, this.state(sessionId));
	}

	private owned(session: ChromeSession, tabId: string) {
		const target = session.targets.listTargets().find((candidate) => candidate.id === tabId);
		if (!target) throw browserError("BROWSER_TARGET_UNAVAILABLE", `Browser tab ${tabId} is unavailable`);
		return target;
	}

	/** The tab in front, opening the session's first one when it has none. */
	private async ensurePage(session: ChromeSession): Promise<string> {
		const targets = session.targets.listTargets();
		if (!session.active || !targets.some((target) => target.id === session.active)) {
			this.select(session, targets.at(-1)?.id ?? (await session.targets.createTarget("about:blank")).id);
		}
		return session.active!;
	}

	// The native daemon keeps its own active tab; move it to ours before a page
	// command, with one resync through a tab listing, as the in-app browser does.
	private async syncNative(runtime: ChromeHostRuntime, session: ChromeSession, signal?: AbortSignal): Promise<void> {
		const tabId = await this.ensurePage(session);
		if (session.nativeActive === tabId) return;
		const key = chromeRuntimeSessionId(session.id);
		try {
			await runtime.runAction(key, "tab-select", { tabId }, session.targets, signal);
		} catch (error) {
			if (!isAgentBrowserCommandFailure(error)) throw error;
			try {
				await runtime.runAction(key, "tabs", {}, session.targets, signal);
				await runtime.runAction(key, "tab-select", { tabId }, session.targets, signal);
			} catch (resyncError) {
				if (!isAgentBrowserCommandFailure(resyncError)) throw resyncError;
				throw browserError("BROWSER_TARGET_MISMATCH", `Browser automation could not target Chrome tab ${tabId}`);
			}
		}
		session.nativeActive = tabId;
	}

	/**
	 * Any command (open, click, act, press...) that leaves the session on a
	 * Google sign-in page tells the agent how the person signs in (B854).
	 */
	private withSignInHint(session: ChromeSession, result: unknown): unknown {
		if (!result || typeof result !== "object" || Array.isArray(result)) return result;
		const resultURL = (result as { url?: unknown }).url;
		const activeURL = session.targets.listTargets().find((target) => target.id === session.active)?.url ?? "";
		const signIn = [typeof resultURL === "string" ? resultURL : "", activeURL].some(isGoogleSignInPage);
		return signIn ? { ...result, signInHint: GOOGLE_SIGN_IN_HINT } : result;
	}

	private target(session: ChromeSession) {
		const tabId = session.active ?? "";
		const target = session.targets.listTargets().find((candidate) => candidate.id === tabId);
		return targetFacts(tabId, target?.url ?? "about:blank");
	}

	private tabsResult(session: ChromeSession) {
		return {
			viewId: `chrome-${session.id}`,
			activeTabId: session.active ?? "",
			tabs: session.targets.listTargets().map((target) => this.tabResult(session, target.id)),
			untrustedExternalContent: true as const,
		};
	}

	private tabResult(session: ChromeSession, tabId: string) {
		const target = this.owned(session, tabId);
		return {
			id: target.id,
			url: sanitizeBrowserURL(target.url),
			title: sanitizeBrowserTitle(target.title),
			active: target.id === session.active,
			agentDrivable: true,
			untrustedExternalContent: true as const,
		};
	}

	private async dispatch(
		runtime: ChromeHostRuntime,
		session: ChromeSession,
		action: string,
		args: Record<string, unknown>,
		signal?: AbortSignal,
	): Promise<unknown> {
		const key = chromeRuntimeSessionId(session.id);
		const runNative: NativeRunner = async (nativeAction, nativeArgs = {}) => {
			await this.syncNative(runtime, session, signal);
			const result = await runtime.runAction(key, nativeAction, nativeArgs, session.targets, signal);
			return { ...result, target: this.target(session) };
		};
		if (INPUT_ACTIONS.has(action)) return runNative(action, inputNativeArgs(action, args));
		if (PASS_THROUGH_ACTIONS.has(action)) return runNative(action, args);
		if (CDP_ACTIONS.has(action)) return session.capture.run(this.cdpSession(session, runNative), action, args);
		switch (action) {
			case "open": {
				const url = normalizeAgentCommandURL(stringArg(args, "url", "URL_REQUIRED", "url is required"));
				const result = await runNative("open", { url });
				const target = this.owned(session, session.active!);
				const rawURL = typeof result.url === "string" ? result.url : target.url;
				const title = typeof result.title === "string" ? result.title : target.title;
				return { viewId: `chrome-${session.id}`, url: sanitizeBrowserURL(rawURL), title: sanitizeBrowserTitle(title), isLoading: false, target: result.target };
			}
			case "snapshot":
				return snapshotAction(args, this.snapshotState(session), runNative);
			case "act":
				return actAction(args, {
					runNative,
					perform: (step, candidate, retried) => this.performAct(session, runNative, step, candidate, retried),
					invalidateBaseline: () => {
						session.baseline = undefined;
					},
				});
			case "get":
				return getAction(args, runNative);
			case "unhighlight": {
				const tabId = await this.ensurePage(session);
				const debug = session.targets.debuggerFor(tabId);
				if (!debug.isAttached()) debug.attach("1.3");
				await debug.sendCommand("Overlay.enable");
				await debug.sendCommand("Overlay.hideHighlight");
				return { url: this.target(session).url };
			}
			case "screenshot": {
				await this.syncNative(runtime, session, signal);
				const result = await runtime.screenshot(key, session.targets, signal, { annotate: args.annotate === true });
				return { ...result, target: this.target(session) };
			}
			case "tabs":
				return this.tabsResult(session);
			case "tab-new": {
				const url = typeof args.url === "string" && args.url.trim() ? normalizeAgentCommandURL(args.url) : "about:blank";
				const created = await session.targets.createTarget(url);
				this.select(session, created.id);
				return this.tabResult(session, created.id);
			}
			case "tab-select": {
				const tabId = stringArg(args, "tabId", "INVALID_ARGUMENT", "tabId is required");
				this.owned(session, tabId);
				this.select(session, tabId);
				await this.syncNative(runtime, session, signal);
				return this.tabResult(session, tabId);
			}
			case "tab-close": {
				const tabId = typeof args.tabId === "string" && args.tabId.trim() ? args.tabId : await this.ensurePage(session);
				this.owned(session, tabId);
				await runtime.runAction(key, "tab-close", { tabId }, session.targets, signal);
				if (session.active === tabId) this.select(session, session.targets.listTargets().at(-1)?.id);
				session.nativeActive = undefined;
				return { closedTabId: tabId, ...this.tabsResult(session) };
			}
		}
		throw new Error(`Unhandled Chrome action: ${action}`);
	}

	private cdpSession(session: ChromeSession, runNative: NativeRunner): ChromeCdpSession {
		return {
			id: session.id,
			ensurePage: () => this.ensurePage(session),
			activeTabId: () => session.active,
			debuggerFor: (tabId) => session.targets.debuggerFor(tabId),
			activate: async (tabId) => {
				await session.targets.activateTarget(tabId);
			},
			runNative,
			target: () => this.target(session),
		};
	}

	private snapshotState(session: ChromeSession) {
		return {
			activeTabId: () => session.active ?? "",
			baseline: () => session.baseline,
			setBaseline: (baseline: SnapshotBaseline | undefined) => {
				session.baseline = baseline;
			},
		};
	}

	// act's matched step. Chrome mode reports the dispatch and URL change; the
	// in-app browser's postcondition observation reads Electron navigation
	// state, so a requested postcondition is reported as not observed here.
	private async performAct(session: ChromeSession, runNative: NativeRunner, step: ActStep, candidate: ActCandidate, retried: boolean): Promise<NativeResult> {
		const before = this.target(session);
		const result = await runNative(step.verb, step.value !== undefined ? { ref: candidate.ref, text: step.value } : { ref: candidate.ref });
		step.dispatched.current = true;
		const after = this.target(session);
		return {
			outcome: "matched",
			resolvedRef: candidate.ref,
			candidate,
			result,
			inputDispatched: true,
			before,
			after,
			navigation: { status: after.tabId !== before.tabId || after.url !== before.url ? "observed" : "not-observed" },
			...(step.postcondition ? { postcondition: { kind: step.postcondition.kind, status: "unsupported", reason: "Chrome mode does not observe postconditions yet" } } : {}),
			retried,
			untrustedExternalContent: true,
		};
	}
}
