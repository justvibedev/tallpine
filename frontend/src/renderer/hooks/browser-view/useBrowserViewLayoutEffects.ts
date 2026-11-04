import { useCallback, useEffect, useLayoutEffect, useMemo } from "react";
import type { BrowserNavState, BrowserTabState } from "../../../main/browser-view-host";
import type { BrowserAnnotationActionInput } from "../../../shared/browser-annotations";
import { subscribeBrowserOverlaySync } from "../../lib/browser-overlay-sync";
import { useBrowserViewState } from "./useBrowserViewState";
import { UseBrowserViewOptions } from "./browser-view-types";
import { isBlankTabUrl, MAX_CLOSED_TABS } from "./browser-view-urls";

/** Layout, hole publishing and preview-trigger effects for useBrowserView. */
export function useBrowserViewLayoutEffects(props: UseBrowserViewOptions, state: ReturnType<typeof useBrowserViewState>) {
	const {
		sessionId,
		active,
		poppedOut,
	} = props;
	const {
		activeRef,
		cancelScheduledMeasure,
		hasNativeBrowser,
		measureAndSend,
		navState,
		observerRef,
		poppedOutRef,
		scheduleMeasure,
		scheduleSettleMeasure,
		sendHiddenBounds,
		setAgentBrowserActive,
		setAgentBrowserActivity,
		setAnnotationModeState,
		setDevtoolsState,
		setFindOpenRequest,
		setFindState,
		setNavState,
		setProfileState,
		setTabOrder,
		setTabsState,
		setViewId,
		settleTimerRef,
		showTabNotice,
		tabNoticeTimerRef,
		tabOrder,
		tabsState,
		tabsStateRef,
		updateClosedTabs,
		viewIdRef,
	} = state;
	useEffect(() => {
		return window.tallpine?.browser.onFindState((state) => {
			if (state.viewId !== viewIdRef.current) return;
			setFindState(state);
		});
	}, []);

	useEffect(() => {
		return window.tallpine?.browser.onFindOpen((state) => {
			if (state.viewId !== viewIdRef.current) return;
			setFindState(state);
			setFindOpenRequest((current) => current + 1);
		});
	}, []);

	useEffect(() => {
		return window.tallpine?.browser.onTabsState((state) => {
			if (state.viewId !== viewIdRef.current) return;
			setTabsState(state);
			const change = state.change;
			if (change?.kind === "popup") {
				showTabNotice("Opened new tab");
				return;
			}
			if (change?.kind !== "closed" || !change.tab || isBlankTabUrl(change.tab.url)) return;
			const { id, title, url, favicon } = change.tab;
			updateClosedTabs((current) => [
				{ id, title, url, favicon },
				...current.filter((tab) => tab.id !== id),
			].slice(0, MAX_CLOSED_TABS));
		});
	}, [showTabNotice, updateClosedTabs]);

	// Re-project the persisted display order onto every incoming tabsState push:
	// browser:tabsState fires on every nav/title-update/loading-state change for
	// any tab, so a one-shot local reorder would otherwise be clobbered by the
	// very next push. New tabs (via "+", popups, agent tab-new) append at the end.
	useEffect(() => {
		const incomingIds = tabsState.tabs.map((tab) => tab.id);
		setTabOrder((prev) => {
			const kept = prev.filter((id) => incomingIds.includes(id));
			const added = incomingIds.filter((id) => !kept.includes(id));
			return kept.length === prev.length && added.length === 0 ? prev : [...kept, ...added];
		});
	}, [tabsState.tabs]);

	const tabs = useMemo(() => {
		const byId = new Map(tabsState.tabs.map((tab) => [tab.id, tab]));
		return tabOrder.map((id) => byId.get(id)).filter((tab): tab is BrowserTabState => Boolean(tab));
	}, [tabOrder, tabsState.tabs]);

	const reorderTabs = useCallback((orderedIds: string[]) => setTabOrder(orderedIds), []);

	useEffect(() => {
		return window.tallpine?.browser.onDevToolsState((state) => {
			if (state.viewId !== viewIdRef.current) return;
			setDevtoolsState(state);
		});
	}, []);

	useEffect(() => {
		return window.tallpine?.browser.onProfileState((state) => {
			if (state.viewId !== viewIdRef.current) return;
			setProfileState(state);
		});
	}, []);

	useEffect(() => {
		return window.tallpine?.browser.onAgentActivity((state) => {
			if (state.viewId !== viewIdRef.current) return;
			setAgentBrowserActive(state.active);
			setAgentBrowserActivity(state);
		});
	}, []);

	useEffect(
		() => () => {
			if (tabNoticeTimerRef.current !== null) window.clearTimeout(tabNoticeTimerRef.current);
		},
		[],
	);

	useLayoutEffect(() => {
		if (active) {
			scheduleSettleMeasure();
		} else {
			sendHiddenBounds();
		}
	}, [active, navState.url, poppedOut, scheduleSettleMeasure, sendHiddenBounds]);

	useEffect(() => {
		if (poppedOutRef.current === poppedOut) return;
		poppedOutRef.current = poppedOut;
		if (!hasNativeBrowser || !activeRef.current) {
			scheduleSettleMeasure();
			return;
		}
		measureAndSend();
		window.setTimeout(() => {
			measureAndSend();
		}, 0);
		scheduleSettleMeasure();
	}, [hasNativeBrowser, measureAndSend, poppedOut, scheduleSettleMeasure]);

	useEffect(() => {
		if (!hasNativeBrowser) return;
		let isResizing = document.body.classList.contains("is-resizing-x");
		const updateResize = () => {
			const wasResizing = isResizing;
			isResizing = document.body.classList.contains("is-resizing-x");
			if (wasResizing !== isResizing) scheduleSettleMeasure();
		};
		const unsubscribeOverlay = subscribeBrowserOverlaySync();
		updateResize();
		// useResizable.ts toggles `is-resizing-x` on <body> (outside React) while the
		// inspector's own drag handle is held. The handle captures its pointer in
		// useResizable, so we only need to keep the native bounds synchronized here.
		// A dedicated, non-subtree observer keeps this cheap: unlike the overlay observer's `data-state`,
		// `class` churns on nearly every render throughout the app, so watching it
		// subtree-wide would run `update()` far more often than the dialog/menu case.
		const resizeObserver = new MutationObserver(updateResize);
		resizeObserver.observe(document.body, {
			attributes: true,
			attributeFilter: ["class"],
		});
		return () => {
			unsubscribeOverlay();
			resizeObserver.disconnect();
		};
	}, [hasNativeBrowser, scheduleSettleMeasure]);

	useEffect(() => {
		const handle = () => scheduleMeasure();
		// Fullscreen animates on macOS, so settle-measure: hiding lands on the
		// leading edge, and the restore on exit waits for the final geometry.
		const handleFullscreenChange = () => scheduleSettleMeasure();
		window.addEventListener("resize", handle);
		window.addEventListener("scroll", handle, true);
		document.addEventListener("fullscreenchange", handleFullscreenChange);
		return () => {
			window.removeEventListener("resize", handle);
			window.removeEventListener("scroll", handle, true);
			document.removeEventListener("fullscreenchange", handleFullscreenChange);
			observerRef.current?.disconnect();
			cancelScheduledMeasure();
			if (settleTimerRef.current !== null) window.clearTimeout(settleTimerRef.current);
		};
	}, [cancelScheduledMeasure, scheduleMeasure, scheduleSettleMeasure]);

	const withView = useCallback(async (fn: (id: string) => Promise<BrowserNavState | void>) => {
		const id = viewIdRef.current;
		if (!id) return;
		try {
			const next = await fn(id);
			if (next) setNavState(next);
		} catch {
			// navigation errors are handled by the did-fail-load event channel
		}
	}, []);

	const setAnnotationMode = useCallback(
		async (enabled: boolean) => {
			const id = viewIdRef.current;
			if (!id || !hasNativeBrowser) {
				setAnnotationModeState(false);
				return;
			}
			const styles = getComputedStyle(document.documentElement);
			await window.tallpine!.browser.setAnnotationMode({
				viewId: id,
				enabled,
				theme: {
					background: styles.getPropertyValue("--background").trim(),
					foreground: styles.getPropertyValue("--foreground").trim(),
					muted: styles.getPropertyValue("--muted").trim(),
					mutedForeground: styles.getPropertyValue("--muted-foreground").trim(),
					border: styles.getPropertyValue("--border").trim(),
					accent: styles.getPropertyValue("--primary").trim(),
					accentForeground: styles.getPropertyValue("--primary-foreground").trim(),
					destructive: styles.getPropertyValue("--destructive").trim(),
				},
			});
			setAnnotationModeState(enabled);
		},
		[hasNativeBrowser],
	);

	const annotationAction = useCallback(
		async (action: BrowserAnnotationActionInput["action"]) => {
			const id = viewIdRef.current;
			if (!id || !hasNativeBrowser) return;
			await window.tallpine!.browser.annotationAction({ viewId: id, action });
		},
		[hasNativeBrowser],
	);

	const selectTab = useCallback(
		async (tabId: string) => {
			const viewId = viewIdRef.current;
			if (!viewId || !hasNativeBrowser) return;
			try {
				const state = await window.tallpine!.browser.selectTab({ viewId, tabId });
				if (viewIdRef.current === state.viewId) setTabsState(state);
			} catch {
				// The native view may already have switched before automation
				// synchronization fails. Read the actual selection back instead of
				// showing a misleading switch-failure notice in the toolbar.
				const state = await window.tallpine!.browser.getTabs(viewId).catch(() => undefined);
				if (state && viewIdRef.current === state.viewId) setTabsState(state);
			}
		},
		[hasNativeBrowser],
	);

	const closeTab = useCallback(
		async (tabId: string) => {
			const viewId = viewIdRef.current;
			if (!viewId || !hasNativeBrowser) return;
			// Read from the ref, not the tabsState closure, so this callback's
			// identity stays stable across tab updates instead of churning on
			// every nav/title-update/loading-state push (it cascades into
			// handleCloseTab in BrowserPanel.tsx otherwise).
			const closing = tabsStateRef.current.tabs.find((tab) => tab.id === tabId);
			try {
				const state = await window.tallpine!.browser.closeTab({ viewId, tabId });
				if (viewIdRef.current !== state.viewId) return;
				setTabsState(state);
				// Only remember it once the main process confirms it's actually gone —
				// closeTab can silently no-op (the tab stays in state.tabs), and
				// recording it as "closed" anyway would show it in Recently Closed
				// while it's still sitting right there in the live tab list. Only real,
				// distinguishable tabs are worth keeping — a blank tab has nothing to
				// reopen.
				// An agent tab's page lives on the remote host, so it is not the person's to reopen.
				const stillOpen = state.tabs.some((tab) => tab.id === tabId);
				if (closing && !stillOpen && !closing.remote && !isBlankTabUrl(closing.url)) {
					const { id, title, url, favicon } = closing;
					updateClosedTabs((current) => [{ id, title, url, favicon }, ...current.filter((t) => t.id !== id)].slice(0, MAX_CLOSED_TABS));
				}
			} catch {
				showTabNotice("Couldn't close that tab");
				// The main process can mutate its own tab state before reporting a
				// close as failed (e.g. the automation runtime's own closeTarget
				// callback already removed the tab, then the overall command still
				// reports failure) — resync instead of leaving this tab's row
				// showing in the rail after it's genuinely gone, which just
				// re-fails identically on every retry.
				window.tallpine?.browser
					.getTabs(viewId)
					.then((state) => {
						if (viewIdRef.current === state.viewId) setTabsState(state);
					})
					.catch(() => undefined);
			}
		},
		[hasNativeBrowser, showTabNotice, updateClosedTabs],
	);

	const openTab = useCallback(
		async (url?: string) => {
			if (!hasNativeBrowser) return;
			let viewId = viewIdRef.current;
			if (!viewId) {
				const ensured = await window.tallpine!.browser.ensure(sessionId, false);
				viewId = ensured.viewId;
				viewIdRef.current = viewId;
				setViewId(viewId);
				setNavState(ensured);
			}
			const state = await window.tallpine!.browser.openTab({ viewId, url });
			if (viewIdRef.current === state.viewId) setTabsState(state);
		},
		[hasNativeBrowser, sessionId],
	);
	return {
		annotationAction,
		closeTab,
		openTab,
		reorderTabs,
		selectTab,
		setAnnotationMode,
		tabs,
		withView,
	};
}
