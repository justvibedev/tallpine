import type { BrowserAgentActivityState, BrowserDevToolsPlacement, BrowserDevToolsState, BrowserFindState, BrowserNavState, BrowserTabState } from "../../main/browser-view-host";
import type { BrowserAnnotationActionInput, BrowserAnnotationStatePayload } from "../../shared/browser-annotations";
import type { BrowserProfileViewState } from "../../shared/browser-profiles";
import type { UseBrowserViewOptions } from "./browser-view/browser-view-types";
import { useBrowserViewState } from "./browser-view/useBrowserViewState";
import { useBrowserViewLayoutEffects } from "./browser-view/useBrowserViewLayoutEffects";
import { useBrowserViewActions } from "./browser-view/useBrowserViewActions";
import { ClosedBrowserTab } from "./browser-view/browser-view-types";
import { EMPTY_NAV_STATE, EMPTY_FIND_STATE, EMPTY_DEVTOOLS_STATE, EMPTY_PROFILE_STATE } from "./browser-view/browser-view-registry";

export type { BrowserNavState };

export type BrowserViewModel = {
	viewId: string;
	navState: BrowserNavState;
	slotRef: (node: HTMLDivElement | null) => void;
	navigate: (url: string) => Promise<void>;
	goBack: () => Promise<void>;
	goForward: () => Promise<void>;
	reload: () => Promise<void>;
	stop: () => Promise<void>;
	findState: BrowserFindState;
	findOpenRequest: number;
	findInPage: (query: string, forward: boolean, newSession: boolean) => Promise<void>;
	stopFindInPage: (focusPage?: boolean) => Promise<void>;
	tabs: BrowserTabState[];
	activeTabId: string;
	tabNotice: string;
	selectTab: (tabId: string) => Promise<void>;
	closeTab: (tabId: string) => Promise<void>;
	openTab: (url?: string) => Promise<void>;
	openLink: (url: string) => Promise<void>;
	reorderTabs: (orderedIds: string[]) => void;
	closedTabs: ClosedBrowserTab[];
	reopenClosedTab: (tabId?: string) => Promise<void>;
	devtoolsState: BrowserDevToolsState;
	profileState: BrowserProfileViewState;
	openDevTools: () => Promise<void>;
	closeDevTools: () => Promise<void>;
	setDevToolsPlacement: (placement: BrowserDevToolsPlacement) => Promise<void>;
	agentBrowserActive: boolean;
	agentBrowserActivity: BrowserAgentActivityState | null;
	destroy: () => void;
	annotationMode: boolean;
	annotationState?: Pick<BrowserAnnotationStatePayload, "count" | "screenshotCount" | "hasDraft">;
	setAnnotationMode: (enabled: boolean) => Promise<void>;
	annotationAction?: (action: BrowserAnnotationActionInput["action"]) => Promise<void>;
};


export function useBrowserView(props: UseBrowserViewOptions): BrowserViewModel {
	const state = useBrowserViewState(props);
	const layout = useBrowserViewLayoutEffects(props, state);
	const actions = useBrowserViewActions(props, state, layout);
	const {
		agentBrowserActive,
		agentBrowserActivity,
		annotationMode,
		annotationState,
		closedTabs,
		devtoolsState,
		findOpenRequest,
		findState,
		hasNativeBrowser,
		navState,
		profileState,
		slotRef,
		tabNotice,
		tabsState,
		viewId,
	} = state;
	const {
		annotationAction,
		closeTab,
		openTab,
		reorderTabs,
		selectTab,
		setAnnotationMode,
		tabs,
		withView,
	} = layout;
	const {
		destroy,
		findInPage,
		navigate,
		openLink,
		reopenClosedTab,
		runDevtools,
		stateBelongsToSession,
		stopFindInPage,
	} = actions;

	return {
		viewId: stateBelongsToSession ? viewId : "",
		navState: stateBelongsToSession ? navState : EMPTY_NAV_STATE,
		slotRef,
		navigate,
		goBack: () => (hasNativeBrowser ? withView((id) => window.tallpine!.browser.goBack(id)) : Promise.resolve()),
		goForward: () => (hasNativeBrowser ? withView((id) => window.tallpine!.browser.goForward(id)) : Promise.resolve()),
		reload: () => (hasNativeBrowser ? withView((id) => window.tallpine!.browser.reload(id)) : Promise.resolve()),
		stop: () => (hasNativeBrowser ? withView((id) => window.tallpine!.browser.stop(id)) : Promise.resolve()),
		findState: stateBelongsToSession ? findState : EMPTY_FIND_STATE,
		findOpenRequest: stateBelongsToSession ? findOpenRequest : 0,
		findInPage,
		stopFindInPage,
		tabs: stateBelongsToSession ? tabs : [],
		activeTabId: stateBelongsToSession ? tabsState.activeTabId : "",
		tabNotice: stateBelongsToSession ? tabNotice : "",
		selectTab,
		closeTab,
		openTab,
		openLink,
		reorderTabs,
		closedTabs: stateBelongsToSession ? closedTabs : [],
		reopenClosedTab,
		devtoolsState: stateBelongsToSession ? devtoolsState : EMPTY_DEVTOOLS_STATE,
		profileState: stateBelongsToSession ? profileState : EMPTY_PROFILE_STATE,
		openDevTools: () => runDevtools("open"),
		closeDevTools: () => runDevtools("close"),
		setDevToolsPlacement: (placement) => runDevtools("setPlacement", placement),
		agentBrowserActive: stateBelongsToSession && agentBrowserActive,
		agentBrowserActivity: stateBelongsToSession ? agentBrowserActivity : null,
		destroy,
		annotationMode,
		annotationState,
		setAnnotationMode,
		annotationAction,
	};
}
