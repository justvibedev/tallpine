import { useCallback, useEffect, useRef, useState } from "react";
import type { BrowserAgentActivityState, BrowserDevToolsState, BrowserFindState, BrowserNavState, BrowserTabsState } from "../../../main/browser-view-host";
import type { BrowserProfileViewState } from "../../../shared/browser-profiles";
import { UseBrowserViewOptions, ClosedBrowserTab } from "./browser-view-types";
import { EMPTY_NAV_STATE, EMPTY_TABS_STATE, EMPTY_DEVTOOLS_STATE, EMPTY_FIND_STATE, EMPTY_PROFILE_STATE, PreviewTrigger, closedTabsBySession, consumedPreviewTriggers } from "./browser-view-registry";
import { publishBrowserHole, claimLayoutRevision, HIDDEN_RECT, hiddenByFullscreen, visibleSlotRect } from "./browser-view-layout";

/** Session-scoped view, navigation and tab state for useBrowserView. */
export function useBrowserViewState(props: UseBrowserViewOptions) {
	const {
		sessionId,
		active,
		poppedOut,
	} = props;
	const [viewId, setViewId] = useState("");
	const [navState, setNavState] = useState<BrowserNavState>(EMPTY_NAV_STATE);
	const [annotationMode, setAnnotationModeState] = useState(false);
	const [annotationState, setAnnotationState] = useState({ count: 0, screenshotCount: 0, hasDraft: false });
	const [tabsState, setTabsState] = useState<BrowserTabsState>(EMPTY_TABS_STATE);
	// Display-only tab order (drag-to-reorder). Re-projected onto every incoming
	// tabsState push below, since the main process's own tab order is not
	// authoritative and browser:tabsState pushes on every nav/title event.
	const [tabOrder, setTabOrder] = useState<string[]>([]);
	const [devtoolsState, setDevtoolsState] = useState<BrowserDevToolsState>(EMPTY_DEVTOOLS_STATE);
	const [findState, setFindState] = useState<BrowserFindState>(EMPTY_FIND_STATE);
	const [findOpenRequest, setFindOpenRequest] = useState(0);
	const [profileState, setProfileState] = useState<BrowserProfileViewState>(EMPTY_PROFILE_STATE);
	const [tabNotice, setTabNotice] = useState("");
	const [closedTabs, setClosedTabs] = useState<ClosedBrowserTab[]>([]);
	const [agentBrowserActive, setAgentBrowserActive] = useState(false);
	const [agentBrowserActivity, setAgentBrowserActivity] = useState<BrowserAgentActivityState | null>(null);
	const [stateSessionId, setStateSessionId] = useState(sessionId);
	const slotNodeRef = useRef<HTMLDivElement | null>(null);
	const viewIdRef = useRef("");
	const annotationModeRef = useRef(false);
	const activeRef = useRef(active);
	const poppedOutRef = useRef(poppedOut);
	const frameRef = useRef<number | null>(null);
	const appliedLayoutRevisionRef = useRef(0);
	const settleTimerRef = useRef<number | null>(null);
	const observerRef = useRef<ResizeObserver | null>(null);
	const previewTriggerRef = useRef<PreviewTrigger | null>(null);
	const tabNoticeTimerRef = useRef<number | null>(null);
	const tabsStateRef = useRef(tabsState);
	const hasNativeBrowser = Boolean(window.tallpine?.browser);

	useEffect(() => {
		activeRef.current = active;
	}, [active]);

	useEffect(() => {
		tabsStateRef.current = tabsState;
	}, [tabsState]);

	useEffect(() => {
		annotationModeRef.current = annotationMode;
	}, [annotationMode]);

	const showTabNotice = useCallback((message: string) => {
		setTabNotice(message);
		if (tabNoticeTimerRef.current !== null) window.clearTimeout(tabNoticeTimerRef.current);
		tabNoticeTimerRef.current = window.setTimeout(() => {
			tabNoticeTimerRef.current = null;
			setTabNotice("");
		}, 3_000);
	}, []);

	// Single choke point for every closedTabs mutation, so the module-level
	// per-session cache (closedTabsBySession) can never drift from what's
	// actually shown.
	const updateClosedTabs = useCallback(
		(updater: (current: ClosedBrowserTab[]) => ClosedBrowserTab[]) => {
			const current = closedTabsBySession.get(sessionId) ?? [];
			const next = updater(current);
			closedTabsBySession.set(sessionId, next);
			setClosedTabs(next);
		},
		[sessionId],
	);

	const sendHiddenBounds = useCallback((id = viewIdRef.current) => {
		if (!id) return;
		publishBrowserHole(id, null);
		window.tallpine?.browser.setBounds({
			viewId: id,
			revision: claimLayoutRevision(),
			rect: HIDDEN_RECT,
			visible: false,
		});
	}, []);

	const measureAndSend = useCallback(() => {
		// measureAndSend runs both from the scheduleMeasure() rAF callback and as a
		// direct synchronous call (parking on overlay open, the settle timer). A
		// direct call may land while a scheduled frame is still queued, so cancel
		// that live handle rather than blindly nulling it — otherwise the
		// scheduleMeasure() dedupe guard and cancelScheduledMeasure() cleanup would
		// both trust a frameRef that no longer reflects the pending frame.
		if (frameRef.current !== null) {
			if (window.cancelAnimationFrame) window.cancelAnimationFrame(frameRef.current);
			window.clearTimeout(frameRef.current);
		}
		frameRef.current = null;
		const id = viewIdRef.current;
		const node = slotNodeRef.current;
		if (!id) return;
		if (!activeRef.current || !node || !node.isConnected || hiddenByFullscreen(node)) {
			sendHiddenBounds(id);
			return;
		}
		const rect = visibleSlotRect(node);
		const payload = {
			viewId: id,
			revision: claimLayoutRevision(),
			rect,
			visible: rect.width > 0 && rect.height > 0,
		};
		publishBrowserHole(id, payload.visible ? rect : null);
		window.tallpine?.browser.setBounds(payload);
	}, [sendHiddenBounds]);

	const cancelScheduledMeasure = useCallback(() => {
		if (frameRef.current === null) return;
		if (window.cancelAnimationFrame) {
			window.cancelAnimationFrame(frameRef.current);
		}
		window.clearTimeout(frameRef.current);
		frameRef.current = null;
	}, []);

	const scheduleMeasure = useCallback(() => {
		if (frameRef.current !== null) return;
		frameRef.current = window.requestAnimationFrame
			? window.requestAnimationFrame(() => measureAndSend())
			: window.setTimeout(() => measureAndSend(), 16);
	}, [measureAndSend]);

	// A ResizeObserver only fires on size changes, so a position-only layout shift
	// leaves the native overlay at stale bounds: entering/leaving pop-out moves the
	// slot into a different panel, and opening the inspector (what `tallpine preview`
	// does) reflows the slot's x without changing the observed node's box size.
	// Neither fires the observer, so the view visibly spills over the sidebar/
	// terminal until an unrelated window resize re-measures it. Re-measure now and
	// again once the panel transition has settled (~240ms) so the final geometry
	// always wins.
	const scheduleSettleMeasure = useCallback(() => {
		scheduleMeasure();
		if (settleTimerRef.current !== null) window.clearTimeout(settleTimerRef.current);
		settleTimerRef.current = window.setTimeout(() => {
			settleTimerRef.current = null;
			measureAndSend();
		}, 280);
	}, [measureAndSend, scheduleMeasure]);

	const slotRef = useCallback(
		(node: HTMLDivElement | null) => {
			observerRef.current?.disconnect();
			slotNodeRef.current = node;
			if (!node) {
				sendHiddenBounds();
				return;
			}
			const observer = new ResizeObserver(scheduleMeasure);
			observer.observe(node);
			// The inspector column keeps a stable width and slides on `x`; the
			// layout gap's width is what actually changes every spring frame.
			// Observing it re-measures through the whole animation so the native
			// view tracks the sliding rail instead of lagging at the last size.
			const column = node.closest("[data-panel]");
			if (column) observer.observe(column);
			const gap = node.closest(".session-split")?.querySelector("[data-slot='inspector-gap']");
			if (gap) observer.observe(gap);
			observerRef.current = observer;
			// Ref handoffs run old-node -> null -> new-node in one React commit. Send
			// the replacement geometry immediately so the native view is restored in
			// the same event-loop turn instead of remaining parked for a painted frame.
			measureAndSend();
			scheduleMeasure();
		},
		[measureAndSend, scheduleMeasure, sendHiddenBounds],
	);

	useEffect(() => {
		return window.tallpine?.browser.onBoundsApplied((result) => {
			if (result.viewId !== viewIdRef.current || result.revision <= appliedLayoutRevisionRef.current) return;
			appliedLayoutRevisionRef.current = result.revision;
		});
	}, []);

	useEffect(() => {
		let disposed = false;
		// Preview revisions are scoped to a session. A native view survives session
		// switches, so seed from the per-session consumed trigger to avoid
		// reasserting previewUrl over manual navigation on switch-back.
		previewTriggerRef.current = hasNativeBrowser ? (consumedPreviewTriggers.get(sessionId) ?? null) : null;
		setStateSessionId(sessionId);
		setViewId("");
		setNavState(EMPTY_NAV_STATE);
		setTabsState(EMPTY_TABS_STATE);
		// Tab ids (`t1`, `t2`, ...) restart per session, so a stale order from the
		// previous session could otherwise silently reapply to the new one.
		setTabOrder([]);
		setDevtoolsState(EMPTY_DEVTOOLS_STATE);
		setFindState(EMPTY_FIND_STATE);
		setFindOpenRequest(0);
		setProfileState(EMPTY_PROFILE_STATE);
		setTabNotice("");
		// Restore this session's own Recently Closed list rather than wiping it —
		// switching away and back should find it exactly as it was, same as the
		// live tabs the native view already keeps.
		setClosedTabs(closedTabsBySession.get(sessionId) ?? []);
		setAgentBrowserActive(false);
		setAgentBrowserActivity(null);
		if (tabNoticeTimerRef.current !== null) {
			window.clearTimeout(tabNoticeTimerRef.current);
			tabNoticeTimerRef.current = null;
		}
		if (!hasNativeBrowser) {
			const state = {
				...EMPTY_NAV_STATE,
				viewId: `preview-${sessionId}`,
				url: "",
				title: "",
			};
			viewIdRef.current = state.viewId;
			setViewId(state.viewId);
			setNavState(state);
			setDevtoolsState((current) => ({
				...current,
				viewId: state.viewId,
				activeTabId: "",
			}));
			setProfileState({
				viewId: state.viewId,
				profileId: null,
				temporary: true,
			});
			return () => {
				disposed = true;
				viewIdRef.current = "";
			};
		}
		window.tallpine?.browser.ensure(sessionId, false).then((state) => {
			if (disposed) return;
			viewIdRef.current = state.viewId;
			setViewId(state.viewId);
			setNavState(state);
			void window.tallpine?.browser
				.getProfile(state.viewId)
				.then((profile) => {
					if (!disposed && viewIdRef.current === profile.viewId) setProfileState(profile);
				})
				.catch(() => undefined);
			void window.tallpine?.browser
				.getTabs(state.viewId)
				.then((tabs) => {
					if (!disposed && viewIdRef.current === tabs.viewId) setTabsState(tabs);
				})
				.catch(() => undefined);
			void window.tallpine?.browser
				.getFindState(state.viewId)
				.then((find) => {
					if (!disposed && viewIdRef.current === find.viewId) setFindState(find);
				})
				.catch(() => undefined);
			scheduleSettleMeasure();
		});
		return () => {
			disposed = true;
			const id = viewIdRef.current;
			if (id) {
				if (annotationModeRef.current) {
					void window.tallpine?.browser.setAnnotationMode({
						viewId: id,
						enabled: false,
					});
					setAnnotationModeState(false);
				}
				sendHiddenBounds(id);
			}
			viewIdRef.current = "";
		};
	}, [
		hasNativeBrowser,
		scheduleSettleMeasure,
		sendHiddenBounds,
		sessionId,
	]);

	useEffect(() => {
		return window.tallpine?.browser.onNavState((state) => {
			if (state.viewId !== viewIdRef.current) return;
			setNavState(state);
		});
	}, []);

	return {
		activeRef,
		agentBrowserActive,
		agentBrowserActivity,
		annotationMode,
		annotationModeRef,
		annotationState,
		cancelScheduledMeasure,
		closedTabs,
		devtoolsState,
		findOpenRequest,
		findState,
		hasNativeBrowser,
		measureAndSend,
		navState,
		observerRef,
		poppedOutRef,
		previewTriggerRef,
		profileState,
		scheduleMeasure,
		scheduleSettleMeasure,
		sendHiddenBounds,
		setAgentBrowserActive,
		setAgentBrowserActivity,
		setAnnotationModeState,
		setAnnotationState,
		setClosedTabs,
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
		slotRef,
		stateSessionId,
		tabNotice,
		tabNoticeTimerRef,
		tabOrder,
		tabsState,
		tabsStateRef,
		updateClosedTabs,
		viewId,
		viewIdRef,
	};
}
