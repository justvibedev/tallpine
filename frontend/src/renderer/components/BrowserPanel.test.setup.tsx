import { render as rtlRender } from "@testing-library/react";
import type { ComponentProps, ReactElement } from "react";
import { vi } from "vitest";
import { BrowserPanel, BrowserPanelView, useBrowserAnnotationQueue } from "./BrowserPanel";
import { useBrowserView, type BrowserNavState } from "../hooks/useBrowserView";
import type { WorkspaceSession } from "../types/workspace";
import { TooltipProvider } from "./ui/tooltip";
import type { BrowserAnnotationSubmitPayload } from "../../shared/browser-annotations";
import type { BrowserRuntimeState } from "../../main/browser-view-host";

export function render(ui: ReactElement) {
	return rtlRender(<TooltipProvider>{ui}</TooltipProvider>);
}

type PanelProps = Partial<ComponentProps<typeof BrowserPanel>>;

/** The BrowserPanel under test with the props every test shares; override per case. */
export function panelElement(props: PanelProps = {}) {
	return <BrowserPanel active onTogglePopOut={() => undefined} poppedOut={false} session={session} {...props} />;
}

/** Rerender with the same TooltipProvider wrapper that render() adds. */
export function rerenderPanel(rerender: (ui: ReactElement) => void, props: PanelProps = {}) {
	rerender(<TooltipProvider>{panelElement(props)}</TooltipProvider>);
}

export function renderPanel(props: PanelProps = {}) {
	return render(panelElement(props));
}


const postMock = vi.hoisted(() => vi.fn());


vi.mock("../lib/api-client", () => ({
	apiClient: { POST: postMock },
	getApiBaseUrl: () => "http://127.0.0.1:3101",
	apiErrorMessage: (error: unknown, fallback = "Request failed") =>
		typeof error === "object" && error !== null && "message" in error
			? String((error as { message: unknown }).message)
			: fallback,
}));


vi.mock("../lib/host-clients", () => ({ clientForSessionHost: () => ({ POST: postMock }) }));


const hookState = vi.hoisted(() => ({
	navigate: vi.fn(),
	goBack: vi.fn(),
	goForward: vi.fn(),
	reload: vi.fn(),
	stop: vi.fn(),
	findInPage: vi.fn(),
	stopFindInPage: vi.fn(),
	findOpenRequest: 0,
	findState: { viewId: "42:sess-1", tabId: "t1", query: "", activeMatchOrdinal: 0, matches: 0, finalUpdate: true },
	selectTab: vi.fn(),
	closeTab: vi.fn(),
	openTab: vi.fn(),
	reorderTabs: vi.fn(),
	closedTabs: [] as { id: string; title: string; url: string; favicon?: string }[],
	reopenClosedTab: vi.fn(),
	openDevTools: vi.fn(),
	closeDevTools: vi.fn(),
	reconnectRuntime: vi.fn(),
	devtoolsState: { viewId: "42:sess-1", open: false, activeTabId: "t1" },
	setAnnotationMode: vi.fn(),
	annotationAction: vi.fn(),
	annotationMode: false,
	annotationState: { count: 0, screenshotCount: 0, hasDraft: false },
	runtimeState: { connected: true, connecting: false, rejected: false } as BrowserRuntimeState | null,
	tabs: [{ id: "t1", url: "", title: "", active: true }],
	activeTabId: "t1",
	tabNotice: "",
	agentBrowserActive: false,
	agentBrowserActivity: null as { active: boolean; action?: string; phase?: "started" | "finished" } | null,
	profileState: { viewId: "42:sess-1", profileId: null as string | null, temporary: true },
	previewUrl: undefined as string | undefined,
	navState: {
		viewId: "42:sess-1",
		url: "",
		title: "",
		canGoBack: false,
		canGoForward: false,
		isLoading: false,
	} as BrowserNavState,
}));


vi.mock("../hooks/useBrowserRuntimeState", () => ({
	useBrowserRuntimeState: () => hookState.runtimeState,
}));


vi.mock("../hooks/useBrowserView", () => ({
	useBrowserView: (options: { previewUrl?: string }) => {
		hookState.previewUrl = options.previewUrl;
		return {
			viewId: "42:sess-1",
			navState: hookState.navState,
			slotRef: vi.fn(),
			navigate: hookState.navigate,
			goBack: hookState.goBack,
			goForward: hookState.goForward,
			reload: hookState.reload,
			stop: hookState.stop,
			findInPage: hookState.findInPage,
			stopFindInPage: hookState.stopFindInPage,
			findOpenRequest: hookState.findOpenRequest,
			findState: hookState.findState,
			tabs: hookState.tabs,
			activeTabId: hookState.activeTabId,
			tabNotice: hookState.tabNotice,
			selectTab: hookState.selectTab,
			closeTab: hookState.closeTab,
			openTab: hookState.openTab,
			reorderTabs: hookState.reorderTabs,
			closedTabs: hookState.closedTabs,
			reopenClosedTab: hookState.reopenClosedTab,
			agentBrowserActive: hookState.agentBrowserActive,
			agentBrowserActivity: hookState.agentBrowserActivity,
			profileState: hookState.profileState,
			devtoolsState: hookState.devtoolsState,
			openDevTools: hookState.openDevTools,
			closeDevTools: hookState.closeDevTools,
			annotationMode: hookState.annotationMode,
			annotationState: hookState.annotationState,
			setAnnotationMode: hookState.setAnnotationMode,
			annotationAction: hookState.annotationAction,
		};
	},
}));


export const session: WorkspaceSession = {
	id: "sess-1",
	workspaceId: "ws-1",
	workspaceName: "my-app",
	title: "do the thing",
	provider: "claude-code",
	kind: "worker",
	branch: "feat/ns",
	status: "needs_input",
	updatedAt: "2026-06-15T00:00:00Z",
	prs: [],
};


export function annotationPayload(
	body: string,
	options: { selector?: string; tag?: string; width?: number; height?: number } = {},
): BrowserAnnotationSubmitPayload {
	const selector = options.selector ?? "button";
	const tag = options.tag ?? "button";
	const width = options.width ?? 80;
	const height = options.height ?? 30;
	const now = "2026-06-15T00:00:00Z";
	return {
		viewId: "42:sess-1",
		tabId: "t1",
		pageKey: "http://localhost:5173/",
		sessionToken: "annotation-session-1",
		session: {
			version: 1,
			page: { url: "http://localhost:5173/", title: "Preview" },
			annotations: [
				{
					id: "annotation-1",
					number: 1,
					kind: "comment",
					body,
					target: {
						context: {
							url: "http://localhost:5173/",
							title: "Preview",
							tag,
							classes: [],
							selector,
							size: { width, height },
							rect: { x: 10, y: 20, width, height },
							computedStyle: {},
						},
					},
					adjustments: [],
					createdAt: now,
					updatedAt: now,
				},
			],
			screenshots: [],
		},
	};
}


export function PersistentBrowserPanelView({
	currentSession,
	visible,
}: {
	currentSession: WorkspaceSession;
	visible: boolean;
}) {
	const browserView = useBrowserView({
		sessionId: currentSession.id,
		active: true,
		poppedOut: false,
		previewUrl: currentSession.previewUrl,
		previewRevision: currentSession.previewRevision,
	});
	const annotationQueue = useBrowserAnnotationQueue({
		sessionId: currentSession.id,
		navUrl: browserView.navState.url,
	});
	if (!visible) return null;
	return (
		<BrowserPanelView
			active
			annotationQueue={annotationQueue}
			browserView={browserView}
			onTogglePopOut={() => undefined}
			poppedOut={false}
			session={currentSession}
		/>
	);
}


export { postMock, hookState };
