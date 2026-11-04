import { Plus } from "lucide-react";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef } from "react";
import { useBrowserAnnotationQueue } from "../BrowserPanel";
import { initialInspectorSize, inspectorSizing, sizingGeometryEqual, INSPECTOR_SPRING_MS, type InspectorSizing } from "../SessionInspectorRail";
import { SessionFileTab } from "../SessionFileTabs";
import { isArtifactPreviewUrl } from "../../lib/artifact-preview";
import { SessionTabMenu } from "../TerminalAgentControls";
import { ShellTopbar } from "../ShellTopbar";
import { TopbarButton } from "../TopbarButton";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import { useBrowserView } from "../../hooks/useBrowserView";
import { useFileAnnotation } from "../../hooks/useFileAnnotation";
import { adoptedShellHandle } from "../../hooks/useShellTerminals";
import { conversationQueryKey } from "../../hooks/conversation";
import { workspaceQueryKeyForHost } from "../../hooks/useWorkspaceQuery";
import { apiErrorMessage } from "../../lib/api-client";
import { clientForSessionHost } from "../../lib/host-clients";
import { isOrchestratorSession, sessionIsActive } from "../../types/workspace";
import { terminalTargetBelongsToSession, type TerminalTarget } from "../../types/terminal";
import { useUiStore, type InspectorView } from "../../stores/ui-store";
import { INSPECTOR_SEPARATOR_RESERVE_PX } from "../../lib/inspector-width";
import { useSessionViewData } from "./useSessionViewData";
import { useSessionViewTerminals } from "./useSessionViewTerminals";
import { SessionViewProps } from "./session-view-types";
import { INSPECTOR_COMPACT_MAX_PX, topbarSecondaryLabelMode, newTerminalShortcutLabel } from "./session-view-helpers";

/** Center workspace tabs, files and browser content state for SessionView. */
export function useSessionViewWorkspace(props: SessionViewProps, data: ReturnType<typeof useSessionViewData>, terms: ReturnType<typeof useSessionViewTerminals>) {
	const {
		sessionId,
		hostId,
	} = props;
	const {
		availableReviewerTerminal,
		browserOnly,
		browserPoppedOut,
		centerFileRequestsBySession,
		chatDraftBoundaries,
		daemonStatus,
		dirtyFiles,
		fileTabs,
		hostLabel,
		initializeInspectorSession,
		inspectorPanelVisible,
		inspectorView,
		interfaceUi,
		queryClient,
		remoteBase,
		remoteSessionQuery,
		reviewerChatId,
		reviewerQuery,
		reviewerSwitchPending,
		session,
		sessionSplitRef,
		setBrowserPopOutState,
		setInspectorOpenForSession,
		setInspectorViewForSession,
		setTerminalTarget,
		startTerminalLiveResize,
		t,
		terminalTarget,
		uiSessionId,
		workspaceResizeTimerRef,
	} = data;
	const {
		activateCenterFile,
		activeShellTerminalHandleId,
		addShellTerminal,
		availableReviewerChat,
		closeCenterFile,
		openShellTerminal,
		selectReviewerChat,
		selectReviewerTerminal,
		selectSessionTerminal,
		selectShellTerminal,
		setActiveShellTerminal,
		shellTerminals,
		shellTerminalsQuery,
	} = terms;
	useEffect(() => {
		if (!activeShellTerminalHandleId) return;
		const shell = shellTerminals.find((s) => s.handleId === activeShellTerminalHandleId);
		if (!shell) return;
		if (terminalTarget.kind === "shell" && terminalTarget.handleId === shell.handleId &&
			terminalTarget.generation === shell.createdAt && terminalTarget.title === shell.title &&
			!fileTabs.activePath && !reviewerChatId) return;
		selectShellTerminal(shell.handleId);
	}, [activeShellTerminalHandleId, fileTabs.activePath, reviewerChatId, selectShellTerminal, shellTerminals, terminalTarget]);
	// A tab selected while it was pending follows it to the shell it became.
	// Only what still points at the pending tab moves: the user may have
	// selected another tab in the meantime.
	useEffect(() => {
		const adoptedActive = activeShellTerminalHandleId ? adoptedShellHandle(activeShellTerminalHandleId) : undefined;
		if (adoptedActive) {
			setActiveShellTerminal(adoptedActive);
			return;
		}
		if (terminalTarget.kind !== "shell") return;
		const adoptedTarget = adoptedShellHandle(terminalTarget.handleId);
		if (adoptedTarget && shellTerminals.some((shell) => shell.handleId === adoptedTarget)) {
			selectShellTerminal(adoptedTarget);
		}
	}, [activeShellTerminalHandleId, selectShellTerminal, setActiveShellTerminal, shellTerminals, terminalTarget]);

	// If the pane is pointed at a shell that is not in THIS session's strip — e.g.
	// after navigating to a different session whose globally-active shell belongs
	// elsewhere — fall back to the session's own pane rather than render a tab
	// that isn't shown here.
	useEffect(() => {
		setTerminalTarget((current) =>
			current.kind === "shell" && !shellTerminals.some((s) => s.handleId === current.handleId)
				? { kind: "worker" }
				: current,
		);
	}, [shellTerminals]);
	// Reviewer selection belongs to the tab, not its replaceable controller.
	// Keep it through switch teardown, then follow the replacement surface.
	useEffect(() => {
		if ((!reviewerChatId && terminalTarget.kind !== "reviewer") || !reviewerQuery.isFetched || reviewerSwitchPending) return;
		if (availableReviewerChat) {
			if (reviewerChatId !== availableReviewerChat.reviewId) selectReviewerChat(availableReviewerChat.reviewId);
		} else if (availableReviewerTerminal) {
			if (terminalTarget.kind !== "reviewer" || terminalTarget.handleId !== availableReviewerTerminal.handleId || terminalTarget.harness !== availableReviewerTerminal.harness) {
				selectReviewerTerminal(availableReviewerTerminal);
			}
		} else {
			selectSessionTerminal();
		}
	}, [availableReviewerChat, availableReviewerTerminal, reviewerChatId, reviewerQuery.isFetched, reviewerSwitchPending, selectReviewerChat, selectReviewerTerminal, selectSessionTerminal, terminalTarget]);
	const isOrchestrator = session ? isOrchestratorSession(session) : false;
	const hasInspector = Boolean(session);
	const sizing = useMemo(() => inspectorSizing(inspectorView), [inspectorView]);
	const browserEntryWidthFloorRef = useRef<number | null>(null);

	// Arm the shared width transition before the selected inspector surface
	// changes its CSS variable. Browser becomes a co-work canvas; utility views
	// return to their stable rail width on the same spring as the shell sidebar.
	const armWorkspaceTransition = useCallback(() => {
		const split = sessionSplitRef.current;
		if (!split) return;
		if (workspaceResizeTimerRef.current !== null) window.clearTimeout(workspaceResizeTimerRef.current);
		split.setAttribute("data-workspace-resizing", "true");
		void split.offsetWidth;
		workspaceResizeTimerRef.current = window.setTimeout(() => {
			split.removeAttribute("data-workspace-resizing");
			workspaceResizeTimerRef.current = null;
		}, INSPECTOR_SPRING_MS);
	}, []);

	useEffect(
		() => () => {
			if (workspaceResizeTimerRef.current !== null) window.clearTimeout(workspaceResizeTimerRef.current);
			sessionSplitRef.current?.removeAttribute("data-workspace-resizing");
		},
		[],
	);

	const prepareWorkspaceProfile = useCallback(
		(nextSizing: InspectorSizing) => {
			armWorkspaceTransition();
			const groupWidth = sessionSplitRef.current?.clientWidth || window.innerWidth;
			const availableWidth = Math.max(0, groupWidth - INSPECTOR_SEPARATOR_RESERVE_PX);
			const targetInspectorWidth = Number.parseFloat(initialInspectorSize(nextSizing, availableWidth));
			startTerminalLiveResize(
				targetInspectorWidth <= INSPECTOR_COMPACT_MAX_PX ? "compact" : "expanded",
				topbarSecondaryLabelMode(Math.max(0, availableWidth - targetInspectorWidth)),
			);
		},
		[armWorkspaceTransition, startTerminalLiveResize],
	);

	const transitionInspectorView = useCallback(
		(next: InspectorView, remember = false) => {
			if (next === inspectorView) return;
			if (next === "browser") {
				const currentWidth = Number(window.localStorage.getItem(sizing.storageKey));
				browserEntryWidthFloorRef.current = Number.isFinite(currentWidth) ? currentWidth : null;
			} else {
				browserEntryWidthFloorRef.current = null;
			}
			const nextSizing = inspectorSizing(next);
			if (!sizingGeometryEqual(sizing, nextSizing)) prepareWorkspaceProfile(nextSizing);
			setInspectorViewForSession(uiSessionId, next, remember);
		},
		[
			inspectorView,
			prepareWorkspaceProfile,
			uiSessionId,
			setInspectorViewForSession,
			sizing,
		],
	);

	const newTerminalError = openShellTerminal.error ? apiErrorMessage(openShellTerminal.error) : undefined;
	const newShellTerminalAction = useMemo(() =>
		session && !isOrchestrator ? (
			<Tooltip>
				<TooltipTrigger asChild>
					<TopbarButton
						aria-label={t("shortcut.new-shell-terminal")}
						data-terminal-focus-handoff="true"
						onClick={addShellTerminal}
						type="button"
						variant="icon"
					>
						<Plus aria-hidden="true" className="size-icon-md" />
					</TopbarButton>
				</TooltipTrigger>
				<TooltipContent side="bottom">
					{newTerminalError ?? t("terminal.newWithShortcut", { shortcut: newTerminalShortcutLabel })}
				</TooltipContent>
			</Tooltip>
		) : null,
		[addShellTerminal, isOrchestrator, newTerminalError, newTerminalShortcutLabel, session, t],
	);
	const fileAnnotation = useFileAnnotation(sessionId, { hostId });
	const centerFileTabs = useMemo(
		() =>
			fileTabs.openPaths.map((path) => ({
				key: `file:${path}`,
				content: (
					<SessionFileTab
						active={fileTabs.activePath === path}
						dirty={Boolean(dirtyFiles[path])}
						onActivate={() => activateCenterFile(path)}
						onClose={() => closeCenterFile(path)}
						path={centerFileRequestsBySession[uiSessionId]?.[path]?.path ?? path}
						sourceLabel={
							centerFileRequestsBySession[uiSessionId]?.[path]?.source?.kind === "snapshot"
								? (centerFileRequestsBySession[uiSessionId]?.[path]?.source as { label?: string })?.label
								: undefined
						}
					/>
				),
				onSelect: () => activateCenterFile(path),
				onClose: () => closeCenterFile(path),
			})),
		[activateCenterFile, closeCenterFile, dirtyFiles, fileTabs.activePath, fileTabs.openPaths, centerFileRequestsBySession, uiSessionId],
	);

	const activeWorkspaceTabKey = fileTabs.activePath ? `file:${fileTabs.activePath}` : undefined;
	const previewUrl = session?.previewUrl?.trim() || undefined;
	const previewRevision = session?.previewRevision;
	const browserSlotVisible = Boolean(
		session &&
			hasInspector &&
			(browserPoppedOut || (inspectorPanelVisible && inspectorView === "browser")),
	);
	useEffect(() => {
		if (hostId && remoteBase && browserSlotVisible) void remoteSessionQuery.refetch();
	}, [hostId, remoteBase, browserSlotVisible, remoteSessionQuery.refetch]);
	const terminated = session ? !sessionIsActive(session) : false;
	// A completed session's HTML artifact is static output the daemon keeps
	// serving, so an artifact preview the user opened stays visible; every other
	// preview of a terminated session is a stale DB fact and is still torn down.
	const browserTerminated = terminated && !(isArtifactPreviewUrl(previewUrl) && !hostId);
	const browserView = useBrowserView({
		sessionId: uiSessionId,
		origin: hostId ? { hostId, sessionId, proxyBase: remoteBase ?? "" } : undefined,
		active: browserSlotVisible,
		poppedOut: browserPoppedOut,
		terminated: browserTerminated,
		previewUrl,
		previewRevision,
	});
	const browserAnnotationQueue = useBrowserAnnotationQueue({
		sessionId: session?.id,
		hostId,
		sourcePreviewUrl: session?.previewUrl,
		navUrl: browserView.navState.url,
	});
	const browserViewId = browserView.viewId;
	useEffect(
		() =>
			window.tallpine?.browser.onClosePanel((targetViewId) => {
				if (!browserViewId || targetViewId !== browserViewId) return;
				setBrowserPopOutState({ sessionId, phase: "docked" });
				setInspectorOpenForSession(uiSessionId, false);
			}),
		[browserViewId, sessionId, uiSessionId, setInspectorOpenForSession],
	);
	const browserUrl = browserView.navState.url.trim();
	// A terminated session's `previewUrl` is a stale DB fact; useBrowserView
	// suppresses and destroys the live preview for it, so it must not count as
	// content here either — otherwise a merged/terminated session with an old
	// preview auto-opens Browser onto a view the hook has already torn down.
	const hasBrowserContent = !browserTerminated && Boolean(previewUrl || browserUrl);

	// Entering a session for the first time ever always starts on Summary. This
	// must fire exactly once per session's *lifetime*, not once per "was this
	// the last session I looked at" or "is this view currently mounted" — so
	// the initialized flag lives in the ui-store (inspectorSessions[sessionId])
	// rather than a component-local ref, and survives both re-entering a
	// different previously-visited session and unmounting/remounting this view
	// entirely (e.g. across route transitions). Treat browser content that
	// already existed when the route resolved as the baseline for that visit;
	// only preview work arriving afterward may reveal Browser automatically.
	useLayoutEffect(() => {
		if (!session) return;
		if (browserOnly) {
			if (!useUiStore.getState().inspectorSessions[uiSessionId]) setInspectorOpenForSession(uiSessionId, false);
			initializeInspectorSession(uiSessionId, hasBrowserContent, true);
			return;
		}
		initializeInspectorSession(uiSessionId, hasBrowserContent, hasInspector);
	}, [browserOnly, hasBrowserContent, hasInspector, session, uiSessionId, initializeInspectorSession, setInspectorOpenForSession, setInspectorViewForSession]);

	// Keep rejecting another session's shell/reviewer handle here as well, so it
	// can never be cached under the destination session.
	const routedTerminalTarget = terminalTargetBelongsToSession(terminalTarget, sessionId)
		? terminalTarget
		: ({ kind: "worker" } satisfies TerminalTarget);
	// Chat surface stays mounted in chat mode for worker, reviewer, and shell
	// targets. A terminal pane (reviewer or shell) renders as a tab inside the
	// chat surface, so opening one never costs the user the conversation.
	const chatTargetKind = routedTerminalTarget.kind;
	const renderedSessionMode = interfaceUi.renderedMode;
	const showChatSurface =
		session !== undefined &&
		renderedSessionMode === "chat" &&
		(chatTargetKind === "worker" || chatTargetKind === "reviewer" || chatTargetKind === "shell");
	const chatViewActive =
		session?.mode === "chat" &&
		(hostId ? Boolean(remoteBase) : daemonStatus.state === "ready") &&
		routedTerminalTarget.kind === "worker" &&
		(!activeShellTerminalHandleId ||
			(shellTerminalsQuery.data !== undefined &&
				!shellTerminals.some((shell) => shell.handleId === activeShellTerminalHandleId))) &&
		!reviewerChatId &&
		!fileTabs.activePath;
	useEffect(() => {
		if (!chatViewActive) return;
		const viewId = crypto.randomUUID();
		let left = false;
		let refreshed = false;
		let pending = Promise.resolve();
		const setViewActive = async (active: boolean) => {
			try {
				const { error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/chat-view", {
					params: { path: { sessionId } },
					body: { viewId, active },
				});
				if (error) throw error;
				// Remote conversations already refresh every two seconds.
				if (active && !left && !refreshed && !hostId) {
					refreshed = true;
					void queryClient.invalidateQueries({ queryKey: conversationQueryKey(sessionId, hostId) });
					void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) });
				}
			} finally {
				// Release a late registration too; leaving must not wait on wake.
				if (active && left) void setViewActive(false).catch(() => {});
			}
		};
		const renewView = () => {
			pending = pending.catch(() => {}).then(() => {
				return left ? undefined : setViewActive(true);
			});
			return pending;
		};
		const refreshAfterWakeError = () => {
			if (left || hostId) return;
			void queryClient.invalidateQueries({ queryKey: conversationQueryKey(sessionId, hostId) });
		};
		void renewView().catch(refreshAfterWakeError);
		const renewal = window.setInterval(() => { void renewView().catch(refreshAfterWakeError); }, 10_000);
		return () => {
			left = true;
			window.clearInterval(renewal);
			void setViewActive(false).catch(() => {});
		};
	}, [chatViewActive, hostId, queryClient, sessionId]);
	// A local session's tab menu holds the interface switch and, in a terminal, its agent.
	const agentWorking = chatDraftBoundaries.length > 0 || session?.isTerminated === true ||
		session?.activity?.state === "active" || session?.activity?.state === "waiting_input" || session?.activity?.state === "blocked";
	const sessionTabActions = !session
		? null
		: (
			<SessionTabMenu
				key={uiSessionId}
				session={session}
				hostId={hostId}
				blocked={agentWorking}
				switchTarget={interfaceUi.target}
				switchAction={interfaceUi.switchAction}
				switchControl={interfaceUi.switchControl}
			/>
		);
	const sessionHeaderActions = (
		<div
			className="session-topbar-session-chrome flex shrink-0 items-center"
			data-compact-session-chrome="false"
		>
			{hostId ? <span className="max-w-40 truncate px-3 text-xs text-muted-foreground" title={hostId}>{hostLabel ?? hostId}</span> : null}
			<ShellTopbar embedded />
		</div>
	);
	const sessionTabActionWide = false;

	// The pane shows one terminal at a time, so selecting a shell or the reviewer
	// takes the agent's terminal off screen while the route still points here.
	// Publish which one is showing: the notification runtime lives outside this
	// subtree and must not treat "on the session route" as "watching the agent".
	return {
		activeWorkspaceTabKey,
		browserAnnotationQueue,
		browserEntryWidthFloorRef,
		browserView,
		centerFileTabs,
		fileAnnotation,
		hasBrowserContent,
		hasInspector,
		isOrchestrator,
		newShellTerminalAction,
		previewRevision,
		previewUrl,
		routedTerminalTarget,
		sessionHeaderActions,
		sessionTabActionWide,
		sessionTabActions,
		showChatSurface,
		sizing,
		terminated,
		transitionInspectorView,
	};
}
