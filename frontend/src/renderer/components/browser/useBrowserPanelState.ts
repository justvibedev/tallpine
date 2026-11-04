import { useCallback, useEffect, useId, useRef, useState, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import { useReducedMotion } from "motion/react";
import { KeyboardSensor, PointerSensor, useSensor, useSensors, type DragEndEvent } from "@dnd-kit/core";
import { sortableKeyboardCoordinates } from "@dnd-kit/sortable";
import { useTabScrollEdges } from "../../hooks/useTabScrollEdges";
import type { BrowserProfile } from "../../../shared/browser-profiles";
import { useUiStore } from "../../stores/ui-store";
import { reorderBrowserTabs } from "../../lib/browser-tab-order";
import { useBrowserDownloads } from "../../hooks/useBrowserDownloads";
import { isHostLocalWebLink } from "../../lib/loopback";
import { BrowserPanelViewProps } from "./browser-types";
import { CUSTOM_DEVICE_PRESET_ID, clampDeviceFrameWidth, DEVICE_PRESETS } from "./browser-constants";

/** Tabs, device frame, zoom and agent activity state for the browser panel. */
export function useBrowserPanelState(props: BrowserPanelViewProps) {
	const {
		active,
		browserView,
		annotationQueue,
	} = props;
	const { t } = useTranslation();
	const prefersReducedMotion = useReducedMotion();
	const {
		viewId,
		navState,
		slotRef,
		navigate,
		goBack,
		goForward,
		reload,
		stop,
		findState = { viewId: "", tabId: "", query: "", activeMatchOrdinal: 0, matches: 0, finalUpdate: true },
		findOpenRequest = 0,
		findInPage = async () => undefined,
		stopFindInPage = async () => undefined,
		tabs,
		activeTabId,
		tabNotice,
		selectTab,
		closeTab,
		openTab,
		reorderTabs,
		closedTabs,
		reopenClosedTab,
		agentBrowserActive,
		agentBrowserActivity,
		devtoolsState = { viewId: "", open: false, activeTabId: "" },
		profileState = { viewId: "", profileId: null, temporary: true },
		openDevTools = async () => undefined,
		closeDevTools = async () => undefined,
		annotationMode,
		annotationState = { count: 0, screenshotCount: 0, hasDraft: false },
		setAnnotationMode,
		annotationAction = async () => undefined,
	} = browserView;
	const [urlInput, setUrlInput] = useState(navState.url);
	const [urlCopied, setUrlCopied] = useState(false);
	const [historySuggestions, setHistorySuggestions] = useState<Array<{ url: string; title?: string }>>([]);
	const historyMenuId = useId();
	const [activeHistorySuggestion, setActiveHistorySuggestion] = useState(-1);
	const [urlEditing, setUrlEditing] = useState(false);
	const { beginPicking, cancelPicking, enqueue, error, failPicking, queuedCount, retryQueued, status } =
		annotationQueue;
	const hasNativeBrowser = Boolean(window.tallpine?.browser);
	const showStaticPreview = !hasNativeBrowser && navState.url !== "";
	// Not on an agent tab: its page is its viewer's picture of the agent's page.
	const onAgentTab = Boolean(tabs.find((tab) => tab.active)?.remote);
	const canAnnotate = Boolean(window.tallpine?.browser && viewId && navState.url && !onAgentTab);
	// An agent tab's localhost page is the host's: opened here it would be this
	// computer's own. Before the viewer's first report the tab shows the viewer
	// itself, an ao-preview-….localhost address, so that is held back too.
	const agentPageOnHost = onAgentTab && isHostLocalWebLink(navState.url);
	const canRetryAnnotation = status === "error" && queuedCount > 0;
	const [devicePreset, setDevicePreset] = useState<string | null>(null);
	const [customDeviceWidth, setCustomDeviceWidth] = useState("390");
	const customWidthInputRef = useRef<HTMLInputElement>(null);
	const [controlsView, setControlsView] = useState<"root" | "devices" | "profiles">("root");
	const [controlsOpen, setControlsOpen] = useState(false);
	const [runtimeReconnectPending, setRuntimeReconnectPending] = useState(false);
	const [browserProfiles, setBrowserProfiles] = useState<BrowserProfile[]>([]);
	const [profilesLoading, setProfilesLoading] = useState(false);
	const openGlobalSettings = useUiStore((state) => state.openGlobalSettings);
	const deviceFrameWidth =
		devicePreset === CUSTOM_DEVICE_PRESET_ID
			? clampDeviceFrameWidth(Number(customDeviceWidth))
			: DEVICE_PRESETS.find((preset) => preset.id === devicePreset)?.width;
	const urlInputRef = useRef<HTMLInputElement>(null);
	const findInputRef = useRef<HTMLInputElement>(null);
	const findComposingRef = useRef(false);
	const findStateRef = useRef(findState);
	findStateRef.current = findState;
	const [findOpen, setFindOpen] = useState(false);
	const findOpenRef = useRef(findOpen);
	findOpenRef.current = findOpen;
	const [findQuery, setFindQuery] = useState("");
	const historyMenuRef = useRef<HTMLDivElement>(null);
	const historyRequestGenerationRef = useRef(0);
	const copyFeedbackTimeoutRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
	const [draggedTopTabId, setDraggedTopTabId] = useState<string | null>(null);
	const draggedTopTab = tabs.find((tab) => tab.id === draggedTopTabId);
	const {
		scrollRef: tabScrollRef,
		scrollToEnd: scrollTabsToEnd,
		showLeftFade: showTabsLeftFade,
		showRightFade: showTabsRightFade,
	} = useTabScrollEdges([tabs.length]);
	const previousTabCountRef = useRef(tabs.length);

	// Opening a tab (or a popup adding one) appends it, so reveal the newest.
	useEffect(() => {
		if (tabs.length > previousTabCountRef.current) scrollTabsToEnd();
		previousTabCountRef.current = tabs.length;
	}, [tabs.length, scrollTabsToEnd]);

	// Keep the active tab visible when selection changes (click, keyboard, or a
	// close that shifts activation), unless a drag is positioning it.
	useEffect(() => {
		if (draggedTopTabId) return;
		const region = tabScrollRef.current;
		if (!region) return;
		const activeTab = Array.from(region.querySelectorAll<HTMLElement>("[data-browser-tab-id]")).find(
			(element) => element.dataset.browserTabId === activeTabId,
		);
		if (!activeTab) return;
		const regionRect = region.getBoundingClientRect();
		const tabRect = activeTab.getBoundingClientRect();
		let nextScrollLeft = region.scrollLeft;
		if (tabRect.left < regionRect.left) nextScrollLeft -= regionRect.left - tabRect.left;
		if (tabRect.right > regionRect.right) nextScrollLeft += tabRect.right - regionRect.right;
		if (nextScrollLeft === region.scrollLeft) return;
		region.scrollTo({ behavior: "smooth", left: Math.max(0, nextScrollLeft) });
	}, [activeTabId, tabs.length, draggedTopTabId, tabScrollRef]);

	useEffect(() => {
		if (controlsView !== "profiles" || !window.tallpine?.browserProfiles) return;
		let canceled = false;
		setProfilesLoading(true);
		void window.tallpine.browserProfiles
			.list()
			.then((state) => {
				if (!canceled) setBrowserProfiles(state.profiles);
			})
			.catch(() => {
				if (!canceled) setBrowserProfiles([]);
			})
			.finally(() => {
				if (!canceled) setProfilesLoading(false);
			});
		return () => {
			canceled = true;
		};
	}, [controlsView]);

	const selectBrowserProfile = useCallback(
		(profileId: string | null) => {
			if (!viewId || !window.tallpine?.browser) return;
			void window.tallpine.browser.selectProfile({
				viewId,
				profileId,
				labels: {
					temporary: t("browser.profile.temporary"),
					manage: t("browser.profile.manage"),
					switchTitle: t("browser.profile.switchTitle"),
					switchMessage: t("browser.profile.switchMessage"),
					switchDetail: t("browser.profile.switchDetail"),
					cancel: t("common.no"),
					confirm: t("common.yes"),
				},
			});
		},
		[t, viewId],
	);

	useEffect(() => {
		if (!viewId) return;
		if (active) window.tallpine?.browser.notifyPanelUsed(viewId);
		else window.tallpine?.browser.notifyPanelBlur(viewId);
		return () => window.tallpine?.browser.notifyPanelBlur(viewId);
	}, [active, viewId]);

	useEffect(
		() =>
			window.tallpine?.browser.onFocusLocation((targetViewId) => {
				if (targetViewId !== viewId) return;
				// ⌘T/Ctrl+T focuses the omnibox after opening a tab. When the bar is
				// portaled into the inspector header it sits outside the panel focus
				// boundary, so restore the browser shortcut target before the input
				// takes focus — otherwise the next ⌘T/⌘W falls through to terminal
				// shortcuts and yank focus to the main pane.
				window.tallpine?.browser.notifyPanelUsed(viewId);
				if (document.activeElement === urlInputRef.current) {
					return;
				}
				urlInputRef.current?.focus();
				urlInputRef.current?.select();
			}),
		[viewId],
	);
	useEffect(() => {
		if (findOpenRequest <= 0) return;
		if (findOpenRef.current) {
			setFindOpen(false);
			setFindQuery("");
			void stopFindInPage(true);
			return;
		}
		setFindOpen(true);
		setFindQuery(findStateRef.current.query);
		requestAnimationFrame(() => {
			findInputRef.current?.focus();
			findInputRef.current?.select();
		});
	}, [findOpenRequest, stopFindInPage]);
	useEffect(() => {
		if (!findOpen || findState.tabId !== activeTabId) return;
		setFindQuery(findState.query);
	}, [activeTabId, findOpen, findState.query, findState.tabId]);
	const closeFind = useCallback(() => {
		setFindOpen(false);
		setFindQuery("");
		void stopFindInPage(true);
	}, [stopFindInPage]);
	const runFind = useCallback(
		(query: string, forward = true, newSession = true) => {
			void findInPage(query, forward, newSession);
		},
		[findInPage],
	);
	const handleFindKeyDown = useCallback(
		(event: KeyboardEvent<HTMLInputElement>) => {
			if (event.key === "Escape") {
				event.preventDefault();
				closeFind();
				return;
			}
			if (event.key !== "Enter" || findComposingRef.current || !findQuery) return;
			event.preventDefault();
			runFind(findQuery, !event.shiftKey, false);
		},
		[closeFind, findQuery, runFind],
	);
	useEffect(
		() =>
			window.tallpine?.browser.onReopenClosedTab((targetViewId) => {
				if (targetViewId !== viewId) return;
				void reopenClosedTab();
			}),
		[reopenClosedTab, viewId],
	);

	const tabSensors = useSensors(
		useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
		useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
	);
	const handleTopTabDragEnd = useCallback(
		(event: DragEndEvent) => {
			setDraggedTopTabId(null);
			if (!event.over) return;
			const orderedIds = reorderBrowserTabs(
				tabs.map((tab) => tab.id),
				String(event.active.id),
				String(event.over.id),
			);
			if (orderedIds) reorderTabs(orderedIds);
		},
		[reorderTabs, tabs],
	);

	// Docked DevTools belongs to the native page view, which is intentionally
	// hidden while the active target is blank. Keep close available for any
	// in-flight state update, but do not offer an open action with no page.
	const canUseDevTools = hasNativeBrowser && Boolean(viewId) && Boolean(navState.url || devtoolsState.open);
	const canTakeScreenshot = hasNativeBrowser && Boolean(viewId) && Boolean(navState.url);
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const browserDownloads = useBrowserDownloads();
	const [downloadsOpen, setDownloadsOpen] = useState(false);
	const knownDownloadIds = useRef<Set<string> | null>(null);
	const observedInitialDownloads = useRef(false);
	const hasActiveDownload = browserDownloads.downloads.some(
		(download) => download.status === "progressing" || download.status === "paused" || download.status === "blocked",
	);
	useEffect(() => {
		if (!browserDownloads.initialized) return;
		const nextIds = new Set(browserDownloads.downloads.map((download) => download.id));
		if (!observedInitialDownloads.current) {
			observedInitialDownloads.current = true;
			knownDownloadIds.current = nextIds;
			return;
		}
		const previousIds = knownDownloadIds.current;
		const hasNewDownload = previousIds
			? browserDownloads.downloads.some((download) => !previousIds.has(download.id))
			: false;
		if (active && hasNewDownload) setDownloadsOpen(true);
		knownDownloadIds.current = nextIds;
	}, [active, browserDownloads.downloads, browserDownloads.initialized]);

	return {
		annotationState,
		devtoolsState,
		findState,
		profileState,
		activeHistorySuggestion,
		activeTabId,
		agentBrowserActive,
		agentBrowserActivity,
		agentPageOnHost,
		annotationAction,
		annotationMode,
		beginPicking,
		browserDownloads,
		browserProfiles,
		canAnnotate,
		canRetryAnnotation,
		canTakeScreenshot,
		canUseDevTools,
		cancelPicking,
		closeDevTools,
		closeFind,
		closeTab,
		closedTabs,
		controlsOpen,
		controlsView,
		copyFeedbackTimeoutRef,
		customDeviceWidth,
		customWidthInputRef,
		deviceFrameWidth,
		devicePreset,
		downloadsOpen,
		draggedTopTab,
		draggedTopTabId,
		enqueue,
		error,
		failPicking,
		findComposingRef,
		findInputRef,
		findOpen,
		findQuery,
		goBack,
		goForward,
		handleFindKeyDown,
		handleTopTabDragEnd,
		hasActiveDownload,
		hasNativeBrowser,
		historyMenuId,
		historyMenuRef,
		historyRequestGenerationRef,
		historySuggestions,
		navState,
		navigate,
		onAgentTab,
		openDevTools,
		openGlobalSettings,
		openTab,
		prefersReducedMotion,
		profilesLoading,
		queuedCount,
		reload,
		reopenClosedTab,
		retryQueued,
		runFind,
		runtimeReconnectPending,
		selectBrowserProfile,
		selectTab,
		setActiveHistorySuggestion,
		setAnnotationMode,
		setControlsOpen,
		setControlsView,
		setCustomDeviceWidth,
		setDevicePreset,
		setDownloadsOpen,
		setDraggedTopTabId,
		setFindQuery,
		setHistorySuggestions,
		setRuntimeReconnectPending,
		setUrlCopied,
		setUrlEditing,
		setUrlInput,
		showGlobalToast,
		showStaticPreview,
		showTabsLeftFade,
		showTabsRightFade,
		slotRef,
		status,
		stop,
		t,
		tabNotice,
		tabScrollRef,
		tabSensors,
		tabs,
		urlCopied,
		urlEditing,
		urlInput,
		urlInputRef,
		viewId,
	};
}
