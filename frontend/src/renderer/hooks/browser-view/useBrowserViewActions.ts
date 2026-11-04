import { PRODUCT_NAME } from "../../../shared/brand";
import { useCallback, useEffect } from "react";
import type { BrowserDevToolsPlacement } from "../../../main/browser-view-host";
import type { BrowserAnnotationCancelPayload, BrowserAnnotationSubmitPayload } from "../../../shared/browser-annotations";
import { tallpineBridge } from "../../lib/bridge";
import { useBrowserViewState } from "./useBrowserViewState";
import { useBrowserViewLayoutEffects } from "./useBrowserViewLayoutEffects";
import { UseBrowserViewOptions } from "./browser-view-types";
import { isBlankTabUrl, sameBrowserURL, MAX_CLOSED_TABS, sameBrowserOrigin } from "./browser-view-urls";
import { closedTabsBySession, EMPTY_FIND_STATE, PreviewTrigger, consumedPreviewTriggers, EMPTY_NAV_STATE, EMPTY_TABS_STATE } from "./browser-view-registry";

/** Navigation, link and tab actions for useBrowserView. */
export function useBrowserViewActions(props: UseBrowserViewOptions, state: ReturnType<typeof useBrowserViewState>, layout: ReturnType<typeof useBrowserViewLayoutEffects>) {
	const {
		sessionId,
		origin,
		terminated,
		previewUrl,
		previewRevision,
	} = props;
	const {
		annotationModeRef,
		hasNativeBrowser,
		navState,
		previewTriggerRef,
		sendHiddenBounds,
		setAnnotationModeState,
		setAnnotationState,
		setClosedTabs,
		setDevtoolsState,
		setFindState,
		setNavState,
		setTabsState,
		setViewId,
		showTabNotice,
		stateSessionId,
		tabsStateRef,
		updateClosedTabs,
		viewId,
		viewIdRef,
	} = state;
	const {
		openTab,
		selectTab,
		setAnnotationMode,
		withView,
	} = layout;
	const openLink = useCallback(
		async (url: string) => {
			if (!hasNativeBrowser) return;
			let id = viewIdRef.current;
			if (!id) {
				const ensured = await window.tallpine!.browser.ensure(sessionId, false);
				id = ensured.viewId;
				viewIdRef.current = id;
				setViewId(id);
				setNavState(ensured);
			}
			let tabs = tabsStateRef.current.tabs;
			// The native tab host is authoritative. The renderer cache can lag after
			// navigation/title updates, so refresh it before deciding whether this URL
			// already has a tab and should be selected instead of duplicated.
			try {
				const next = await window.tallpine!.browser.getTabs(id);
				tabs = next.tabs;
				if (viewIdRef.current === id) setTabsState(next);
			} catch {
				// Keep the last known tabs as a fallback if the native host is briefly
				// unavailable; opening the link remains better than dropping the click.
			}
			// A link is the person's: it never selects or loads into an agent tab.
			const existingTab = tabs.find((tab) => !tab.remote && !isBlankTabUrl(tab.url) && sameBrowserURL(tab.url, url));
			if (existingTab) {
				await selectTab(existingTab.id);
				return;
			}
			const activeTab = tabs.find((tab) => tab.active);
			if (activeTab && !activeTab.remote && isBlankTabUrl(activeTab.url)) {
				const state = await window.tallpine!.browser.navigate({ viewId: id, url });
				if (viewIdRef.current === state.viewId) setNavState(state);
				return;
			}
			await openTab(url);
		},
		[hasNativeBrowser, openTab, selectTab, sessionId],
	);

	const reopenClosedTab = useCallback(
		async (tabId?: string) => {
			const current = closedTabsBySession.get(sessionId) ?? [];
			const entry = tabId ? current.find((tab) => tab.id === tabId) : current[0];
			if (!entry) return;
			updateClosedTabs((tabs) => tabs.filter((tab) => tab.id !== entry.id));
			try {
				await openTab(entry.url);
			} catch {
				// Restore the entry instead of losing it when tab creation fails.
				updateClosedTabs((tabs) => [entry, ...tabs.filter((tab) => tab.id !== entry.id)].slice(0, MAX_CLOSED_TABS));
				showTabNotice("Couldn't reopen that tab");
			}
		},
		[openTab, sessionId, showTabNotice, updateClosedTabs],
	);

	const runDevtools = useCallback(
		async (operation: "open" | "close" | "setPlacement", placement?: BrowserDevToolsPlacement) => {
			const id = viewIdRef.current;
			if (!id || !hasNativeBrowser) return;
			try {
				const next = await window.tallpine!.browser.devtools({
					viewId: id,
					operation,
					placement,
				});
				if (viewIdRef.current === next.viewId) setDevtoolsState(next);
			} catch {
				// The main process reports the unavailable state through the normal
				// browser lifecycle; a failed optional DevTools action should not
				// become an unhandled renderer rejection.
			}
		},
		[hasNativeBrowser],
	);

	useEffect(() => {
		const handleDone = (payload: BrowserAnnotationSubmitPayload | BrowserAnnotationCancelPayload) => {
			if (payload.viewId !== viewIdRef.current) return;
			setAnnotationModeState(false);
		};
		const offSubmit = window.tallpine?.browser.onAnnotationSubmit(handleDone);
		const offCancel = window.tallpine?.browser.onAnnotationCancel(handleDone);
		return () => {
			offSubmit?.();
			offCancel?.();
		};
	}, []);

	useEffect(() => {
		const offState = window.tallpine?.browser.onAnnotationState((payload) => {
			if (payload.viewId !== viewIdRef.current) return;
			setAnnotationState({
				count: payload.count,
				screenshotCount: payload.screenshotCount,
				hasDraft: payload.hasDraft,
			});
		});
		return () => offState?.();
	}, []);

	useEffect(() => {
		if (navState.url || !annotationModeRef.current) return;
		void setAnnotationMode(false);
	}, [navState.url, setAnnotationMode]);

	const navigate = useCallback(
		(url: string) => {
			if (!hasNativeBrowser) {
				const normalized = url.trim();
				setNavState((current) => ({
					...current,
					url: normalized,
					title: normalized ? `${PRODUCT_NAME} preview` : "",
					isLoading: false,
				}));
				return Promise.resolve();
			}
			return withView((id) => window.tallpine!.browser.navigate({ viewId: id, url }));
		},
		[hasNativeBrowser, withView],
	);

	// A preview never lands in an agent tab: it goes to the person's preview
	// tab (one on its origin or another preview's), else to a new tab.
	const showPreview = useCallback(
		async (url: string) => {
			const id = viewIdRef.current;
			const tabs = hasNativeBrowser && id ? (await window.tallpine!.browser.getTabs(id).catch(() => null))?.tabs : undefined;
			if (!tabs?.find((tab) => tab.active)?.remote) return navigate(url);
			const preview = /^https:\/\/ao-preview-[0-9a-f]{32}\.localhost(?::\d+)?\//;
			const own = tabs.find((tab) => !tab.remote && (sameBrowserOrigin(tab.url, url) || preview.test(tab.url)));
			if (!own) return openTab(url);
			await selectTab(own.id);
			return navigate(url);
		},
		[hasNativeBrowser, navigate, openTab, selectTab],
	);

	// A remote session's agent browser (the host's
	// Chromium its agent drives, streamed) is part of the session's surface:
	// main shows it as one tab per agent page. It is derived from the host
	// whenever the view is built or the host reconnects, not remembered: a
	// reopened window brings it back. A host with no agent browser to show yet
	// (starting, asleep, redeploying, not set up, or an agent that has not
	// used it) is asked again, backing off, for as long as the view stays.
	useEffect(() => {
		const agentBrowserUrl = origin
			? () => tallpineBridge.remotes.agentBrowserUrl(origin.hostId, origin.sessionId)
			: undefined;
		if (!hasNativeBrowser || !agentBrowserUrl || !viewId || viewIdRef.current !== viewId || terminated) return;
		let cancelled = false;
		let retry = 0;
		// ponytail: polls every 15 s at most while nothing shows; main could
		// instead say when the agent browser comes up.
		const again = (attempt: number) => {
			if (!cancelled) retry = window.setTimeout(() => ask(attempt + 1), Math.min(2_000 * 2 ** attempt, 15_000));
		};
		const ask = (attempt: number) => {
			void agentBrowserUrl().then(async (url) => {
				if (cancelled || viewIdRef.current !== viewId) return;
				if (!url) return again(attempt);
				const state = await window.tallpine!.browser.openTab({ viewId, url });
				if (viewIdRef.current === viewId) setTabsState(state);
			}).catch((error) => {
				if (attempt === 0) console.warn("Unable to open the agent browser", error);
				again(attempt);
			});
		};
		ask(0);
		return () => {
			cancelled = true;
			window.clearTimeout(retry);
		};
	}, [hasNativeBrowser, origin?.hostId, origin?.sessionId, origin?.proxyBase, terminated, viewId]);

	const clear = useCallback(() => {
		if (!hasNativeBrowser) {
			setNavState((current) => ({
				...current,
				url: "",
				title: "",
				isLoading: false,
			}));
			return Promise.resolve();
		}
		return withView((id) => window.tallpine!.browser.clear(id));
	}, [hasNativeBrowser, withView]);

	const findInPage = useCallback(
		(query: string, forward: boolean, newSession: boolean) => {
			if (!hasNativeBrowser) {
				setFindState((current) => ({ ...current, query, activeMatchOrdinal: 0, matches: 0, finalUpdate: true }));
				return Promise.resolve();
			}
			return withView(async (id) => {
				const state = await window.tallpine!.browser.findInPage({ viewId: id, query, forward, newSession });
				if (viewIdRef.current === state.viewId) setFindState(state);
			});
		},
		[hasNativeBrowser, withView],
	);

	const stopFindInPage = useCallback(
		(focusPage = false) => {
			if (!hasNativeBrowser) {
				setFindState(EMPTY_FIND_STATE);
				return Promise.resolve();
			}
			return withView(async (id) => {
				const state = await window.tallpine!.browser.stopFindInPage({ viewId: id, focusPage });
				if (viewIdRef.current === state.viewId) setFindState(state);
			});
		},
		[hasNativeBrowser, withView],
	);

	// Clearing or terminating a remote preview must also retire its viewer-only
	// origin, even when the native browser view has already been destroyed.
	useEffect(() => {
		if (!origin || (!terminated && previewUrl?.trim())) return;
		void tallpineBridge.remotes.previewUrl(origin.hostId, origin.sessionId, "").catch(() => {
			// A disconnected proxy is already closed, so there is nothing to revoke.
		});
		return undefined;
	}, [origin?.hostId, origin?.sessionId, origin?.proxyBase, previewUrl, terminated]);

	// Drive the view from the daemon-set preview target. Current daemons key
	// this on previewRevision (bumped on every `tallpine preview` call); older daemons
	// did not send it, so fall back to URL changes for compatibility.
	useEffect(() => {
		// During a session switch React still renders once with the prior
		// viewId state, while the cleanup has already cleared viewIdRef. Do not
		// consume the new session's preview revision against that stale view.
		if (!viewId || viewIdRef.current !== viewId || terminated) return;
		const target = previewUrl?.trim() ?? "";
		const source = origin ? `${origin.hostId}:${origin.sessionId}:${origin.proxyBase}` : "local";
		const revision = typeof previewRevision === "number" ? previewRevision : null;
		const previous = previewTriggerRef.current;
		if (previous?.revision === revision && previous.target === target && previous.origin === source) return;
		if (revision !== null && previous?.revision === revision && previous.origin === source) return;
		const consumed: PreviewTrigger = { revision, target, origin: source };
		previewTriggerRef.current = consumed;
		if (hasNativeBrowser) consumedPreviewTriggers.set(sessionId, consumed);
		if (target) {
			let cancelled = false;
			const resolved = origin
				? tallpineBridge.remotes.previewUrl(origin.hostId, origin.sessionId, target)
				: Promise.resolve(target);
			void resolved.then((url) => {
				return cancelled ? undefined : showPreview(url);
			}).catch((error) => console.warn("Unable to open browser preview", error));
			return () => { cancelled = true; };
		} else if ((revision !== null && revision > 0) || previous?.target) {
			void clear();
		}
		return undefined;
	}, [clear, hasNativeBrowser, origin?.hostId, origin?.sessionId, origin?.proxyBase, previewRevision, previewUrl, sessionId, showPreview, terminated, viewId]);

	const destroy = useCallback(() => {
		const id = viewIdRef.current;
		if (!id) return;
		if (annotationModeRef.current) {
			void window.tallpine?.browser.setAnnotationMode({ viewId: id, enabled: false });
			setAnnotationModeState(false);
		}
		sendHiddenBounds(id);
		window.tallpine?.browser.destroy(id);
		viewIdRef.current = "";
		setViewId("");
		setNavState(EMPTY_NAV_STATE);
		setTabsState(EMPTY_TABS_STATE);
		setFindState(EMPTY_FIND_STATE);
		setClosedTabs([]);
	}, [sendHiddenBounds]);

	// Termination invalidates the complete session-owned browser, including all
	// tabs, captures, profile state, and target mappings. `clear` remains the
	// explicit preview-reset operation.
	useEffect(() => {
		if (!terminated || !viewId) return;
		consumedPreviewTriggers.delete(sessionId);
		closedTabsBySession.delete(sessionId);
		destroy();
	}, [destroy, sessionId, terminated, viewId]);

	// Hook state survives a `sessionId` prop change until the reset effect above
	// commits. Keep navigation, tab, and activity state hidden during that
	// intervening render so consumers can never interpret the departed session's
	// state as belonging to the destination session.
	const stateBelongsToSession = stateSessionId === sessionId;

	return {
		destroy,
		findInPage,
		navigate,
		openLink,
		reopenClosedTab,
		runDevtools,
		stateBelongsToSession,
		stopFindInPage,
	};
}
