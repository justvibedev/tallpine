/**
 * Runs browser handoffs (B858) for both browser backends: shows the Browser
 * panel's banner, brings the agent's tab to the front, and waits until the
 * person clicks Continue, the page reaches --until-url, or the timeout. The
 * daemon raises and resolves the needs-you notification around this wait
 * (backend/internal/service/browser/handoff.go).
 */
import { randomUUID } from "node:crypto";
import type { IpcMain, WebContents } from "electron";
import { browserError, throwIfAborted } from "./browser-view-host/args";
import {
	BROWSER_HANDOFF_CHANGED_CHANNEL,
	BROWSER_HANDOFF_CONTINUE_CHANNEL,
	BROWSER_HANDOFF_STATE_CHANNEL,
	compileUrlGlob,
	type BrowserHandoffChange,
	type BrowserHandoffState,
} from "../shared/browser-handoff";

// Limits shared with the daemon (backend/internal/service/browser/handoff.go).
const MIN_TIMEOUT_MS = 10_000;
const MAX_TIMEOUT_MS = 30 * 60_000;
const URL_POLL_MS = 500;

/** The page a handoff waits on, from the backend that owns the session. */
export type BrowserHandoffPage = {
	/** The URL of the session's front tab now; undefined once it is gone. */
	url(): string | undefined;
	/** The person works in plain Chrome for a Google sign-in (B854). */
	signIn?: boolean;
	/** Settles when the person finished without Continue (quit or Done on the sign-in Chrome). */
	ended?: Promise<void>;
	/** Runs once the handoff ends, however it ends. */
	release?(): Promise<void>;
};

export type BrowserHandoffResult = { outcome: "continued" | "url-matched"; url: string };

type Running = { state: BrowserHandoffState; continued: () => void; abort: (error: Error) => void };

function handoffArgs(args: Record<string, unknown>): { reason: string; untilUrl?: string; timeoutMs: number } {
	const reason = typeof args.reason === "string" ? args.reason.trim() : "";
	if (!reason) throw browserError("INVALID_ARGUMENT", "reason is required");
	const timeoutMs = args.timeoutMs;
	if (typeof timeoutMs !== "number" || !Number.isInteger(timeoutMs) || timeoutMs < MIN_TIMEOUT_MS || timeoutMs > MAX_TIMEOUT_MS)
		throw browserError("INVALID_ARGUMENT", `timeoutMs must be between ${MIN_TIMEOUT_MS} and ${MAX_TIMEOUT_MS}`);
	const untilUrl = typeof args.untilUrl === "string" && args.untilUrl.trim() ? args.untilUrl.trim() : undefined;
	return { reason, untilUrl, timeoutMs };
}

export class BrowserHandoffs {
	private readonly running = new Map<string, Running>();

	constructor(
		private readonly publish: (change: BrowserHandoffChange) => void,
		private readonly now: () => number = Date.now,
	) {}

	state(sessionId: string): BrowserHandoffState | null {
		return this.running.get(sessionId)?.state ?? null;
	}

	/** The person clicked Continue on the banner of handoff id. */
	continue(sessionId: string, id: string): boolean {
		const running = this.running.get(sessionId);
		if (!running || running.state.id !== id) return false;
		running.continued();
		return true;
	}

	/**
	 * Ends every running handoff: the window that showed their banners is
	 * gone, so nobody can answer them. The agent's command fails at once and
	 * the daemon resolves its notification.
	 */
	cancelAll(): void {
		for (const running of [...this.running.values()])
			running.abort(browserError("BROWSER_TARGET_UNAVAILABLE", "The Tallpine window closed during the handoff; hand off again"));
	}

	/**
	 * Refuses a handoff that cannot start (bad arguments, canceled, one already
	 * running), before the backend prepares its page: on a Google sign-in page
	 * that opens plain Chrome and takes the agent's Chrome away (B854).
	 */
	check(sessionId: string, args: Record<string, unknown>, signal?: AbortSignal): ReturnType<typeof handoffArgs> {
		throwIfAborted(signal);
		const parsed = handoffArgs(args);
		if (this.running.has(sessionId))
			throw browserError("BROWSER_HANDOFF_ACTIVE", "This session is already waiting on the person; wait for that handoff to end");
		return parsed;
	}

	/**
	 * Waits on page until Continue, an --until-url match (checked on every
	 * poll after the start, so a page already there does not end it at once
	 * unless it navigates), or the timeout. One handoff per session at a time.
	 * The page is released however this ends, refusals included.
	 */
	async run(
		sessionId: string,
		args: Record<string, unknown>,
		page: BrowserHandoffPage,
		signal?: AbortSignal,
	): Promise<BrowserHandoffResult> {
		try {
			return await this.wait(sessionId, this.check(sessionId, args, signal), page, signal);
		} finally {
			await page.release?.().catch(() => undefined);
		}
	}

	private async wait(
		sessionId: string,
		{ reason, untilUrl, timeoutMs }: ReturnType<typeof handoffArgs>,
		page: BrowserHandoffPage,
		signal?: AbortSignal,
	): Promise<BrowserHandoffResult> {
		const matches = untilUrl ? compileUrlGlob(untilUrl) : undefined;
		const state: BrowserHandoffState = {
			id: randomUUID(), sessionId, reason, ...(untilUrl ? { untilUrl } : {}), deadline: this.now() + timeoutMs,
			...(page.signIn ? { signIn: true } : {}),
		};
		const currentUrl = () => page.url() ?? "";
		const startUrl = currentUrl();
		let poll: ReturnType<typeof setInterval> | undefined;
		let timer: ReturnType<typeof setTimeout> | undefined;
		let onAbort: (() => void) | undefined;
		try {
			return await new Promise<BrowserHandoffResult>((resolve, reject) => {
				this.running.set(sessionId, { state, continued: () => resolve({ outcome: "continued", url: currentUrl() }), abort: reject });
				this.publish({ sessionId, state });
				void page.ended?.then(() => resolve({ outcome: "continued", url: currentUrl() }));
				if (matches) {
					let lastUrl = startUrl;
					poll = setInterval(() => {
						const url = currentUrl();
						if (url === lastUrl) return;
						lastUrl = url;
						if (matches(url)) resolve({ outcome: "url-matched", url });
					}, URL_POLL_MS);
				}
				timer = setTimeout(() => {
					reject(browserError("BROWSER_HANDOFF_TIMEOUT",
						`The person did not finish within ${Math.round(timeoutMs / 1000)}s; ask them in chat or hand off again`));
				}, timeoutMs);
				onAbort = () => reject(browserError("BROWSER_COMMAND_CANCELED", "Browser command was canceled"));
				signal?.addEventListener("abort", onAbort, { once: true });
			});
		} finally {
			clearInterval(poll);
			clearTimeout(timer);
			if (onAbort) signal?.removeEventListener("abort", onAbort);
			if (this.running.get(sessionId)?.state.id === state.id) {
				this.running.delete(sessionId);
				this.publish({ sessionId, state: null });
			}
		}
	}
}

export type AppBrowserHandoffs = { handoffs: BrowserHandoffs; dispose(): void };

/** One registry per main window; the shell's Browser panel reads and answers it. */
export function createAppBrowserHandoffs(options: {
	ipcMain: Pick<IpcMain, "handle" | "removeHandler">;
	shellWebContents: Pick<WebContents, "send" | "isDestroyed">;
}): AppBrowserHandoffs {
	const { ipcMain, shellWebContents } = options;
	const handoffs = new BrowserHandoffs((change) => {
		if (!shellWebContents.isDestroyed()) shellWebContents.send(BROWSER_HANDOFF_CHANGED_CHANNEL, change);
	});
	const fromShell = (sender: unknown, value: unknown): value is string =>
		sender === shellWebContents && typeof value === "string" && value.trim() !== "";
	ipcMain.handle(BROWSER_HANDOFF_STATE_CHANNEL, (event, sessionId: unknown) =>
		fromShell(event.sender, sessionId) ? handoffs.state(sessionId) : null);
	ipcMain.handle(BROWSER_HANDOFF_CONTINUE_CHANNEL, (event, sessionId: unknown, id: unknown) =>
		fromShell(event.sender, sessionId) && typeof id === "string" ? handoffs.continue(sessionId, id) : false);
	return {
		handoffs,
		dispose: () => {
			handoffs.cancelAll();
			ipcMain.removeHandler(BROWSER_HANDOFF_STATE_CHANNEL);
			ipcMain.removeHandler(BROWSER_HANDOFF_CONTINUE_CHANNEL);
		},
	};
}
