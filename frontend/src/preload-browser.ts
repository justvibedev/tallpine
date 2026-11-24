/* The Browser panel's preload bridge (window.tallpine.browser), split out of preload.ts. */
import { ipcRenderer } from "electron";
import type {
	BrowserAgentActivityState,
	BrowserDevToolsInput,
	BrowserDevToolsState,
	BrowserFindInput,
	BrowserFindState,
	BrowserFindStopInput,
	BrowserNavState,
	BrowserRect,
	BrowserRuntimeState,
	BrowserTabsState,
} from "./main/browser-view-host";
import type { ChromeSessionState } from "./shared/chrome-mode";
import type { BrowserHandoffChange, BrowserHandoffState } from "./shared/browser-handoff";
import type {
	BrowserAnnotationActionInput,
	BrowserAnnotationCancelPayload,
	BrowserAnnotationCompleteInput,
	BrowserAnnotationDiscardInput,
	BrowserAnnotationModeInput,
	BrowserAnnotationStatePayload,
	BrowserAnnotationSubmitPayload,
} from "./shared/browser-annotations";
import type {
	BrowserProfileMenuInput,
	BrowserProfileSelectInput,
	BrowserProfileViewState,
} from "./shared/browser-profiles";
import type {
	BrowserHistorySuggestion,
} from "./shared/browser-profile-import";
import type { BrowserSitePermissionResetInput, BrowserSitePermissionRespondInput } from "./shared/browser-site-permissions";
import type {
	BrowserDownloadActionInput,
	BrowserDownloadsState,
} from "./shared/browser-downloads";

export type BrowserBoundsInput = {
	viewId: string;
	revision: number;
	rect: BrowserRect;
	visible: boolean;
};

export type BrowserBoundsApplied = BrowserBoundsInput;

export type BrowserNavigateInput = {
	viewId: string;
	url: string;
};

export const browserBridge = {
		reconnectRuntime: () => ipcRenderer.invoke("browser:runtime:reconnect") as Promise<void>,
		getRuntimeState: () => ipcRenderer.invoke("browser:runtime:state") as Promise<BrowserRuntimeState>,
		nativeCompositionEnabled: true,
		ensure: (sessionId: string, createInitialTab = true) => ipcRenderer.invoke("browser:ensure", sessionId, createInitialTab) as Promise<BrowserNavState>,
		setBounds: (input: BrowserBoundsInput) => ipcRenderer.send("browser:setBounds", input),
		onBoundsApplied: (listener: (result: BrowserBoundsApplied) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, result: BrowserBoundsApplied) => listener(result);
			ipcRenderer.on("browser:boundsApplied", wrapped);
			return () => {
				ipcRenderer.off("browser:boundsApplied", wrapped);
			};
		},
		setOverlayOpen: (open: boolean) => ipcRenderer.send("browser:overlay", open),
		navigate: (input: BrowserNavigateInput) =>
			ipcRenderer.invoke("browser:navigate", input) as Promise<BrowserNavState>,
		historySuggestions: (input: { viewId: string; query: string }) =>
			ipcRenderer.invoke("browser:history:suggest", input) as Promise<BrowserHistorySuggestion[]>,
		historyFavicon: (input: { viewId: string; url: string }) =>
			ipcRenderer.invoke("browser:history:favicon", input) as Promise<string | undefined>,
		clear: (viewId: string) => ipcRenderer.invoke("browser:clear", viewId) as Promise<BrowserNavState>,
		goBack: (viewId: string) => ipcRenderer.invoke("browser:goBack", viewId) as Promise<BrowserNavState>,
		goForward: (viewId: string) => ipcRenderer.invoke("browser:goForward", viewId) as Promise<BrowserNavState>,
		reload: (viewId: string) => ipcRenderer.invoke("browser:reload", viewId) as Promise<BrowserNavState>,
		stop: (viewId: string) => ipcRenderer.invoke("browser:stop", viewId) as Promise<BrowserNavState>,
		getFindState: (viewId: string) =>
			ipcRenderer.invoke("browser:find:get", viewId) as Promise<BrowserFindState>,
		findInPage: (input: BrowserFindInput) =>
			ipcRenderer.invoke("browser:find", input) as Promise<BrowserFindState>,
		stopFindInPage: (input: BrowserFindStopInput) =>
			ipcRenderer.invoke("browser:find:stop", input) as Promise<BrowserFindState>,
		captureScreenshot: (viewId: string) => ipcRenderer.invoke("browser:captureScreenshot", viewId) as Promise<void>,
		downloads: {
			list: () => ipcRenderer.invoke("browser:downloads:list") as Promise<BrowserDownloadsState>,
			action: (input: BrowserDownloadActionInput) =>
				ipcRenderer.invoke("browser:downloads:action", input) as Promise<BrowserDownloadsState>,
			clear: () => ipcRenderer.invoke("browser:downloads:clear") as Promise<BrowserDownloadsState>,
			onChanged: (listener: (state: BrowserDownloadsState) => void) => {
				const wrapped = (_event: Electron.IpcRendererEvent, state: BrowserDownloadsState) => listener(state);
				ipcRenderer.on("browser:downloadsChanged", wrapped);
				return () => {
					ipcRenderer.off("browser:downloadsChanged", wrapped);
				};
			},
		},
		getTabs: (viewId: string) => ipcRenderer.invoke("browser:getTabs", viewId) as Promise<BrowserTabsState>,
		selectTab: (input: { viewId: string; tabId: string }) =>
			ipcRenderer.invoke("browser:selectTab", input) as Promise<BrowserTabsState>,
		closeTab: (input: { viewId: string; tabId: string }) =>
			ipcRenderer.invoke("browser:closeTab", input) as Promise<BrowserTabsState>,
		openTab: (input: { viewId: string; url?: string }) =>
			ipcRenderer.invoke("browser:openTab", input) as Promise<BrowserTabsState>,
		getProfile: (viewId: string) =>
			ipcRenderer.invoke("browser:profile:get", viewId) as Promise<BrowserProfileViewState>,
		showProfileMenu: (input: BrowserProfileMenuInput) =>
			ipcRenderer.invoke("browser:profile:menu", input) as Promise<void>,
		selectProfile: (input: BrowserProfileSelectInput) =>
			ipcRenderer.invoke("browser:profile:select", input) as Promise<void>,
		notifyPanelUsed: (viewId: string) => ipcRenderer.send("browser:panelUsed", viewId),
		notifyPanelBlur: (viewId: string) => ipcRenderer.send("browser:panelBlur", viewId),
		onFocusLocation: (listener: (viewId: string) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, viewId: string) => listener(viewId);
			ipcRenderer.on("browser:focusLocation", wrapped);
			return () => {
				ipcRenderer.off("browser:focusLocation", wrapped);
			};
		},
		onFindOpen: (listener: (state: BrowserFindState) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, state: BrowserFindState) => listener(state);
			ipcRenderer.on("browser:findOpen", wrapped);
			return () => {
				ipcRenderer.off("browser:findOpen", wrapped);
			};
		},
		onReopenClosedTab: (listener: (viewId: string) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, viewId: string) => listener(viewId);
			ipcRenderer.on("browser:reopenClosedTab", wrapped);
			return () => {
				ipcRenderer.off("browser:reopenClosedTab", wrapped);
			};
		},
		onClosePanel: (listener: (viewId: string) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, viewId: string) => listener(viewId);
			ipcRenderer.on("browser:closePanel", wrapped);
			return () => {
				ipcRenderer.off("browser:closePanel", wrapped);
			};
		},
		respondSitePermission: (input: BrowserSitePermissionRespondInput) =>
			ipcRenderer.invoke("browser:sitePermission:respond", input) as Promise<void>,
		resetSitePermission: (input: BrowserSitePermissionResetInput) =>
			ipcRenderer.invoke("browser:sitePermission:reset", input) as Promise<void>,
		devtools: (input: BrowserDevToolsInput) =>
			ipcRenderer.invoke("browser:devtools", input) as Promise<BrowserDevToolsState>,
		destroy: (viewId: string) => ipcRenderer.send("browser:destroy", viewId),
		setAnnotationMode: (input: BrowserAnnotationModeInput) =>
			ipcRenderer.invoke("browser:annotation:setMode", input) as Promise<void>,
		completeAnnotation: (input: BrowserAnnotationCompleteInput) =>
			ipcRenderer.invoke("browser:annotation:complete", input) as Promise<void>,
		discardAnnotations: (input: BrowserAnnotationDiscardInput) =>
			ipcRenderer.invoke("browser:annotation:discard", input) as Promise<void>,
		annotationAction: (input: BrowserAnnotationActionInput) =>
			ipcRenderer.invoke("browser:annotation:action", input) as Promise<void>,
		onNavState: (listener: (state: BrowserNavState) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, state: BrowserNavState) => listener(state);
			ipcRenderer.on("browser:navState", wrapped);
			return () => {
				ipcRenderer.off("browser:navState", wrapped);
			};
		},
		onFindState: (listener: (state: BrowserFindState) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, state: BrowserFindState) => listener(state);
			ipcRenderer.on("browser:findState", wrapped);
			return () => {
				ipcRenderer.off("browser:findState", wrapped);
			};
		},
		onPageFocus: (listener: (viewId: string) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, viewId: string) => listener(viewId);
			ipcRenderer.on("browser:pageFocus", wrapped);
			return () => {
				ipcRenderer.off("browser:pageFocus", wrapped);
			};
		},
		onTabsState: (listener: (state: BrowserTabsState) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, state: BrowserTabsState) => listener(state);
			ipcRenderer.on("browser:tabsState", wrapped);
			return () => {
				ipcRenderer.off("browser:tabsState", wrapped);
			};
		},
		onAgentActivity: (listener: (state: BrowserAgentActivityState) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, state: BrowserAgentActivityState) => listener(state);
			ipcRenderer.on("browser:agentActivity", wrapped);
			return () => {
				ipcRenderer.off("browser:agentActivity", wrapped);
			};
		},
		onRuntimeState: (listener: (state: BrowserRuntimeState) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, state: BrowserRuntimeState) => listener(state);
			ipcRenderer.on("browser:runtimeState", wrapped);
			return () => {
				ipcRenderer.off("browser:runtimeState", wrapped);
			};
		},
		// Real Chrome mode (B849): the session's Chrome tab for the panel's card.
		chromeState: (sessionId: string) => ipcRenderer.invoke("browser:chromeState", sessionId) as Promise<ChromeSessionState>,
		chromeShow: (sessionId: string) => ipcRenderer.invoke("browser:chromeShow", sessionId) as Promise<{ shown: boolean }>,
		// B854: plain Chrome on the same profile for signing in, then back under agent control.
		chromeSignIn: (sessionId: string) => ipcRenderer.invoke("browser:chromeSignIn", sessionId) as Promise<ChromeSessionState>,
		chromeSignInDone: (sessionId: string) => ipcRenderer.invoke("browser:chromeSignInDone", sessionId) as Promise<ChromeSessionState>,
		onChromeStateChanged: (listener: (change: { sessionId: string; state: ChromeSessionState }) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, change: { sessionId: string; state: ChromeSessionState }) => listener(change);
			ipcRenderer.on("browser:chromeStateChanged", wrapped);
			return () => {
				ipcRenderer.off("browser:chromeStateChanged", wrapped);
			};
		},
		// B858: an agent waits on the person; the panel shows the banner and answers Continue.
		handoffState: (sessionId: string) => ipcRenderer.invoke("browser:handoffState", sessionId) as Promise<BrowserHandoffState | null>,
		handoffContinue: (sessionId: string, id: string) => ipcRenderer.invoke("browser:handoffContinue", sessionId, id) as Promise<boolean>,
		onHandoffChanged: (listener: (change: BrowserHandoffChange) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, change: BrowserHandoffChange) => listener(change);
			ipcRenderer.on("browser:handoffChanged", wrapped);
			return () => {
				ipcRenderer.off("browser:handoffChanged", wrapped);
			};
		},
		onDevToolsState: (listener: (state: BrowserDevToolsState) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, state: BrowserDevToolsState) => listener(state);
			ipcRenderer.on("browser:devtoolsState", wrapped);
			return () => {
				ipcRenderer.off("browser:devtoolsState", wrapped);
			};
		},
		onProfileState: (listener: (state: BrowserProfileViewState) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, state: BrowserProfileViewState) => listener(state);
			ipcRenderer.on("browser:profileState", wrapped);
			return () => {
				ipcRenderer.off("browser:profileState", wrapped);
			};
		},
		onProfileManage: (listener: (viewId: string) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, payload: { viewId?: unknown }) => {
				if (typeof payload?.viewId === "string") listener(payload.viewId);
			};
			ipcRenderer.on("browser:profileManage", wrapped);
			return () => {
				ipcRenderer.off("browser:profileManage", wrapped);
			};
		},
		onAnnotationSubmit: (listener: (payload: BrowserAnnotationSubmitPayload) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, payload: BrowserAnnotationSubmitPayload) => listener(payload);
			ipcRenderer.on("browser:annotation:submitted", wrapped);
			return () => {
				ipcRenderer.off("browser:annotation:submitted", wrapped);
			};
		},
		onAnnotationCancel: (listener: (payload: BrowserAnnotationCancelPayload) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, payload: BrowserAnnotationCancelPayload) => listener(payload);
			ipcRenderer.on("browser:annotation:canceled", wrapped);
			return () => {
				ipcRenderer.off("browser:annotation:canceled", wrapped);
			};
		},
		onAnnotationState: (listener: (payload: BrowserAnnotationStatePayload) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, payload: BrowserAnnotationStatePayload) => listener(payload);
			ipcRenderer.on("browser:annotation:state", wrapped);
			return () => {
				ipcRenderer.off("browser:annotation:state", wrapped);
			};
		},
};
