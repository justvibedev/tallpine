// Recovery and diagnostics for Chromium process loss (B618). After a
// system-wide GPU failure the shell window stayed blank grey until force-quit:
// nothing reloaded it and nothing was logged. A renderer crash, or a GPU
// process crash that leaves the compositor without a surface, is fixed by
// reloading the shell; a rate limit stops a crash loop from reloading forever.

export interface RecoverableContents {
	isDestroyed(): boolean;
	reload(): void;
	on(event: "render-process-gone", listener: (event: unknown, details: { reason: string; exitCode: number }) => void): unknown;
	on(event: "unresponsive" | "responsive", listener: () => void): unknown;
	removeListener(event: string, listener: (...args: never[]) => void): unknown;
}

export interface RecoverableApp {
	on(event: "child-process-gone", listener: (event: unknown, details: { type: string; reason: string; exitCode: number; name?: string; serviceName?: string }) => void): unknown;
	removeListener(event: string, listener: (...args: never[]) => void): unknown;
}

export interface ProcessRecoveryOptions {
	app: RecoverableApp;
	contents: RecoverableContents;
	log?: (message: string) => void;
	now?: () => number;
	schedule?: (fn: () => void, ms: number) => unknown;
	/** Max automatic reloads inside windowMs before giving up. */
	maxReloads?: number;
	windowMs?: number;
	delayMs?: number;
	/** True while an unsaved chat draft would make a reload of a live page prompt. */
	hasUnsavedDrafts?: () => boolean;
	retryMs?: number;
	/** After this long deferred for drafts, reload anyway: the unload prompt lets the user decide. */
	maxDraftDeferMs?: number;
}

// Exits that are not a fault: nothing to recover.
const BENIGN_REASONS = new Set(["clean-exit"]);

export function attachProcessRecovery(options: ProcessRecoveryOptions): () => void {
	const { app, contents } = options;
	const log = options.log ?? ((m: string) => console.warn(`Tallpine: ${m}`));
	const now = options.now ?? Date.now;
	const schedule = options.schedule ?? ((fn, ms) => setTimeout(fn, ms));
	const maxReloads = options.maxReloads ?? 3;
	const windowMs = options.windowMs ?? 60_000;
	const delayMs = options.delayMs ?? 500;
	const hasUnsavedDrafts = options.hasUnsavedDrafts ?? (() => false);
	const retryMs = options.retryMs ?? 5_000;
	let reloads: number[] = [];
	const maxDraftDeferMs = options.maxDraftDeferMs ?? 45_000;
	let pending = false;
	let pendingLive = false;

	// One reload is pending at a time (a GPU plus renderer loss coalesces). It is
	// deferred, never dropped: while a live page has unsaved drafts (bounded by
	// maxDraftDeferMs), or the crash-loop budget is spent (3 reloads per window),
	// it retries until it can run.
	const reloadShell = (why: string, liveRenderer: boolean) => {
		if (pending) {
			// A renderer loss overrides a deferral: the dead page cannot clear its draft risk.
			if (!liveRenderer) pendingLive = false;
			return;
		}
		pending = true;
		pendingLive = liveRenderer;
		const startedAt = now();
		let loggedDeferral = false;
		const attempt = () => {
			if (contents.isDestroyed()) {
				pending = false;
				return;
			}
			if (pendingLive && hasUnsavedDrafts() && now() - startedAt < maxDraftDeferMs) {
				if (!loggedDeferral) log(`shell reload after ${why} deferred: unsaved chat draft`);
				loggedDeferral = true;
				schedule(attempt, retryMs);
				return;
			}
			const t = now();
			reloads = reloads.filter((r) => t - r < windowMs);
			if (reloads.length >= maxReloads) {
				const wait = Math.max(reloads[0] + windowMs - t, 1);
				log(`shell reload after ${why} deferred ${wait}ms: ${reloads.length} reloads in the last ${windowMs}ms`);
				schedule(attempt, wait);
				return;
			}
			reloads.push(t);
			pending = false;
			log(`reloading shell after ${why}`);
			try {
				contents.reload();
			} catch (error) {
				log(`shell reload after ${why} failed: ${String(error)}`);
			}
		};
		schedule(attempt, delayMs);
	};

	const onRendererGone = (_e: unknown, d: { reason: string; exitCode: number }) => {
		log(`shell renderer process gone: reason=${d.reason} exitCode=${d.exitCode}`);
		if (!BENIGN_REASONS.has(d.reason)) reloadShell(`renderer ${d.reason}`, false);
	};
	const onUnresponsive = () => log("shell renderer unresponsive");
	const onResponsive = () => log("shell renderer responsive again");
	const onChildGone = (_e: unknown, d: { type: string; reason: string; exitCode: number; name?: string; serviceName?: string }) => {
		const label = d.name ?? d.serviceName ?? "";
		log(`child process gone: type=${d.type} reason=${d.reason} exitCode=${d.exitCode}${label ? ` name=${label}` : ""}`);
		// Chromium restarts the GPU process itself, but the shell's compositor
		// surface can stay blank; a reload re-attaches it.
		if (d.type === "GPU" && !BENIGN_REASONS.has(d.reason)) reloadShell(`GPU process ${d.reason}`, true);
	};

	contents.on("render-process-gone", onRendererGone);
	contents.on("unresponsive", onUnresponsive);
	contents.on("responsive", onResponsive);
	app.on("child-process-gone", onChildGone);
	return () => {
		contents.removeListener("render-process-gone", onRendererGone as never);
		contents.removeListener("unresponsive", onUnresponsive as never);
		contents.removeListener("responsive", onResponsive as never);
		app.removeListener("child-process-gone", onChildGone as never);
	};
}
