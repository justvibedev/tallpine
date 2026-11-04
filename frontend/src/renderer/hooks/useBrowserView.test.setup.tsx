import { renderHook } from "@testing-library/react";
import { useBrowserView, type BrowserNavState } from "./useBrowserView";
import { vi } from "vitest";

export type Listener = (state: BrowserNavState) => void;

export type TabsListener = (state: import("../../main/browser-view-host").BrowserTabsState) => void;

export type DevToolsListener = (state: import("../../main/browser-view-host").BrowserDevToolsState) => void;

export type ActivityListener = (state: import("../../main/browser-view-host").BrowserAgentActivityState) => void;

export type ProfileListener = (state: import("../../shared/browser-profiles").BrowserProfileViewState) => void;

export type AnnotationStateListener = (state: import("../../shared/browser-annotations").BrowserAnnotationStatePayload) => void;


/** Mounts useBrowserView for the shared test session, active and docked unless overridden. */
export function renderBrowserView(options: Partial<Parameters<typeof useBrowserView>[0]> = {}) {
	return renderHook(() => useBrowserView({ sessionId: "sess-1", active: true, poppedOut: false, ...options }));
}

export function createSlot(rect: Partial<DOMRect> = {}) {
	const slot = document.createElement("div");
	document.body.appendChild(slot);
	slot.getBoundingClientRect = vi.fn(() => ({
		x: 12,
		y: 34,
		width: 320,
		height: 240,
		top: 34,
		right: 332,
		bottom: 274,
		left: 12,
		toJSON: () => ({}),
		...rect,
	}));
	return slot;
}


export function setupBridge() {
	const listeners = new Set<Listener>();
	const tabsListeners = new Set<TabsListener>();
	const devtoolsListeners = new Set<DevToolsListener>();
	const activityListeners = new Set<ActivityListener>();
	const profileListeners = new Set<ProfileListener>();
	const annotationStateListeners = new Set<AnnotationStateListener>();
	const bridge = {
		nativeCompositionEnabled: false,
		stateFor(viewId: string): BrowserNavState {
			return {
				viewId,
				url: "",
				title: "",
				canGoBack: false,
				canGoForward: false,
				isLoading: false,
			};
		},
		ensure: vi.fn(async (sessionId: string): Promise<BrowserNavState> => ({
			viewId: `42:${sessionId}`,
			url: "",
			title: "",
			canGoBack: false,
			canGoForward: false,
			isLoading: false,
		})),
		setBounds: vi.fn(),
		onBoundsApplied: vi.fn(() => () => undefined),
		setOverlayOpen: vi.fn(),
		navigate: vi.fn(async ({ viewId }: { viewId: string }) => bridge.stateFor(viewId)),
		clear: vi.fn(async (viewId: string) => bridge.stateFor(viewId)),
		goBack: vi.fn(async (viewId: string) => bridge.stateFor(viewId)),
		goForward: vi.fn(async (viewId: string) => bridge.stateFor(viewId)),
		reload: vi.fn(async (viewId: string) => bridge.stateFor(viewId)),
		stop: vi.fn(async (viewId: string) => bridge.stateFor(viewId)),
		getTabs: vi.fn(async (viewId: string) => ({
			viewId,
			activeTabId: "t1",
			tabs: [{ id: "t1", url: "", title: "", active: true }],
		})),
		selectTab: vi.fn(async ({ viewId, tabId }: { viewId: string; tabId: string }) => ({
			viewId,
			activeTabId: tabId,
			tabs: [
				{
					id: tabId,
					url: "http://localhost:4173/",
					title: "Selected",
					active: true,
				},
			],
		})),
		closeTab: vi.fn(async ({ viewId }: { viewId: string; tabId: string }) => ({
			viewId,
			activeTabId: "t1",
			tabs: [
				{
					id: "t1",
					url: "http://localhost:3000/",
					title: "First",
					active: true,
				},
			],
		})),
		openTab: vi.fn(async ({ viewId }: { viewId: string; url?: string }) => ({
			viewId,
			activeTabId: "t2",
			tabs: [
				{
					id: "t1",
					url: "http://localhost:3000/",
					title: "First",
					active: false,
				},
				{ id: "t2", url: "", title: "", active: true },
			],
		})),
		notifyPanelUsed: vi.fn(),
		notifyPanelBlur: vi.fn(),
		onFocusLocation: vi.fn(() => () => undefined),
		getFindState: vi.fn(async (viewId: string) => ({
			viewId, tabId: "t1", query: "", activeMatchOrdinal: 0, matches: 0, finalUpdate: true,
		})),
		findInPage: vi.fn(async ({ viewId, query }: { viewId: string; query: string }) => ({
			viewId, tabId: "t1", query, activeMatchOrdinal: 1, matches: 1, finalUpdate: true,
		})),
		stopFindInPage: vi.fn(async ({ viewId }: { viewId: string }) => ({
			viewId, tabId: "t1", query: "", activeMatchOrdinal: 0, matches: 0, finalUpdate: true,
		})),
		onFindOpen: vi.fn(() => () => undefined),
		onReopenClosedTab: vi.fn(() => () => undefined),
		onClosePanel: vi.fn(() => () => undefined),
		devtools: vi.fn(
			async ({ viewId, operation, placement }: {
				viewId: string;
				operation: "open" | "close" | "setPlacement";
				placement?: "right" | "bottom" | "left" | "undocked";
			}) => ({
				viewId,
				open: operation !== "close",
				activeTabId: "t1",
				placement: placement ?? "undocked",
			}),
		),
		getProfile: vi.fn(async (viewId: string) => ({
			viewId,
			profileId: null,
			temporary: true,
		})),
		showProfileMenu: vi.fn(),
		selectProfile: vi.fn(),
		reconnectRuntime: vi.fn(async () => undefined),
		getRuntimeState: vi.fn(async () => ({ connected: false, connecting: false, rejected: false })),
		historySuggestions: vi.fn(async () => []),
		historyFavicon: vi.fn(async () => undefined),
		captureScreenshot: vi.fn(async () => undefined),
		downloads: {
			list: vi.fn(async () => ({ downloads: [] })),
			action: vi.fn(async () => ({ downloads: [] })),
			clear: vi.fn(async () => ({ downloads: [] })),
			onChanged: vi.fn(() => () => { /* no-op test subscription */ }),
		},
		destroy: vi.fn(),
		setAnnotationMode: vi.fn(async () => undefined),
		completeAnnotation: vi.fn(async () => undefined),
		discardAnnotations: vi.fn(async () => undefined),
		annotationAction: vi.fn(async () => undefined),
		onNavState: vi.fn((listener: Listener) => {
			listeners.add(listener);
			return () => listeners.delete(listener);
		}),
		onFindState: vi.fn(() => () => undefined),
		onPageFocus: vi.fn(() => () => undefined),
		onTabsState: vi.fn((listener: TabsListener) => {
			tabsListeners.add(listener);
			return () => tabsListeners.delete(listener);
		}),
		onDevToolsState: vi.fn((listener: DevToolsListener) => {
			devtoolsListeners.add(listener);
			return () => devtoolsListeners.delete(listener);
		}),
		onAgentActivity: vi.fn((listener: ActivityListener) => {
			activityListeners.add(listener);
			return () => activityListeners.delete(listener);
		}),
		onRuntimeState: vi.fn(() => () => undefined),
		onProfileState: vi.fn((listener: ProfileListener) => {
			profileListeners.add(listener);
			return () => profileListeners.delete(listener);
		}),
		onProfileManage: vi.fn(() => () => undefined),
		onAnnotationSubmit: vi.fn(() => () => undefined),
		onAnnotationCancel: vi.fn(() => () => undefined),
		onAnnotationState: vi.fn((listener: AnnotationStateListener) => {
			annotationStateListeners.add(listener);
			return () => annotationStateListeners.delete(listener);
		}),
		emit(state: BrowserNavState) {
			listeners.forEach((listener) => listener(state));
		},
		emitTabs(state: Parameters<TabsListener>[0]) {
			tabsListeners.forEach((listener) => listener(state));
		},
		emitDevTools(state: Parameters<DevToolsListener>[0]) {
			devtoolsListeners.forEach((listener) => listener(state));
		},
		emitActivity(state: Parameters<ActivityListener>[0]) {
			activityListeners.forEach((listener) => listener(state));
		},
		emitProfile(state: Parameters<ProfileListener>[0]) {
			profileListeners.forEach((listener) => listener(state));
		},
		emitAnnotationState(state: Parameters<AnnotationStateListener>[0]) {
			annotationStateListeners.forEach((listener) => listener(state));
		},
	};
	window.tallpine = { ...window.tallpine!, browser: bridge };
	return bridge;
}


// jsdom does not implement the Fullscreen API, so `document.fullscreenElement`
// has no property descriptor to spy on. Define it directly, and clear it after
// each test so state never leaks between cases.
export function setFullscreenElement(element: Element | null): void {
	Object.defineProperty(document, "fullscreenElement", {
		configurable: true,
		get: () => element,
	});
}

/** Nav state for a loaded page in the default view. */
export function loadedNavState(url = "http://localhost:3000/"): BrowserNavState {
	return { viewId: "42:sess-1", url, title: "", canGoBack: false, canGoForward: false, isLoading: false };
}
