import { useBrowserPanelState } from "./browser/useBrowserPanelState";
import { useBrowserPanelActions } from "./browser/useBrowserPanelActions";
import { useBrowserPanelChrome } from "./browser/useBrowserPanelChrome";
import { BrowserToolbar } from "./browser/BrowserToolbar";
import { BrowserFindBar } from "./browser/BrowserFindBar";
import { BrowserBody } from "./browser/BrowserBody";
import type { AnnotationStatus, BrowserPanelProps, BrowserPanelViewProps, BrowserAnnotationQueueModel } from "./browser/browser-types";
import { useCallback, useEffect, useRef, useState, type FocusEvent } from "react";
import { AnimatePresence, motion } from "motion/react";
import { apiErrorMessage } from "../lib/api-client";
import { clientForSessionHost } from "../lib/host-clients";
import { useBrowserView } from "../hooks/useBrowserView";
import { formatBrowserAnnotationMessage, type BrowserAnnotationSubmitPayload } from "../../shared/browser-annotations";
import { cn } from "../lib/utils";
import { appI18n } from "../i18n";
import { tallpineBridge } from "../lib/bridge";


export function useBrowserAnnotationQueue({
	sessionId,
	hostId,
	sourcePreviewUrl,
	navUrl,
}: {
	sessionId?: string;
	hostId?: string;
	sourcePreviewUrl?: string;
	navUrl?: string;
}): BrowserAnnotationQueueModel {
	const [state, setState] = useState<{ status: AnnotationStatus; error: string; queuedCount: number }>({
		status: "idle",
		error: "",
		queuedCount: 0,
	});
	const annotationQueueRef = useRef<BrowserAnnotationSubmitPayload[]>([]);
	const stagedScreenshotPathsRef = useRef(new Map<BrowserAnnotationSubmitPayload, string[]>());
	const annotationSendingRef = useRef(false);
	const sessionIdRef = useRef(sessionId ?? "");
	const hostIdRef = useRef(hostId);
	const sourcePreviewUrlRef = useRef(sourcePreviewUrl);
	const generationRef = useRef(0);
	const sentTimerRef = useRef<number | null>(null);

	const resetQueue = useCallback(() => {
		generationRef.current += 1;
		if (sentTimerRef.current !== null) window.clearTimeout(sentTimerRef.current);
		sentTimerRef.current = null;
		annotationQueueRef.current = [];
		stagedScreenshotPathsRef.current.clear();
		annotationSendingRef.current = false;
		setState({ status: "idle", error: "", queuedCount: 0 });
	}, []);

	const drainAnnotationQueue = useCallback(() => {
		if (annotationSendingRef.current || !sessionIdRef.current) {
			return;
		}

		const payload = annotationQueueRef.current.shift();
		setState((current) => ({ ...current, queuedCount: annotationQueueRef.current.length }));
		if (!payload) return;

		annotationSendingRef.current = true;
		const sendGeneration = generationRef.current;
		const sendSessionId = sessionIdRef.current;
		const client = clientForSessionHost(hostIdRef.current);
		setState({ status: "sending", error: "", queuedCount: annotationQueueRef.current.length });

		void (async () => {
			let sent = false;
			let failureMessage = appI18n.t("browser.unableSendAnnotation");
			try {
				let screenshotPaths = stagedScreenshotPathsRef.current.get(payload);
				if (!screenshotPaths) {
					const attachments = [
						...payload.session.screenshots.map(({ mimeType, data }) => ({ mimeType, data })),
						...(payload.snapshot ? [payload.snapshot] : []),
					];
					if (attachments.length > 0) {
						const staged = await client.POST("/api/v1/sessions/{sessionId}/attachments", {
							params: { path: { sessionId: sendSessionId } },
							body: { attachments },
						});
						if (staged.error || !staged.data) {
							failureMessage = apiErrorMessage(staged.error, appI18n.t("browser.unableSendAnnotation"));
							return;
						}
						screenshotPaths = staged.data.paths;
						stagedScreenshotPathsRef.current.set(payload, screenshotPaths);
					} else {
						screenshotPaths = [];
					}
				}
				let sendPayload = payload;
				if (hostIdRef.current && sourcePreviewUrlRef.current) {
					let pageUrl = "";
					try {
						pageUrl = await tallpineBridge.remotes.resolvePreviewUrl(hostIdRef.current, sendSessionId, payload.session.page.url);
					} catch { /* A disconnected host cannot resolve a preview capability. */ }
					sendPayload = { ...payload, session: { ...payload.session, page: { ...payload.session.page, url: pageUrl } } };
				}
				const message = formatBrowserAnnotationMessage(sendPayload, { screenshotPaths });
				const { error } = await client.POST("/api/v1/sessions/{sessionId}/send", {
					params: { path: { sessionId: sendSessionId } },
					body: { message, userAuthored: true },
				});
				if (error) {
					failureMessage = apiErrorMessage(error, appI18n.t("browser.unableSendAnnotation"));
					return;
				}
				sent = true;
				stagedScreenshotPathsRef.current.delete(payload);
				await window.tallpine?.browser.completeAnnotation?.({
					viewId: payload.viewId,
					tabId: payload.tabId,
					pageKey: payload.pageKey,
					sessionToken: payload.sessionToken,
					success: true,
				});
			} catch (error) {
				failureMessage = apiErrorMessage(error, appI18n.t("browser.unableSendAnnotation"));
			} finally {
				if (sendGeneration !== generationRef.current || sendSessionId !== sessionIdRef.current) return;
				annotationSendingRef.current = false;
				if (!sent) {
					annotationQueueRef.current.unshift(payload);
					setState({
						status: "error",
						error: failureMessage,
						queuedCount: annotationQueueRef.current.length,
					});
					return;
				}

				const queuedCount = annotationQueueRef.current.length;
				setState({ status: queuedCount > 0 ? "queued" : "sent", error: "", queuedCount });
				if (queuedCount > 0) {
					drainAnnotationQueue();
				} else {
					if (sentTimerRef.current !== null) window.clearTimeout(sentTimerRef.current);
					sentTimerRef.current = window.setTimeout(() => {
						sentTimerRef.current = null;
						setState((current) =>
							current.status === "sent" ? { status: "idle", error: "", queuedCount: 0 } : current,
						);
					}, 2_000);
				}
			}
		})();
	}, []);

	useEffect(() => {
		sessionIdRef.current = sessionId ?? "";
		hostIdRef.current = hostId;
		sourcePreviewUrlRef.current = sourcePreviewUrl;
		resetQueue();
	}, [hostId, resetQueue, sessionId, sourcePreviewUrl]);

	useEffect(() => {
		if (navUrl) return;
		resetQueue();
	}, [navUrl, resetQueue]);

	useEffect(
		() => () => {
			if (sentTimerRef.current !== null) window.clearTimeout(sentTimerRef.current);
		},
		[],
	);

	const beginPicking = useCallback(() => {
		setState((current) => ({ ...current, status: "picking", error: "" }));
	}, []);

	const cancelPicking = useCallback(() => {
		setState((current) => ({
			status: annotationQueueRef.current.length > 0 ? "queued" : current.status === "sending" ? "sending" : "idle",
			error: "",
			queuedCount: annotationQueueRef.current.length,
		}));
	}, []);

	const failPicking = useCallback((message: string) => {
		setState({ status: "error", error: message, queuedCount: annotationQueueRef.current.length });
	}, []);

	const enqueue = useCallback(
		(payload: BrowserAnnotationSubmitPayload) => {
			annotationQueueRef.current.push(payload);
			setState({ status: "queued", error: "", queuedCount: annotationQueueRef.current.length });
			drainAnnotationQueue();
		},
		[drainAnnotationQueue],
	);

	const retryQueued = useCallback(() => {
		if (annotationQueueRef.current.length === 0) return;
		setState({ status: "queued", error: "", queuedCount: annotationQueueRef.current.length });
		drainAnnotationQueue();
	}, [drainAnnotationQueue]);

	return {
		status: state.status,
		error: state.error,
		queuedCount: state.queuedCount,
		beginPicking,
		cancelPicking,
		enqueue,
		failPicking,
		retryQueued,
	};
}

export function BrowserPanel({
	session,
	active,
	poppedOut,
	onTogglePopOut,
	topbarHost,
}: BrowserPanelProps) {
	const browserView = useBrowserView({
		sessionId: session.id,
		active,
		poppedOut,
		previewUrl: session.previewUrl,
		previewRevision: session.previewRevision,
	});
	const annotationQueue = useBrowserAnnotationQueue({
		sessionId: session.id,
		navUrl: browserView.navState.url,
	});
	return (
		<BrowserPanelView
			active={active}
			annotationQueue={annotationQueue}
			browserView={browserView}
			onTogglePopOut={onTogglePopOut}
			poppedOut={poppedOut}
			session={session}
			topbarHost={topbarHost}
		/>
	);
}

export function BrowserPanelView(props: BrowserPanelViewProps) {
	const {
		poppedOut,
		topbarHost,
	} = props;
	const state = useBrowserPanelState(props);
	const actions = useBrowserPanelActions(props, state);
	const chrome = useBrowserPanelChrome(props, state, actions);
	const {
		annotationMode,
		navState,
		prefersReducedMotion,
		tabs,
		viewId,
	} = state;
	const {
		agentStatusLabel,
	} = actions;
	const {
		annotationToolbar,
		browserTabBar,
	} = chrome;
	const v = { ...state, ...actions, ...chrome };
	return (
		<div
			className={cn(
				"browser-panel flex h-full min-h-browser-min flex-col overflow-hidden border border-border bg-background",
				poppedOut && "browser-panel--popped-out",
				agentStatusLabel && "browser-panel--agent-active",
			)}
			data-browser-dock-target={poppedOut ? undefined : ""}
			data-browser-native-page={navState.url ? "live" : "empty"}
			data-testid="browser-panel"
			onBlurCapture={(event: FocusEvent<HTMLDivElement>) => {
				if (!viewId || event.currentTarget.contains(event.relatedTarget)) return;
				// Focus moving into the portaled omnibox is still browser chrome — do
				// not drop the shortcut target or ⌘T/⌘W will create/close terminals.
				if (topbarHost && event.relatedTarget instanceof Node && topbarHost.contains(event.relatedTarget)) {
					return;
				}
				// relatedTarget is null or body/documentElement when focus leaves the
				// document entirely (e.g. into the native page after a shortcut-driven
				// tab close) or when an unmounting element drops focus to document.body.
				// That is still browser context, so keep the shortcut target.
				if (
					!event.relatedTarget ||
					event.relatedTarget === document.body ||
					event.relatedTarget === document.documentElement
				) {
					return;
				}
				window.tallpine?.browser.notifyPanelBlur(viewId);
			}}
			onFocusCapture={() => {
				if (viewId) window.tallpine?.browser.notifyPanelUsed(viewId);
			}}
			onPointerDownCapture={() => {
				if (viewId) window.tallpine?.browser.notifyPanelUsed(viewId);
			}}
			role="tabpanel"
		>
			<BrowserToolbar props={props} v={v} />
			{tabs.length > 1 ? <div className="browser-panel__tabs-row" data-testid="browser-tabs-row">{browserTabBar}</div> : null}
			<AnimatePresence initial={false}>
				{annotationMode ? (
					<motion.div
						key="annotation-toolbar"
						className="browser-panel__annotation-row"
						data-testid="browser-annotation-toolbar"
						initial={prefersReducedMotion ? false : { height: 0, opacity: 0 }}
						animate={{ height: "auto", opacity: 1 }}
						exit={prefersReducedMotion ? undefined : { height: 0, opacity: 0 }}
						transition={
							prefersReducedMotion
								? { duration: 0 }
								: { duration: 0.18, ease: [0.22, 1, 0.36, 1] }
						}
					>
						{annotationToolbar}
					</motion.div>
				) : null}
			</AnimatePresence>
			<BrowserFindBar v={v} />
			<BrowserBody v={v} />
		</div>
	);
}
