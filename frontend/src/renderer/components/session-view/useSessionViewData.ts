import { useIsMutating, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useBlocker, useNavigate } from "@tanstack/react-router";
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { INSPECTOR_SPRING_MS } from "../SessionInspectorRail";
import type { ArtifactOpenRequest } from "../SessionArtifactsView";
import { useAgentAccountLabel } from "../../hooks/useAgentAccounts";
import { useSessionPlan } from "../../hooks/useSessionPlan";
import { orchestratorInspectorView } from "../../lib/orchestrator-inspector-view";
import { useSessionInterfaceSwitch } from "../../hooks/useSessionInterfaceSwitch";
import { canResumeAgent, resumeAgentOnOpen } from "../../hooks/useCanResumeAgent";
import { useSessionGoneRedirect } from "../../hooks/useSessionGoneRedirect";
import { useSessionInterfaceTransitionStatus } from "../../hooks/useSessionInterfaceTransition";
import { conversationQueryKey } from "../../hooks/conversation";
import { discardCapturedPendingFileAttachments } from "../../hooks/useFileAttachments";
import { useRemoteSessionProjectKind, useWorkspaceQuery, useWorkspaceSession, workspaceQueryKeyForHost } from "../../hooks/useWorkspaceQuery";
import { apiErrorMessage } from "../../lib/api-client";
import { clientForSessionHost } from "../../lib/host-clients";
import { useHostConnection } from "../../hooks/useHostConnection";
import { sessionReviewsQueryKey } from "../../lib/session-reviews";
import { sessionUiKey } from "../../lib/hosts";
import { filesProjectKind } from "../../lib/files-review-selection";
import { tallpineBridge } from "../../lib/bridge";
import { chatDraftDialogCopy } from "../../lib/chat-draft-boundary";
import { EMPTY_SESSION_FILE_TABS, type SessionFileTabState } from "../../lib/session-file-tabs";
import { useShell } from "../../lib/shell-context";
import { usesPreviewWorkspaceData } from "../../lib/preview-mode";
import { isOrchestratorSession, sessionAgentExited, sessionIsActive } from "../../types/workspace";
import { type TerminalTarget } from "../../types/terminal";
import { inspectorIsOpen, useResolvedTheme, useUiStore } from "../../stores/ui-store";
import { useSideChatsAvailable } from "../../lib/side-chats";
import { SessionViewProps, BrowserPopOutState, CenterFileOpenRequest, ReviewsResponse } from "./session-view-types";
import { EMPTY_AUXILIARY_TAB_ORDER, reviewerTerminalFromReviews } from "./session-view-helpers";

/** Session lookup, host connection, interface mode and session-scoped UI state for SessionView. */
export function useSessionViewData(props: SessionViewProps) {
	const {
		sessionId,
		projectId,
		hostId,
	} = props;
	const navigate = useNavigate();
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const uiSessionId = sessionUiKey(sessionId, hostId);
	const { baseUrl: remoteBase, label: hostLabel } = useHostConnection(hostId);
	const refreshWorkspaces = useCallback(
		() => queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) }),
		[hostId, queryClient],
	);
	const workspaceQuery = useWorkspaceQuery({ enabled: !hostId });
	const remoteSessionQuery = useWorkspaceSession(sessionId, hostId, false);
	const workspaces = hostId ? [] : workspaceQuery.data ?? [];
	const routedWorkspaces = projectId ? workspaces.filter((workspace) => workspace.id === projectId) : [];
	const listedMatches = (projectId ? routedWorkspaces : workspaces)
		.flatMap((workspace) => workspace.sessions.filter((candidate) => candidate.id === sessionId));
	const listedSession = listedMatches.length === 1 ? listedMatches[0] : undefined;
	const ambiguousRoute = listedMatches.length > 1 || Boolean(projectId && routedWorkspaces.length > 1 && !listedSession);
	const localLookupEnabled = !ambiguousRoute;
	const workspaceSessionQuery = useWorkspaceSession(sessionId, undefined, !hostId && localLookupEnabled);
	const fallbackSession = !projectId || workspaceSessionQuery.data?.workspaceId === projectId
		? workspaceSessionQuery.data : undefined;
	const session = hostId ? (remoteBase && !remoteSessionQuery.isError && remoteSessionQuery.data?.workspaceId === (projectId ?? remoteSessionQuery.data?.workspaceId) ? remoteSessionQuery.data : undefined)
		: ambiguousRoute ? undefined : listedSession ?? fallbackSession;
	const sessionGone = useSessionGoneRedirect({
		sessionId,
		projectId,
		session,
		settled: !workspaceQuery.isLoading && !workspaceSessionQuery.isLoading,
		workspaces,
		enabled: !hostId,
	});
	// The Files review offers only what the session's kind of project has.
	const remoteWorkspaceKind = useRemoteSessionProjectKind(sessionId, hostId);
	const sessionWorkspaceKind = hostId ? remoteWorkspaceKind : workspaces.find((workspace) => workspace.id === session?.workspaceId)?.kind;
	const filesKind = session ? filesProjectKind(session, sessionWorkspaceKind) : undefined;
	const interfaceContext = hostId ?? (session ? null : undefined);
	const interfaceUi = useSessionInterfaceSwitch(sessionId, session, interfaceContext);
	const accountLabel = useAgentAccountLabel(Boolean(interfaceUi.accountMoveTarget));
	const accountSwitchingLabel = interfaceUi.accountMoveTarget
		? t("settings.agentAccounts.switching", { email: accountLabel(interfaceUi.accountMoveTarget) })
		: undefined;
	const { draftBoundaries: chatDraftBoundaries, confirmUnsafeDraftLeave } = interfaceUi;
	const remoteHostsEnabled = useUiStore((state) => state.remoteHosts);
	const setRemoteHosts = useUiStore((state) => state.setRemoteHosts);
	useBlocker({
		disabled: chatDraftBoundaries.length === 0,
		enableBeforeUnload: chatDraftBoundaries.length > 0,
		shouldBlockFn: async () => {
			const decision = await confirmUnsafeDraftLeave();
			if (decision.kind === "cancelled") {
				if (hostId && !remoteHostsEnabled) setRemoteHosts(true);
				return true;
			}
			if (decision.kind === "confirmed") {
				// Route navigation is the boundary itself, so confirmed in-flight file
				// work can be invalidated now. Interface switches defer this until the
				// exact durable transition reports completed.
				discardCapturedPendingFileAttachments(decision.pendingAttachments);
			}
			return false;
		},
	});
	useEffect(() => {
		tallpineBridge.app.setChatDraftRisk?.(chatDraftBoundaries, chatDraftDialogCopy(chatDraftBoundaries));
	}, [chatDraftBoundaries, t]);
	useEffect(
		() => () => tallpineBridge.app.setChatDraftRisk?.([]),
		[sessionId],
	);
	const theme = useResolvedTheme();
	const browserOnly = Boolean(session && isOrchestratorSession(session));
	const isInspectorOpen = useUiStore((state) => inspectorIsOpen(state.inspectorSessions, uiSessionId));
	const [sideChatContainer, setSideChatContainer] = useState<HTMLDivElement | null>(null);
	const sideChatsAvailable = useSideChatsAvailable(session, hostId);
	const orchestratorPlan = useSessionPlan(browserOnly ? sessionId : undefined, hostId);
	const inspectorView = useUiStore((state) => {
		const stored = state.inspectorSessions[uiSessionId]?.view;
		// An orchestrator's inspector is the browser, plus the Plan tab.
		if (browserOnly) return orchestratorInspectorView(stored, Boolean(orchestratorPlan));
		return stored ?? "summary";
	});
	const setInspectorOpenForSession = useUiStore((state) => state.setInspectorOpen);
	const toggleInspector = useUiStore((state) => state.toggleInspector);
	const setInspectorViewForSession = useUiStore((state) => state.setInspectorView);
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const setFilesChangedOnly = useUiStore((state) => state.setFilesChangedOnly);
	const resetFilesSelection = useUiStore((state) => state.resetFilesSelection);
	const workspaceFileOpenRequest = useUiStore((state) => state.workspaceFileOpenRequest);
	const clearWorkspaceFileOpenRequest = useUiStore((state) => state.clearWorkspaceFileOpenRequest);
	const initializeInspectorSession = useUiStore((state) => state.initializeInspectorSession);
	const setBrowserContentRevealed = useUiStore((state) => state.setBrowserContentRevealed);
	const setBrowserUnseen = useUiStore((state) => state.setBrowserUnseen);
	const { daemonStatus } = useShell();
	const resumeStatus = useSessionInterfaceTransitionStatus(canResumeAgent(session) ? session?.id : undefined, hostId);
	const canResume = canResumeAgent(session, resumeStatus.transition) && !resumeStatus.isLoading && !resumeStatus.statusError;
	const openedSession = useRef({ key: uiSessionId, checked: false });
	const autoResume = useMutation({
		mutationKey: ["resume-agent", "local", sessionId],
		mutationFn: resumeAgentOnOpen,
		onSettled: async (_data, _error, id) => {
			await Promise.all([
				refreshWorkspaces(),
				queryClient.invalidateQueries({ queryKey: conversationQueryKey(id) }),
			]);
		},
	});
	// True while a background resume owns the chat surface: hide the stopped
	// banner and disable sending, but keep history readable. Covers the initial
	// check (before the mutation fires) and the mutation in flight. Once checked
	// is set, only the in-flight mutation keeps it true — switching back to an
	// already-resumed session no longer flashes "Resuming agent…" while the
	// workspace query catches up with the new activity state.
	const quietResume = (autoResume.variables === sessionId && autoResume.isPending) ||
		(!usesPreviewWorkspaceData && !hostId && canResumeAgent(session, resumeStatus.transition) &&
		!resumeStatus.statusError && openedSession.current.key === uiSessionId && !openedSession.current.checked);
	const resumeOnOpen = autoResume.mutate;
	useEffect(() => {
		if (openedSession.current.key !== uiSessionId) openedSession.current = { key: uiSessionId, checked: false };
		if (usesPreviewWorkspaceData || hostId || !session || daemonStatus.state !== "ready" ||
			(session.statusReadiness && session.statusReadiness !== "ready") || openedSession.current.checked) return;
		if (!sessionAgentExited(session)) {
			openedSession.current.checked = true;
			return;
		}
		if (!canResume) return;
		// One attempt per route opening; a failed resume or a later agent exit
		// stays stopped rather than entering an automatic restart loop.
		openedSession.current.checked = true;
		resumeOnOpen(session.id);
	}, [canResume, daemonStatus.state, hostId, resumeOnOpen, session, uiSessionId]);
	const previewBaselineRef = useRef<{ sessionId: string; key: string } | null>(null);
	const revealedBrowserCommandsRef = useRef<{ sessionId: string; commands: Set<string> }>({ sessionId: uiSessionId, commands: new Set() });
	const sessionSplitRef = useRef<HTMLDivElement | null>(null);
	const terminalLiveResizeTimerRef = useRef<number | null>(null);
	const workspaceResizeTimerRef = useRef<number | null>(null);
	const [inspectorSettledClosed, setInspectorSettledClosed] = useState(!isInspectorOpen);
	const inspectorPanelVisible = isInspectorOpen || !inspectorSettledClosed;
	const [terminalTarget, setTerminalTarget] = useState<TerminalTarget>({ kind: "worker" });
	const [reviewerChatId, setReviewerChatId] = useState<string | null>(null);
	const [browserPopOutState, setBrowserPopOutState] = useState<BrowserPopOutState>({
		sessionId,
		phase: "docked",
	});
	const [filesPoppedOut, setFilesPoppedOut] = useState(false);
	const [filesSplit, setFilesSplit] = useState(() => window.localStorage.getItem("ao.files.diffStyle") === "split");
	const [artifactRequestsBySession, setArtifactRequestsBySession] = useState<Record<string, ArtifactOpenRequest>>({});
	const [fileTabsBySession, setFileTabsBySession] = useState<Record<string, SessionFileTabState>>({});
	const fileTabs = fileTabsBySession[uiSessionId] ?? EMPTY_SESSION_FILE_TABS;
	const [dirtyFilesBySession, setDirtyFilesBySession] = useState<Record<string, Record<string, true>>>({});
	const dirtyFiles = dirtyFilesBySession[uiSessionId] ?? {};
	const [centerFileRequestsBySession, setCenterFileRequestsBySession] = useState<
		Record<string, Record<string, CenterFileOpenRequest>>
	>({});
	const consumedCenterEditingRequestsRef = useRef(new Set<string>());
	const consumedCenterLineRequestsRef = useRef(new Set<string>());
	const [auxiliaryTabOrderBySession, setAuxiliaryTabOrderBySession] = useState<Record<string, string[]>>({});
	const auxiliaryTabOrder = auxiliaryTabOrderBySession[uiSessionId] ?? EMPTY_AUXILIARY_TAB_ORDER;
	const setAuxiliaryTabOrder = useCallback(
		(nextOrder: string[]) => {
			setAuxiliaryTabOrderBySession((current) => {
				const currentOrder = current[uiSessionId] ?? [];
				const visibleKeys = new Set(nextOrder);
				let nextIndex = 0;
				const mergedOrder = currentOrder.map((key) =>
					visibleKeys.has(key) ? nextOrder[nextIndex++]! : key,
				);
				while (nextIndex < nextOrder.length) mergedOrder.push(nextOrder[nextIndex++]!);
				return { ...current, [uiSessionId]: mergedOrder };
			});
		},
		[uiSessionId],
	);
	const removeAuxiliaryTab = useCallback(
		(key: string) => {
			setAuxiliaryTabOrderBySession((current) => {
				const currentOrder = current[uiSessionId];
				if (!currentOrder?.includes(key)) return current;
				const nextOrder = currentOrder.filter((candidate) => candidate !== key);
				if (nextOrder.length === 0) {
					const { [uiSessionId]: _removed, ...rest } = current;
					return rest;
				}
				return { ...current, [uiSessionId]: nextOrder };
			});
		},
		[uiSessionId],
	);
	const browserPopOutPhase = browserPopOutState.sessionId === sessionId ? browserPopOutState.phase : "docked";
	const browserPopOutMounted = browserPopOutPhase !== "docked";
	const browserPoppedOut = browserPopOutPhase === "open";
	const [browserPopoutTopbarHost, setBrowserPopoutTopbarHost] = useState<HTMLDivElement | null>(null);
	useLayoutEffect(() => {
		if (browserPopOutPhase !== "mounting" || !browserPopoutTopbarHost) return;
		// Establish the portal destination before moving the browser. This layout
		// effect completes before paint, so the user sees one atomic geometry change
		// and the address/tabs never spend a frame underneath the native view.
		setBrowserPopOutState({ sessionId, phase: "open" });
	}, [browserPopOutPhase, browserPopoutTopbarHost, sessionId]);
	// One SessionView serves every local session, so session-scoped UI state must
	// reset on a switch. Reset during render, not in an effect: children must never
	// mount (or fetch) against the previous session's reviewer, popout, or dialog.
	const [stateSessionId, setStateSessionId] = useState(sessionId);
	if (stateSessionId !== sessionId) {
		setStateSessionId(sessionId);
		setTerminalTarget({ kind: "worker" });
		setReviewerChatId(null);
		setBrowserPopOutState({ sessionId, phase: "docked" });
		setFilesPoppedOut(false);
	}
	const stopTerminalLiveResize = useCallback(() => {
		if (terminalLiveResizeTimerRef.current !== null) {
			window.clearTimeout(terminalLiveResizeTimerRef.current);
			terminalLiveResizeTimerRef.current = null;
		}
		sessionSplitRef.current?.removeAttribute("data-terminal-live-resize");
		sessionSplitRef.current?.removeAttribute("data-inspector-label-mode");
		sessionSplitRef.current?.removeAttribute("data-topbar-secondary-label-mode");
	}, []);
	const startTerminalLiveResize = useCallback(
		(labelMode: "compact" | "expanded", topbarLabelMode: "compact" | "expanded") => {
			const split = sessionSplitRef.current;
			if (!split) return;
			if (terminalLiveResizeTimerRef.current !== null) {
				window.clearTimeout(terminalLiveResizeTimerRef.current);
			}
			split.setAttribute("data-terminal-live-resize", "true");
			split.setAttribute("data-inspector-label-mode", labelMode);
			split.setAttribute("data-topbar-secondary-label-mode", topbarLabelMode);
			terminalLiveResizeTimerRef.current = window.setTimeout(() => {
				split.removeAttribute("data-terminal-live-resize");
				split.removeAttribute("data-inspector-label-mode");
				split.removeAttribute("data-topbar-secondary-label-mode");
				terminalLiveResizeTimerRef.current = null;
			}, INSPECTOR_SPRING_MS);
		},
		[],
	);

	useEffect(() => stopTerminalLiveResize, [stopTerminalLiveResize]);

	useEffect(() => {
		if (session?.kind !== "orchestrator" || session.mode === "chat") return;
		if (session.provisionState === "failed") {
			useUiStore.getState().setProjectProvisioning(session.workspaceId, false, hostId);
		}
	}, [hostId, session?.kind, session?.mode, session?.provisionState, session?.workspaceId]);
	const reviewerQuery = useQuery({
		queryKey: sessionReviewsQueryKey(sessionId, hostId),
		enabled: Boolean(
			(hostId ? remoteBase : window.tallpine) && session && sessionIsActive(session) && !isOrchestratorSession(session) && session.prs.length > 0,
		),
		refetchInterval: (query) => {
			const data = query.state.data as ReviewsResponse | undefined;
			return data?.reviews?.some((review) => review.status === "running") ? 2500 : false;
		},
		queryFn: async () => {
			const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/reviews", {
				params: { path: { sessionId } },
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to load reviews"));
			return data ?? ({ reviewerHandleId: "", reviews: [], runs: [], activeReviewers: [], queuedReviews: [] } satisfies ReviewsResponse);
		},
	});
	const reviewerSwitchPending = useIsMutating({
		mutationKey: [...sessionReviewsQueryKey(sessionId, hostId), "switch-reviewer"],
	}) > 0;
	const availableReviewerTerminal = reviewerTerminalFromReviews(reviewerQuery.data, terminalTarget);
	return {
		accountSwitchingLabel,
		artifactRequestsBySession,
		autoResume,
		auxiliaryTabOrder,
		availableReviewerTerminal,
		browserOnly,
		browserPopOutMounted,
		browserPopOutPhase,
		browserPopoutTopbarHost,
		browserPoppedOut,
		centerFileRequestsBySession,
		chatDraftBoundaries,
		clearWorkspaceFileOpenRequest,
		consumedCenterEditingRequestsRef,
		consumedCenterLineRequestsRef,
		daemonStatus,
		dirtyFiles,
		fileTabs,
		filesKind,
		filesPoppedOut,
		filesSplit,
		hostLabel,
		initializeInspectorSession,
		inspectorPanelVisible,
		inspectorSettledClosed,
		inspectorView,
		interfaceUi,
		isInspectorOpen,
		navigate,
		previewBaselineRef,
		queryClient,
		quietResume,
		refreshWorkspaces,
		remoteBase,
		remoteSessionQuery,
		removeAuxiliaryTab,
		resetFilesSelection,
		revealedBrowserCommandsRef,
		reviewerChatId,
		reviewerQuery,
		reviewerSwitchPending,
		session,
		sessionGone,
		sessionSplitRef,
		setArtifactRequestsBySession,
		setAuxiliaryTabOrder,
		setAuxiliaryTabOrderBySession,
		setBrowserContentRevealed,
		setBrowserPopOutState,
		setBrowserPopoutTopbarHost,
		setBrowserUnseen,
		setCenterFileRequestsBySession,
		setDirtyFilesBySession,
		setFileTabsBySession,
		setFilesChangedOnly,
		setFilesPoppedOut,
		setFilesSplit,
		setInspectorOpenForSession,
		setInspectorSettledClosed,
		setInspectorViewForSession,
		setReviewerChatId,
		setSideChatContainer,
		setTerminalTarget,
		showGlobalToast,
		sideChatContainer,
		sideChatsAvailable,
		startTerminalLiveResize,
		stopTerminalLiveResize,
		t,
		terminalTarget,
		theme,
		toggleInspector,
		uiSessionId,
		workspaceFileOpenRequest,
		workspaceQuery,
		workspaceResizeTimerRef,
		workspaceSessionQuery,
	};
}
