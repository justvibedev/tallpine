import { useCallback, useEffect, useState } from "react";
import type { FileOpenOptions } from "../FileContentPane";
import { useOpenArtifactPreview } from "../../hooks/useOpenArtifactPreview";
import { fetchSessionView, forgetSessionArtifactFiles } from "../../hooks/useWorkspaceQuery";
import { sessionUiKey } from "../../lib/hosts";
import { resolveSessionWorkspacePath } from "../../hooks/useSessionWorkspaceFiles";
import { sessionWorkspaceFilesQueryOptions } from "../../hooks/workspace-files/workspace-list-queries";
import { isAbsoluteFileReference, matchWorkspaceFilePath, normalizeWorkspaceFileReference } from "../../lib/workspace-file-path";
import { markFileViewerPerformance } from "../../lib/file-viewer-performance";
import { useUiStore } from "../../stores/ui-store";
import { useSessionViewData } from "./useSessionViewData";
import { useSessionViewTerminals } from "./useSessionViewTerminals";
import { useSessionViewWorkspace } from "./useSessionViewWorkspace";
import { SessionViewProps } from "./session-view-types";
import { browserIsVisible, previewRevealKey, BROWSER_REVEAL_ACTIONS } from "./session-view-helpers";

/** Artifact, file open and pop-out actions for SessionView. */
export function useSessionViewActions(props: SessionViewProps, data: ReturnType<typeof useSessionViewData>, terms: ReturnType<typeof useSessionViewTerminals>, workspace: ReturnType<typeof useSessionViewWorkspace>) {
	const {
		sessionId,
		hostId,
	} = props;
	const {
		browserOnly,
		browserPoppedOut,
		clearWorkspaceFileOpenRequest,
		inspectorView,
		isInspectorOpen,
		previewBaselineRef,
		queryClient,
		refreshWorkspaces,
		resetFilesSelection,
		revealedBrowserCommandsRef,
		reviewerChatId,
		session,
		setArtifactRequestsBySession,
		setBrowserContentRevealed,
		setBrowserPopOutState,
		setBrowserUnseen,
		setFilesChangedOnly,
		setFilesPoppedOut,
		setInspectorOpenForSession,
		setInspectorViewForSession,
		t,
		uiSessionId,
		workspaceFileOpenRequest,
	} = data;
	const {
		clearVisibleTerminalKind,
		openCenterFile,
		setVisibleTerminalKind,
	} = terms;
	const {
		browserView,
		hasBrowserContent,
		hasInspector,
		previewRevision,
		previewUrl,
		routedTerminalTarget,
		terminated,
		transitionInspectorView,
	} = workspace;
	useEffect(() => {
		setVisibleTerminalKind(uiSessionId, reviewerChatId ? "reviewer" : routedTerminalTarget.kind);
		return () => clearVisibleTerminalKind(uiSessionId);
	}, [clearVisibleTerminalKind, reviewerChatId, routedTerminalTarget.kind, uiSessionId, setVisibleTerminalKind]);

	const prepareFilesInspector = useCallback(() => {
		if (browserOnly) return;
		setBrowserPopOutState({ sessionId, phase: "docked" });
		setFilesPoppedOut(false);
		setFilesChangedOnly(uiSessionId, true);
		transitionInspectorView("files");
		setInspectorOpenForSession(uiSessionId, true);
	}, [browserOnly, sessionId, uiSessionId, setFilesChangedOnly, setInspectorOpenForSession, transitionInspectorView]);

	const fetchWorkspaceFiles = useCallback(async () => {
		return queryClient.fetchQuery(
			sessionWorkspaceFilesQueryOptions(sessionId, t("files.error.loadWorkspace"), hostId),
		);
	}, [hostId, queryClient, sessionId, t]);

	const handleOpenArtifact = useCallback(
		(target: { feedback?: boolean; path: string }) => {
			if (browserOnly) return;
			setBrowserPopOutState({ sessionId, phase: "docked" });
			setFilesPoppedOut(false);
			transitionInspectorView("artifacts");
			setInspectorOpenForSession(uiSessionId, true);
			setArtifactRequestsBySession((current) => ({
				...current,
				[uiSessionId]: { feedback: target.feedback, path: target.path, key: (current[uiSessionId]?.key ?? 0) + 1 },
			}));
		},
		[browserOnly, sessionId, setInspectorOpenForSession, transitionInspectorView, uiSessionId],
	);

	// A file written moments ago, or one past the artifact list cap, is not in
	// the cached session, and the Files inspector can only show listed
	// artifacts. Refresh once before deciding it cannot be opened.
	const revealResolvedArtifact = useCallback(
		async (path: string) => {
			if (browserOnly) return false;
			const listed = (artifacts: Array<{ path: string }> | undefined) => artifacts?.some((artifact) => artifact.path === path) ?? false;
			if (!listed(session?.artifactFiles)) {
				// The session list carries no file listing, so read the directory itself.
				forgetSessionArtifactFiles(queryClient, sessionId, hostId);
				const fresh = await fetchSessionView(hostId, sessionId).catch(() => undefined);
				if (!listed(fresh?.artifactFiles)) return false;
				void refreshWorkspaces();
			}
			handleOpenArtifact({ path });
			return true;
		},
		[browserOnly, handleOpenArtifact, hostId, queryClient, refreshWorkspaces, session?.artifactFiles, sessionId],
	);

	const revealResolvedWorkspaceFile = useCallback(
		async (rawPath: string, options?: FileOpenOptions) => {
			const reference = normalizeWorkspaceFileReference(rawPath);
			if (!isAbsoluteFileReference(reference)) {
				const data = await fetchWorkspaceFiles();
				openCenterFile(matchWorkspaceFilePath(rawPath, data.files ?? []), options);
				return;
			}
			// The renderer is never told where a session's worktree lives, so the
			// daemon decides which root an absolute path belongs to.
			const resolved = await resolveSessionWorkspacePath(sessionId, reference, hostId);
			if (resolved?.kind === "workspace") {
				openCenterFile(resolved.path, options);
				return;
			}
			if (resolved?.kind === "artifact" && await revealResolvedArtifact(resolved.path)) return;
			// Outside the session, or the daemon could not say. Nothing shows the
			// path belongs here, so open it as written and let the file pane say
			// so, rather than a workspace file that happens to share its name.
			openCenterFile(reference, options);
		},
		[fetchWorkspaceFiles, hostId, openCenterFile, revealResolvedArtifact, sessionId],
	);

	const handleOpenFiles = useCallback(() => {
		markFileViewerPerformance("files-click");
		// Opens on the checked-out branch's default review: its uncommitted
		// changes, or its commits when the worktree is clean.
		resetFilesSelection(uiSessionId);
		prepareFilesInspector();
		void fetchWorkspaceFiles();
	}, [fetchWorkspaceFiles, prepareFilesInspector, resetFilesSelection, uiSessionId]);

	const handleOpenReviewFile = useCallback(
		(target: { line?: number; path: string }) => {
			void revealResolvedWorkspaceFile(target.path, { line: target.line, mode: "diff" });
		},
		[revealResolvedWorkspaceFile],
	);

	const handleOpenFile = useCallback(
		(path: string, line?: number) => {
			void revealResolvedWorkspaceFile(path, { line, mode: "file" });
		},
		[revealResolvedWorkspaceFile],
	);

	const handleArtifactRequestConsumed = useCallback((key: number) => {
		setArtifactRequestsBySession((current) => {
			const request = current[uiSessionId];
			if (!request || request.key !== key || !request.feedback) return current;
			return {
				...current,
				[uiSessionId]: { ...request, feedback: undefined },
			};
		});
	}, [uiSessionId]);
	const openArtifactPreview = useOpenArtifactPreview(session?.id, session?.hostId);
	useEffect(() => {
		if (!workspaceFileOpenRequest || !session) return;
		if (sessionUiKey(workspaceFileOpenRequest.sessionId, workspaceFileOpenRequest.hostId) !== uiSessionId) return;
		const { nonce, path, source } = workspaceFileOpenRequest;
		if (source === "artifact") {
			// Same routing as the Summary panel's artifact rows: HTML opens in
			// the Browser panel, everything else in the Artifacts view.
			const artifact = session.artifactFiles?.find((file) => file.path === path);
			if (artifact?.kind === "html" && artifact.previewUrl) openArtifactPreview(artifact.previewUrl);
			else handleOpenArtifact({ path });
		} else {
			handleOpenFile(path);
		}
		clearWorkspaceFileOpenRequest(nonce);
	}, [
		clearWorkspaceFileOpenRequest,
		handleOpenArtifact,
		handleOpenFile,
		openArtifactPreview,
		openCenterFile,
		prepareFilesInspector,
		session,
		uiSessionId,
		workspaceFileOpenRequest,
	]);

	const handleToggleFilesPopOut = useCallback(
		(next: boolean) => {
			if (next) setBrowserPopOutState({ sessionId, phase: "docked" });
			setFilesPoppedOut(next);
			transitionInspectorView("files");
			setInspectorOpenForSession(uiSessionId, true);
		},
		[sessionId, uiSessionId, setInspectorOpenForSession, transitionInspectorView],
	);

	const handleToggleBrowserPopOut = useCallback(
		(next: boolean) => {
			if (next) setFilesPoppedOut(false);
			setBrowserPopOutState((current) => {
				if (next) {
					if (current.sessionId === sessionId && current.phase !== "docked") return current;
					return { sessionId, phase: "mounting" };
				}
				if (current.sessionId !== sessionId || current.phase === "docked") return current;
				return { sessionId, phase: "docked" };
			});
		},
		[sessionId],
	);

	useEffect(() => {
		if (!hasInspector) return;
		const current = useUiStore.getState().inspectorSessions[uiSessionId];
		if (browserOnly) {
			if (terminated && current?.browserUnseen) setBrowserUnseen(uiSessionId, false);
			return;
		}
		if (!hasBrowserContent) {
			if (current?.browserContentRevealed) setBrowserContentRevealed(uiSessionId, false);
			else if (current?.browserUnseen) setBrowserUnseen(uiSessionId, false);
			return;
		}
		if (current?.browserContentRevealed) return;
		setBrowserContentRevealed(uiSessionId, true);
	}, [
		hasBrowserContent,
		browserOnly,
		hasInspector,
		previewRevision,
		uiSessionId,
		setBrowserContentRevealed,
		setBrowserUnseen,
		terminated,
	]);

	const revealBrowserPanel = useCallback(() => {
		setBrowserContentRevealed(uiSessionId, true);
		setBrowserUnseen(uiSessionId, false);
		// Pin a derived Browser so a plan arriving later doesn't flip the panel away.
		if (inspectorView === "browser" && useUiStore.getState().inspectorSessions[uiSessionId]?.view !== "browser") setInspectorViewForSession(uiSessionId, "browser");
		if (browserIsVisible(uiSessionId, browserPoppedOut, inspectorView)) return;
		setFilesPoppedOut(false);
		transitionInspectorView("browser");
		setInspectorOpenForSession(uiSessionId, true);
	}, [browserPoppedOut, inspectorView, uiSessionId, setBrowserContentRevealed, setBrowserUnseen, setInspectorViewForSession, transitionInspectorView, setInspectorOpenForSession]);

	useEffect(() => {
		if (!hasInspector) return;
		const previewKey = previewRevealKey(previewUrl, previewRevision);
		const baseline = previewBaselineRef.current;
		if (!baseline || baseline.sessionId !== uiSessionId) {
			previewBaselineRef.current = { sessionId: uiSessionId, key: previewKey };
			return;
		}
		if (baseline.key === previewKey) return;
		previewBaselineRef.current = { sessionId: uiSessionId, key: previewKey };
		if (!previewKey || terminated) return;
		// Only the routed session mounts SessionView. A new explicit preview
		// reveals its panel; entering a session with an existing target does not.
		revealBrowserPanel();
	}, [
		hasInspector,
		previewRevision,
		previewUrl,
		uiSessionId,
		terminated,
		revealBrowserPanel,
	]);

	// Explicit open commands reveal once per command. A fast command's start
	// and finish can share a React render, so either event can consume its ID.
	// Later completion and UI changes must not override a user's close.
	useEffect(() => {
		if (revealedBrowserCommandsRef.current.sessionId !== uiSessionId) {
			revealedBrowserCommandsRef.current = { sessionId: uiSessionId, commands: new Set() };
		}
		if (!hasInspector || terminated) return;
		const activity = browserView.agentBrowserActivity;
		if (activity && BROWSER_REVEAL_ACTIONS.has(activity.action)) {
			if (!activity.commandId || activity.viewId !== browserView.viewId) return;
			const commands = revealedBrowserCommandsRef.current.commands;
			if (commands.has(activity.commandId)) return;
			commands.add(activity.commandId);
			// Bound deduplication memory while retaining recent overlapping commands.
			if (commands.size > 64) commands.delete(commands.values().next().value!);
			revealBrowserPanel();
			return;
		}
		if (!browserView.agentBrowserActive) return;
		// Passive agent activity (anything but an explicit open command) never opens
		// the panel: an agent already browsing when the user opens the chat would
		// pop it open on whatever URL it is on. It only raises the unseen dot.
		if (!browserIsVisible(uiSessionId, browserPoppedOut, inspectorView)) setBrowserUnseen(uiSessionId, true);
	}, [
		browserPoppedOut,
		browserView.agentBrowserActive,
		browserView.agentBrowserActivity,
		browserView.viewId,
		hasInspector,
		inspectorView,
		isInspectorOpen,
		uiSessionId,
		setBrowserUnseen,
		terminated,
		browserOnly,
		setBrowserContentRevealed,
		setInspectorOpenForSession,
		revealBrowserPanel,
	]);

	// Opening Browser consumes the pending activity indicator, including the
	// case where the inspector was collapsed while already parked on Browser.
	useEffect(() => {
		if (hasInspector && (browserPoppedOut || (isInspectorOpen && inspectorView === "browser"))) {
			setBrowserUnseen(uiSessionId, false);
		}
	}, [browserPoppedOut, hasInspector, inspectorView, isInspectorOpen, uiSessionId, setBrowserUnseen]);

	const [switcherHost, setSwitcherHost] = useState<HTMLDivElement | null>(null);
	return {
		handleArtifactRequestConsumed,
		handleOpenArtifact,
		handleOpenFile,
		handleOpenFiles,
		handleOpenReviewFile,
		handleToggleBrowserPopOut,
		handleToggleFilesPopOut,
		prepareFilesInspector,
		setSwitcherHost,
		switcherHost,
	};
}
