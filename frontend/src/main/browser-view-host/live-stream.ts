import { BrowserScreencast } from "../browser-screencast";
import type { BrowserLiveSink, BrowserLiveState, BrowserRemoteInput, BrowserRemoteNavigation, BrowserRemoteTabAction } from "../browser-live-types";
import { browserError, stringArg } from "./args";
import { readNavState } from "./nav-events";
import { normalizeAgentBrowserURL } from "./url";
import { activeEntry, isBlankBrowserEntry } from "./entry-state";
import type { BrowserEntry, BrowserSessionEntry } from "./types";
import { OFFSCREEN_BOUNDS } from "./constants";
import { ensureDebugger } from "./network-capture";
import type { HostContext } from "./context";

export function createLiveStream(ctx: HostContext) {
  const { entries, hostState, options, viewIdsBySessionId } = ctx;
  const { activateTab, applySessionBounds, closeTab, ensureSessionReady, liveViewportOverrides, agentViewportOverrides, navigateEntry, openUserTab, queueNativeOperation, syncTabLifecycle, withBrowserOperation } = ctx.fns;

	// CSS-pixel size the streamed page is laid out at. Remote pointer and wheel
	// input is mapped against this, so it must match what the capture shows.
	function liveViewport(session: BrowserSessionEntry): { width: number; height: number } {
		const source = session.visible ? session.bounds : session.lastVisibleSize ?? OFFSCREEN_BOUNDS;
		return {
			width: Math.max(1, Math.round(source.width)),
			height: Math.max(1, Math.round(source.height)),
		};
	}

	// A parked native view has no layout of its own: it keeps the size the
	// desktop last showed, and a session whose panel was never opened has no
	// size at all, so nothing paints and input lands nowhere. Pin the viewport
	// while a parked page is streamed, and release it once the desktop shows it.
	function syncLiveViewport(session: BrowserSessionEntry, entry: BrowserEntry): void {
		// An explicit agent viewport owns the CDP slot until it is reset.
		if (agentViewportOverrides.has(entry)) return;
		const size = session.live && !session.visible ? liveViewport(session) : undefined;
		const key = size ? `${size.width}x${size.height}` : undefined;
		if (liveViewportOverrides.get(entry) === key) return;
		const debug = entry.view.webContents.debugger;
		if (!debug?.isAttached()) {
			if (!key) liveViewportOverrides.delete(entry);
			return;
		}
		if (key) liveViewportOverrides.set(entry, key);
		else liveViewportOverrides.delete(entry);
		const pending = size
			? debug.sendCommand("Emulation.setDeviceMetricsOverride", { ...size, deviceScaleFactor: 0, mobile: false })
			: debug.sendCommand("Emulation.clearDeviceMetricsOverride");
		void Promise.resolve(pending).catch(() => {
			if (liveViewportOverrides.get(entry) === key) liveViewportOverrides.delete(entry);
		});
	}

	function browserLiveState(session: BrowserSessionEntry): BrowserLiveState {
		const current = activeEntry(session);
		const nav = readNavState(current);
		const bounds = liveViewport(session);
		return {
			url: nav.url,
			title: nav.title,
			loading: nav.isLoading,
			canGoBack: nav.canGoBack,
			canGoForward: nav.canGoForward,
			activeTabId: session.activeTabId,
			tabs: [...session.tabs.values()].map((tab) => {
				const state = readNavState(tab);
				return { id: tab.tabId, title: state.title, url: state.url, active: tab.tabId === session.activeTabId };
			}),
			width: bounds.width,
			height: bounds.height,
		};
	}

	function emitLiveState(session: BrowserSessionEntry): BrowserLiveState {
		const state = browserLiveState(session);
		const key = JSON.stringify(state);
		if (session.live && session.live.lastStateKey !== key) {
			session.live.lastStateKey = key;
			session.live.sink.state(state);
		}
		return state;
	}

	async function stopLiveCapture(session: BrowserSessionEntry): Promise<void> {
		const live = session.live;
		if (!live) return;
		const screencast = live.screencast;
		live.screencast = undefined;
		live.tabId = undefined;
		if (screencast) await screencast.stop();
		if (!session.visible) applySessionBounds(session, activeEntry(session));
	}

	async function retargetLiveStream(session: BrowserSessionEntry): Promise<void> {
		const live = session.live;
		if (!live) return;
		const previousScreencast = live.screencast;
		live.screencast = undefined;
		if (previousScreencast) await previousScreencast.stop();
		if (session.live !== live) return;
		const entry = activeEntry(session);
		await ensureDebugger(entry);
		if (session.live !== live || activeEntry(session) !== entry) return;
		applySessionBounds(session, entry);
		const cast = new BrowserScreencast(
			entry.view.webContents.debugger!,
			(frame) => {
				if (session.live !== live || live.screencast !== cast || live.tabId !== entry.tabId) return;
				live.sink.frame(frame);
				emitLiveState(session);
			},
			(code, message) => live.sink.error(code, message),
			() => liveViewport(session),
			() => !session.visible || options.mainWindow.isMinimized?.() === true || options.mainWindow.isVisible?.() === false,
		);
		live.screencast = cast;
		live.tabId = entry.tabId;
		await cast.start();
		if (session.live !== live || live.screencast !== cast) {
			await cast.stop();
			return;
		}
		emitLiveState(session);
	}

	async function startLiveStream(sessionId: string, streamId: number, sink: BrowserLiveSink): Promise<BrowserLiveState> {
		if (!Number.isSafeInteger(streamId) || streamId <= 0) throw browserError("INVALID_ARGUMENT", "streamId is invalid");
		const session = await ensureSessionReady(sessionId);
		if (session.live) await stopLiveCapture(session);
		session.live = { streamId, sink };
		syncTabLifecycle(session);
		// Report an empty browser immediately, even before a hidden renderer
		// finishes preparing capture. The phone must not wait for a blank frame.
		try {
			emitLiveState(session);
			await retargetLiveStream(session);
			return emitLiveState(session);
		} catch (error) {
			session.live = undefined;
			if (!session.visible) applySessionBounds(session, activeEntry(session));
			throw error;
		}
	}

	function detachLiveStream(session: BrowserSessionEntry): Promise<void> | undefined {
		if (!session.live) return undefined;
		const live = session.live;
		session.live = undefined;
		syncTabLifecycle(session);
		live.tabId = undefined;
		const screencast = live.screencast;
		live.screencast = undefined;
		if (!session.visible) applySessionBounds(session, activeEntry(session));
		return screencast?.stop();
	}

	async function stopLiveStream(sessionId: string, streamId: number): Promise<void> {
		const viewId = viewIdsBySessionId.get(sessionId);
		const session = viewId ? entries.get(viewId) : undefined;
		if (!session || session.live?.streamId !== streamId) return;
		await detachLiveStream(session);
	}

	function stopAllLiveStreams(): Promise<void> {
		// Detach every sink synchronously before awaiting CDP. A replacement link
		// can connect immediately, but it must never observe stale ownership from
		// the link that just disappeared.
		const stops: Promise<void>[] = [];
		for (const session of entries.values()) {
			const stop = detachLiveStream(session);
			if (stop) stops.push(stop);
		}
		return Promise.all(stops).then(() => undefined);
	}

	function assertRemoteWritable(session: BrowserSessionEntry, scrollOnly = false): void {
		if (!session.live) throw browserError("BROWSER_REMOTE_NOT_VIEWED", "Remote browser stream is not active");
		if (session.agentBrowserCommands > 0 || session.profileSwitching) {
			throw browserError("BROWSER_REMOTE_READ_ONLY", "Browser control is temporarily read-only while the agent is active");
		}
		// Scrolling has no target to fight over, so a phone can always scroll the
		// page it is watching. Everything else yields to an active desktop user.
		if (
			!scrollOnly &&
			session.visible &&
			options.mainWindow.isFocused?.() !== false &&
			options.mainWindow.isMinimized?.() !== true &&
			hostState.lastFocusedViewId === session.viewId
		) {
			throw browserError("BROWSER_REMOTE_READ_ONLY", "Desktop browser control is active");
		}
	}

	async function handleRemoteInput(sessionId: string, input: BrowserRemoteInput): Promise<void> {
		const session = await ensureSessionReady(sessionId);
		const scrollOnly = input.kind === "wheel";
		assertRemoteWritable(session, scrollOnly);
		await queueNativeOperation(session, async () => {
			assertRemoteWritable(session, scrollOnly);
			const entry = activeEntry(session);
			await entry.ready;
			session.live?.screencast?.requestFrame();
			const bounds = liveViewport(session);
			if (input.kind === "pointer") {
				if (!Number.isFinite(input.x) || !Number.isFinite(input.y) || input.x < 0 || input.x > 1 || input.y < 0 || input.y > 1) {
					throw browserError("INVALID_ARGUMENT", "Pointer coordinates are invalid");
				}
				entry.view.webContents.sendInputEvent({
					type: input.phase === "move" ? "mouseMove" : input.phase === "down" ? "mouseDown" : "mouseUp",
					x: Math.round(input.x * Math.max(1, bounds.width - 1)),
					y: Math.round(input.y * Math.max(1, bounds.height - 1)),
					button: input.button ?? "left",
					clickCount: input.phase === "move" ? undefined : 1,
				} as never);
				return;
			}
			if (input.kind === "wheel") {
				if (!Number.isFinite(input.deltaX) || !Number.isFinite(input.deltaY)) throw browserError("INVALID_ARGUMENT", "Wheel delta is invalid");
				// Native window input depends on a visible, focused surface. CDP
				// targets this page even while its view or owning window is hidden.
				await ensureDebugger(entry);
				await entry.view.webContents.debugger!.sendCommand("Input.dispatchMouseEvent", {
					type: "mouseWheel",
					x: Math.round(bounds.width / 2),
					y: Math.round(bounds.height / 2),
					// Electron wheel deltas have the opposite sign to CDP deltas.
					deltaX: -Math.max(-2_000, Math.min(2_000, input.deltaX)),
					deltaY: -Math.max(-2_000, Math.min(2_000, input.deltaY)),
				});
				return;
			}
			if (input.kind === "text") {
				if (typeof input.value !== "string" || Buffer.byteLength(input.value, "utf8") > 16 << 10) throw browserError("INVALID_ARGUMENT", "Text input is invalid");
				await ensureDebugger(entry);
				await entry.view.webContents.debugger!.sendCommand("Input.insertText", { text: input.value });
				return;
			}
			if (input.key.length > 64 || input.code.length > 64) throw browserError("INVALID_ARGUMENT", "Key input is invalid");
			const modifiers = (input.modifiers ?? []).filter((value) => ["shift", "control", "alt", "meta"].includes(value));
			if (modifiers.length !== (input.modifiers ?? []).length || (modifiers.includes("meta") && ["q", "w"].includes(input.key.toLowerCase()))) {
				throw browserError("INVALID_ARGUMENT", "Key combination is not allowed");
			}
			entry.view.webContents.sendInputEvent({
				type: input.phase === "down" ? "keyDown" : "keyUp",
				keyCode: input.key,
				modifiers,
			} as never);
		});
	}

	async function handleRemoteNavigation(sessionId: string, input: BrowserRemoteNavigation): Promise<void> {
		const session = await ensureSessionReady(sessionId);
		assertRemoteWritable(session);
		await withBrowserOperation(session, async () => {
			const entry = activeEntry(session);
			switch (input.action) {
				case "open": {
					const raw = input.url?.trim();
					if (!raw) throw browserError("URL_REQUIRED", "url is required");
					await navigateEntry(entry, normalizeAgentBrowserURL(raw));
					break;
				}
				case "back": entry.view.webContents.goBack(); break;
				case "forward": entry.view.webContents.goForward(); break;
				case "reload": entry.view.webContents.reload(); break;
				case "stop": entry.view.webContents.stop(); break;
				default: throw browserError("INVALID_ARGUMENT", "Navigation action is unsupported");
			}
			emitLiveState(session);
		});
	}

	// The desktop panel only opens a session's preview once it is mounted. A
	// phone that starts watching first would otherwise stream an empty browser,
	// so seed its initial unused tab once. Later blank tabs belong to the user.
	async function openLivePreview(sessionId: string, url: string): Promise<void> {
		const viewId = viewIdsBySessionId.get(sessionId);
		const session = viewId ? entries.get(viewId) : undefined;
		const target = url.trim();
		if (!session?.live || !session.livePreviewPending || !target) return;
		if (session.agentBrowserCommands > 0 || session.profileSwitching) return;
		const current = activeEntry(session);
		if (!isBlankBrowserEntry(current) || current.pendingNavigation) {
			session.livePreviewPending = false;
			return;
		}
		await withBrowserOperation(session, async () => {
			const entry = activeEntry(session);
			if (!session.live || !session.livePreviewPending || !isBlankBrowserEntry(entry) || entry.pendingNavigation) return;
			session.livePreviewPending = false;
			await navigateEntry(entry, normalizeAgentBrowserURL(target));
			emitLiveState(session);
		});
	}

	async function handleRemoteTab(sessionId: string, input: BrowserRemoteTabAction): Promise<void> {
		const session = await ensureSessionReady(sessionId);
		assertRemoteWritable(session);
		switch (input.action) {
			case "new": await openUserTab(session, input.url); break;
			case "select": activateTab(session, stringArg(input as unknown as Record<string, unknown>, "tabId", "TAB_ID_REQUIRED", "tabId is required")); break;
			case "close": closeTab(session, input.tabId); break;
			default: throw browserError("INVALID_ARGUMENT", "Tab action is unsupported");
		}
		emitLiveState(session);
	}

  return {
    handleRemoteInput,
    handleRemoteNavigation,
    handleRemoteTab,
    openLivePreview,
    retargetLiveStream,
    startLiveStream,
    stopAllLiveStreams,
    stopLiveStream,
    syncLiveViewport,
  };
}
