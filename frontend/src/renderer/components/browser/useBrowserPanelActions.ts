import { useCallback, useEffect, useLayoutEffect, type FocusEvent, type FormEvent, type KeyboardEvent } from "react";
import { useBrowserRuntimeState } from "../../hooks/useBrowserRuntimeState";
import { appI18n } from "../../i18n";
import { isLocalDaemonUrl, useDaemonTcpOpen } from "../../lib/daemon-tcp";
import { isWebLink, openLinkInSystemBrowser } from "../../lib/external-link-policy";
import { tallpineBridge } from "../../lib/bridge";
import { useBrowserPanelState } from "./useBrowserPanelState";
import { BrowserPanelViewProps } from "./browser-types";
import { MAX_HISTORY_SUGGESTIONS } from "./browser-constants";
import { agentActivityLabel, browserRuntimeRejectedKey } from "./browser-helpers";

/** Screenshot, capture, annotation and navigation actions for the browser panel. */
export function useBrowserPanelActions(props: BrowserPanelViewProps, state: ReturnType<typeof useBrowserPanelState>) {
	const {
		session,
	} = props;
	const {
		activeHistorySuggestion,
		agentBrowserActive,
		agentBrowserActivity,
		agentPageOnHost,
		annotationMode,
		beginPicking,
		canAnnotate,
		canRetryAnnotation,
		cancelPicking,
		closeTab,
		copyFeedbackTimeoutRef,
		enqueue,
		error,
		failPicking,
		hasNativeBrowser,
		historyMenuRef,
		historyRequestGenerationRef,
		historySuggestions,
		navState,
		navigate,
		openTab,
		queuedCount,
		retryQueued,
		selectTab,
		setActiveHistorySuggestion,
		setAnnotationMode,
		setHistorySuggestions,
		setRuntimeReconnectPending,
		setUrlCopied,
		setUrlEditing,
		setUrlInput,
		showGlobalToast,
		status,
		t,
		urlCopied,
		urlEditing,
		urlInput,
		urlInputRef,
		viewId,
		profileState,
	} = state;
	const takeScreenshot = useCallback(async () => {
		if (!viewId || !window.tallpine?.browser) return;
		try {
			await window.tallpine.browser.captureScreenshot(viewId);
			showGlobalToast(t("browser.screenshotCopied"), undefined, "top-center");
		} catch {
			showGlobalToast(t("browser.screenshotFailed"), undefined, "top-center");
		}
	}, [showGlobalToast, t, viewId]);

	useEffect(() => {
		setUrlInput(navState.url);
		setUrlCopied(false);
		clearTimeout(copyFeedbackTimeoutRef.current);
		setHistorySuggestions([]);
		setActiveHistorySuggestion(-1);
		// A prior submit (typed, or pasted, then Enter) leaves the caret at the
		// end of the old value; the browser keeps that same horizontal scroll
		// position for the new value, scrolling the scheme/host off the left
		// edge (e.g. showing "://example.com" instead of "https://example.com").
		// Reset it once the DOM has the new value committed, so the address is
		// readable from the start like a real address bar after navigating.
		const frame = window.requestAnimationFrame(() => {
			if (urlInputRef.current) urlInputRef.current.scrollLeft = 0;
		});
		return () => {
			window.cancelAnimationFrame(frame);
			clearTimeout(copyFeedbackTimeoutRef.current);
		};
	}, [navState.url]);

	useEffect(() => {
		const generation = ++historyRequestGenerationRef.current;
		const query = urlInput.trim();
		if (
			!urlEditing ||
			!window.tallpine?.browser ||
			!viewId ||
			!profileState.profileId ||
			query === navState.url ||
			query.length < 1
		) {
			setHistorySuggestions([]);
			return;
		}
		let current = true;
		const timer = window.setTimeout(() => {
			void window.tallpine!.browser.historySuggestions({ viewId, query }).then(
				(suggestions) => {
					if (!current || historyRequestGenerationRef.current !== generation) return;
					setHistorySuggestions(suggestions.slice(0, MAX_HISTORY_SUGGESTIONS));
					setActiveHistorySuggestion(-1);
				},
				() => {
					if (!current || historyRequestGenerationRef.current !== generation) return;
					setHistorySuggestions([]);
					setActiveHistorySuggestion(-1);
				},
			);
		}, 120);
		return () => {
			current = false;
			window.clearTimeout(timer);
		};
	}, [navState.url, profileState.profileId, urlEditing, urlInput, viewId]);

	useLayoutEffect(() => {
		if (!urlEditing) return;
		urlInputRef.current?.select();
	}, [urlEditing]);

	useEffect(() => {
		const onPageFocus = window.tallpine?.browser.onPageFocus;
		if (!onPageFocus) return;
		return onPageFocus((focusedViewId) => {
			if (focusedViewId !== viewId) return;
			historyRequestGenerationRef.current += 1;
			urlInputRef.current?.blur();
			setUrlEditing(false);
			setUrlInput(navState.url);
			setHistorySuggestions([]);
			setActiveHistorySuggestion(-1);
		});
	}, [navState.url, viewId]);

	useEffect(() => {
		const offSubmit = window.tallpine?.browser.onAnnotationSubmit((payload) => {
			if (payload.viewId !== viewId) return;
			enqueue(payload);
		});
		const offCancel = window.tallpine?.browser.onAnnotationCancel((payload) => {
			if (payload.viewId !== viewId) return;
			cancelPicking();
		});
		return () => {
			offSubmit?.();
			offCancel?.();
		};
	}, [cancelPicking, enqueue, viewId]);

	const navigateFromAddressBar = (url: string) => {
		historyRequestGenerationRef.current += 1;
		urlInputRef.current?.blur();
		setUrlEditing(false);
		setUrlInput(url);
		setHistorySuggestions([]);
		setActiveHistorySuggestion(-1);
		void navigate(url);
	};

	const submit = (event: FormEvent<HTMLFormElement>) => {
		event.preventDefault();
		const nextURL = urlInput.trim();
		if (nextURL) navigateFromAddressBar(nextURL);
	};

	const handleURLChange = (value: string) => {
		historyRequestGenerationRef.current += 1;
		setUrlInput(value);
		setActiveHistorySuggestion(-1);
	};

	const handleURLKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
		if (event.key === "Escape") {
			event.preventDefault();
			historyRequestGenerationRef.current += 1;
			setHistorySuggestions([]);
			setActiveHistorySuggestion(-1);
			return;
		}
		if (historySuggestions.length === 0) return;
		if (event.key === "ArrowDown" || event.key === "ArrowUp") {
			event.preventDefault();
			const direction = event.key === "ArrowDown" ? 1 : -1;
			setActiveHistorySuggestion((current) => {
				if (current < 0) return direction > 0 ? 0 : historySuggestions.length - 1;
				return (current + direction + historySuggestions.length) % historySuggestions.length;
			});
			return;
		}
		if (event.key === "Enter" && activeHistorySuggestion >= 0) {
			event.preventDefault();
			navigateFromAddressBar(historySuggestions[activeHistorySuggestion]!.url);
			return;
		}
	};

	const endUrlEditing = (event: FocusEvent<HTMLInputElement>) => {
		if (event.relatedTarget instanceof Node && historyMenuRef.current?.contains(event.relatedTarget)) return;
		historyRequestGenerationRef.current += 1;
		setUrlEditing(false);
		setUrlInput(navState.url);
		setHistorySuggestions([]);
		setActiveHistorySuggestion(-1);
	};

	const beginUrlEditing = () => {
		setUrlEditing(true);
	};

	// A remote preview's address works only in this panel, which trusts its
	// certificate: outside Tallpine it goes by its plain-http address.
	const externalURL = async (url: string): Promise<string> => {
		if (!session.hostId) return url;
		// A disconnected host has no other address for it: keep the one shown.
		return tallpineBridge.remotes.externalUrl(session.hostId, url).catch(() => url);
	};

	// B180: with loopback TCP off, a local preview URL is a dead link outside the app.
	const daemonTcpOpen = useDaemonTcpOpen();
	const externalBlocked = !daemonTcpOpen && !session.hostId && isLocalDaemonUrl(navState.url);

	const openCurrentPageExternally = async () => {
		if (!isWebLink(navState.url) || agentPageOnHost || externalBlocked) return;
		await openLinkInSystemBrowser(await externalURL(navState.url));
	};

	const copyCurrentURL = async () => {
		if (!navState.url) return;
		try {
			await tallpineBridge.clipboard.writeText(await externalURL(navState.url));
			setUrlCopied(true);
			clearTimeout(copyFeedbackTimeoutRef.current);
			copyFeedbackTimeoutRef.current = setTimeout(() => setUrlCopied(false), 1_200);
		} catch {
			showGlobalToast(t("browser.urlCopyFailed"), undefined, "top-center");
		}
	};

	const toggleAnnotationMode = async () => {
		if (!canAnnotate || status === "sending") return;
		if (canRetryAnnotation) {
			retryQueued();
			return;
		}
		const next = !(annotationMode || status === "picking");
		try {
			await setAnnotationMode(next);
			if (next) {
				beginPicking();
			} else {
				cancelPicking();
			}
		} catch (error) {
			failPicking(error instanceof Error ? error.message : appI18n.t("browser.unableStartAnnotation"));
		}
	};

	// A blank new tab has nowhere to go on its own, so send focus straight to
	// the URL bar afterward instead of leaving the user to click into it.
	const handleOpenTab = useCallback(async () => {
		await openTab();
		urlInputRef.current?.focus();
		urlInputRef.current?.select();
	}, [openTab]);

	const handleSelectTab = useCallback(
		async (tabId: string) => {
			try {
				await selectTab(tabId);
			} catch {
				// The existing tab remains active.
			}
		},
		[selectTab],
	);
	const handleCloseTab = useCallback(
		(tabId: string) => {
			void closeTab(tabId);
		},
		[closeTab],
	);

	const annotationStatusLabel =
		status === "picking"
			? t("browser.pickElement")
			: status === "queued"
				? queuedCount > 1
					? t("browser.queuedCount", { count: queuedCount })
					: t("browser.queued")
				: status === "sending"
					? t("browser.sending")
					: status === "sent"
						? t("browser.sent")
						: status === "error"
							? error
							: "";
	const agentStatusLabel = agentActivityLabel(agentBrowserActivity, agentBrowserActive);
	const runtimeState = useBrowserRuntimeState();
	// A handshake in flight (launch, Reconnect, a daemon restart's first redial) is
	// not a disconnect yet; alarming then would flash the alert on every relink.
	const runtimeDisconnected =
		hasNativeBrowser && runtimeState !== null && !runtimeState.connected && !runtimeState.connecting;
	const runtimeRejectedMessage = runtimeState?.rejected ? t(browserRuntimeRejectedKey(runtimeState.rejectionCode)) : null;
	const reconnectRuntime = useCallback(async () => {
		setRuntimeReconnectPending(true);
		try {
			await window.tallpine?.browser.reconnectRuntime();
		} finally {
			setRuntimeReconnectPending(false);
		}
	}, []);
	const suggestionsOpen = urlEditing && historySuggestions.length > 0;
	const currentURLIsWeb = isWebLink(navState.url);
	const copyURLLabel = t(urlCopied ? "browser.urlCopied" : "browser.copyUrl");
	return {
		agentStatusLabel,
		annotationStatusLabel,
		beginUrlEditing,
		copyCurrentURL,
		copyURLLabel,
		currentURLIsWeb,
		endUrlEditing,
		externalBlocked,
		handleCloseTab,
		handleOpenTab,
		handleSelectTab,
		handleURLChange,
		handleURLKeyDown,
		navigateFromAddressBar,
		openCurrentPageExternally,
		reconnectRuntime,
		runtimeDisconnected,
		runtimeRejectedMessage,
		runtimeState,
		submit,
		suggestionsOpen,
		takeScreenshot,
		toggleAnnotationMode,
	};
}
