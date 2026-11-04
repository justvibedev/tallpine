import { useCallback, useEffect, useMemo, useRef } from "react";
import type { FileOpenOptions } from "../FileContentPane";
import { adoptedShellHandle, useCloseShellTerminal, useOpenShellTerminal, useRenameShellTerminal, useShellTerminals } from "../../hooks/useShellTerminals";
import { subscribeChatReveal } from "../../lib/chat-context-bus";
import { apiErrorCode } from "../../lib/api-client";
import { activateSessionFile, closeSessionFile, EMPTY_SESSION_FILE_TABS, openSessionFile, sessionFileTabKey } from "../../lib/session-file-tabs";
import { sessionIsActive } from "../../types/workspace";
import { useUiStore } from "../../stores/ui-store";
import { useSessionViewData } from "./useSessionViewData";
import { SessionViewProps, ReviewerTerminalTarget } from "./session-view-types";
import { reviewerChatFromReviews, nextCenterFileRequestKey } from "./session-view-helpers";

/** Reviewer targets and shell terminal tabs for SessionView. */
export function useSessionViewTerminals(props: SessionViewProps, data: ReturnType<typeof useSessionViewData>) {
	const {
		sessionId,
		hostId,
	} = props;
	const {
		auxiliaryTabOrder,
		availableReviewerTerminal,
		consumedCenterEditingRequestsRef,
		consumedCenterLineRequestsRef,
		dirtyFiles,
		fileTabs,
		removeAuxiliaryTab,
		reviewerQuery,
		session,
		setAuxiliaryTabOrderBySession,
		setCenterFileRequestsBySession,
		setDirtyFilesBySession,
		setFileTabsBySession,
		setReviewerChatId,
		setTerminalTarget,
		terminalTarget,
		uiSessionId,
	} = data;
	const reviewerTerminal = session && sessionIsActive(session) ? availableReviewerTerminal : undefined;
	const availableReviewerChat = reviewerChatFromReviews(reviewerQuery.data);
	const reviewerChat = session && sessionIsActive(session) ? availableReviewerChat : undefined;

	// Shell terminals opened inside a session live beside its pane as extra tabs,
	// scoped to the session on screen so each session has its own shell set.
	const shellTerminalsQuery = useShellTerminals(hostId);
	const allShellTerminals = shellTerminalsQuery.data ?? [];
	const shellTerminals = useMemo(
		() => allShellTerminals.filter((shell) => shell.sessionId === sessionId),
		[allShellTerminals, sessionId],
	);
	const resolvedAuxiliaryTabOrder = useMemo(() => {
		const openFileKeys = fileTabs.openPaths.map((path) => `file:${path}`);
		const openShellKeys = shellTerminals.map((shell) => shell.handleId);
		const available = [
			...(reviewerTerminal ? [`reviewer:${reviewerTerminal.handleId}`] : []),
			...(!reviewerTerminal && reviewerChat ? [`reviewer-chat:${reviewerChat.reviewId}`] : []),
			...openFileKeys,
			...openShellKeys,
		];
		const availableKeys = new Set(available);
		// A pending shell tab keeps its place once it becomes its shell.
		const resolved = [
			...new Set(auxiliaryTabOrder.map((key) => adoptedShellHandle(key) ?? key)),
		].filter((key) => availableKeys.has(key));
		for (const key of available) {
			if (!resolved.includes(key)) resolved.push(key);
		}
		return resolved;
	}, [auxiliaryTabOrder, fileTabs.openPaths, reviewerChat, reviewerTerminal, shellTerminals]);
	useEffect(() => {
		setAuxiliaryTabOrderBySession((current) => {
			const storedOrder = current[uiSessionId] ?? [];
			const currentOrder = [...new Set(storedOrder.map((key) => adoptedShellHandle(key) ?? key))];
			const newKeys = resolvedAuxiliaryTabOrder.filter((key) => !currentOrder.includes(key));
			if (newKeys.length === 0 && currentOrder.length === storedOrder.length
				&& currentOrder.every((key, index) => key === storedOrder[index])) {
				return current;
			}
			return { ...current, [uiSessionId]: [...currentOrder, ...newKeys] };
		});
	}, [resolvedAuxiliaryTabOrder, uiSessionId]);
	const openShellTerminal = useOpenShellTerminal(hostId);
	const closeShellTerminal = useCloseShellTerminal(hostId);
	const renameShellTerminal = useRenameShellTerminal(hostId);
	const activeShellTerminalHandleId = useUiStore((state) => state.activeShellTerminalHandleId);
	const setActiveShellTerminal = useUiStore((state) => state.setActiveShellTerminal);
	const setVisibleTerminalKind = useUiStore((state) => state.setVisibleTerminalKind);
	const clearVisibleTerminalKind = useUiStore((state) => state.clearVisibleTerminalKind);
	const renameShellTerminalByHandle = useCallback(
		(handleId: string, title: string) => renameShellTerminal.mutate({ handleId, title }),
		[renameShellTerminal],
	);

	// Scoped to the session on screen so the daemon roots the shell in that
	// session's worktree (the project id is only the fallback when the session's
	// workspace can no longer be resolved).
	const addShellTerminal = useCallback(() => {
		const shell = openShellTerminal.open({ projectId: session?.workspaceId, sessionId });
		if (!shell) return;
		setFileTabsBySession((current) => ({
			...current,
			[uiSessionId]: activateSessionFile(current[uiSessionId] ?? EMPTY_SESSION_FILE_TABS, null),
		}));
		setActiveShellTerminal(shell.handleId);
		setTerminalTarget({
			generation: shell.createdAt,
			kind: "shell",
			handleId: shell.handleId,
			sessionId,
			title: shell.title,
		});
	}, [
		openShellTerminal,
		sessionId,
		session?.workspaceId,
		setActiveShellTerminal,
		uiSessionId,
	]);

	const activateAuxiliaryTab = useCallback(
		(key?: string) => {
			if (key?.startsWith("file:")) {
				const path = key.slice("file:".length);
				setActiveShellTerminal(null);
				setTerminalTarget({ kind: "worker" });
				setFileTabsBySession((current) => ({
					...current,
					[uiSessionId]: activateSessionFile(current[uiSessionId] ?? EMPTY_SESSION_FILE_TABS, path),
				}));
				return;
			}
			if (reviewerTerminal && key === `reviewer:${reviewerTerminal.handleId}`) {
				setActiveShellTerminal(null);
				setTerminalTarget({
					kind: "reviewer",
					handleId: reviewerTerminal.handleId,
					harness: reviewerTerminal.harness,
					sessionId,
				});
				setFileTabsBySession((current) => ({
					...current,
					[uiSessionId]: activateSessionFile(current[uiSessionId] ?? EMPTY_SESSION_FILE_TABS, null),
				}));
				return;
			}
			if (reviewerChat && key === `reviewer-chat:${reviewerChat.reviewId}`) {
				setActiveShellTerminal(null);
				setTerminalTarget({ kind: "worker" });
				setReviewerChatId(reviewerChat.reviewId);
				setFileTabsBySession((current) => ({
					...current,
					[uiSessionId]: activateSessionFile(current[uiSessionId] ?? EMPTY_SESSION_FILE_TABS, null),
				}));
				return;
			}
			const shell = shellTerminals.find((candidate) => candidate.handleId === key);
			if (shell) {
				setActiveShellTerminal(shell.handleId);
				setTerminalTarget({
					generation: shell.createdAt,
					kind: "shell",
					handleId: shell.handleId,
					sessionId,
					title: shell.title,
				});
				setFileTabsBySession((current) => ({
					...current,
					[uiSessionId]: activateSessionFile(current[uiSessionId] ?? EMPTY_SESSION_FILE_TABS, null),
				}));
				return;
			}
			setActiveShellTerminal(null);
			setTerminalTarget({ kind: "worker" });
			setFileTabsBySession((current) => ({
				...current,
				[uiSessionId]: activateSessionFile(current[uiSessionId] ?? EMPTY_SESSION_FILE_TABS, null),
			}));
		},
		[reviewerChat, reviewerTerminal, sessionId, shellTerminals, setActiveShellTerminal, uiSessionId],
	);
	const adjacentAuxiliaryTab = useCallback(
		(closingKey: string) => {
			const closingIndex = resolvedAuxiliaryTabOrder.indexOf(closingKey);
			if (closingIndex < 0) return undefined;
			return resolvedAuxiliaryTabOrder[closingIndex - 1] ?? resolvedAuxiliaryTabOrder[closingIndex + 1];
		},
		[resolvedAuxiliaryTabOrder],
	);

	const selectShellTerminal = useCallback(
		(handleId: string) => {
			const shell = shellTerminals.find((s) => s.handleId === handleId);
			if (!shell) return;
			setReviewerChatId(null);
			setActiveShellTerminal(shell.handleId);
			setFileTabsBySession((current) => ({
				...current,
				[uiSessionId]: activateSessionFile(current[uiSessionId] ?? EMPTY_SESSION_FILE_TABS, null),
			}));
			setTerminalTarget({
				generation: shell.createdAt,
				kind: "shell",
				handleId: shell.handleId,
				sessionId,
				title: shell.title,
			});
		},
		[sessionId, shellTerminals, setActiveShellTerminal, uiSessionId],
	);

	const closeShellTerminalByHandle = useCallback(
		(handleId: string) => {
			if (terminalTarget.kind === "shell" && terminalTarget.handleId === handleId) {
				// Match the visible mixed strip, not the shell-only creation order.
				activateAuxiliaryTab(adjacentAuxiliaryTab(handleId));
			} else if (activeShellTerminalHandleId === handleId) {
				setActiveShellTerminal(null);
			}
			closeShellTerminal.mutate(handleId, {
				onSuccess: () => removeAuxiliaryTab(handleId),
				onError: (error) => {
					if (apiErrorCode(error) === "SHELL_TERMINAL_NOT_FOUND") removeAuxiliaryTab(handleId);
				},
			});
		},
		[
			activeShellTerminalHandleId,
			activateAuxiliaryTab,
			adjacentAuxiliaryTab,
			closeShellTerminal,
			removeAuxiliaryTab,
			setActiveShellTerminal,
			terminalTarget,
		],
	);

	// Selecting the session's own pane also drops the active shell, so the effect
	// above does not immediately pull the view back to that shell.
	const selectSessionTerminal = useCallback(() => {
		setActiveShellTerminal(null);
		setTerminalTarget({ kind: "worker" });
		setReviewerChatId(null);
		setFileTabsBySession((current) => ({
			...current,
			[uiSessionId]: activateSessionFile(current[uiSessionId] ?? EMPTY_SESSION_FILE_TABS, null),
		}));
	}, [setActiveShellTerminal, uiSessionId]);
	// "Ask in chat" from a file tab brings the session's Chat surface forward.
	useEffect(() => subscribeChatReveal(uiSessionId, selectSessionTerminal), [selectSessionTerminal, uiSessionId]);
	const selectReviewerTerminal = useCallback((target: ReviewerTerminalTarget) => {
		setReviewerChatId(null);
		setActiveShellTerminal(null);
		setTerminalTarget({ kind: "reviewer", handleId: target.handleId, harness: target.harness, sessionId });
		setFileTabsBySession((current) => ({
			...current,
			[uiSessionId]: activateSessionFile(current[uiSessionId] ?? EMPTY_SESSION_FILE_TABS, null),
		}));
	}, [sessionId, setActiveShellTerminal, uiSessionId]);
	const selectReviewerChat = useCallback((reviewId: string) => {
		setActiveShellTerminal(null);
		setTerminalTarget({ kind: "worker" });
		setReviewerChatId(reviewId);
		setFileTabsBySession((current) => ({
			...current,
			[uiSessionId]: activateSessionFile(current[uiSessionId] ?? EMPTY_SESSION_FILE_TABS, null),
		}));
	}, [setActiveShellTerminal, uiSessionId]);
	const openCenterFile = useCallback(
		(path: string, options?: FileOpenOptions) => {
			const filePath = path;
			// Live paths retain their existing identities; snapshots coexist with them.
			path = sessionFileTabKey(path, options?.source);
			setReviewerChatId(null);
			setActiveShellTerminal(null);
			setTerminalTarget({ kind: "worker" });
			const key = nextCenterFileRequestKey();
			setCenterFileRequestsBySession((current) => {
				const sessionRequests = current[uiSessionId] ?? {};
				if (dirtyFiles[path] && sessionRequests[path]) return current;
				return {
					...current,
					[uiSessionId]: {
						...sessionRequests,
						[path]: {
							path: filePath,
							source: options?.source,
							previousPath: options?.previousPath,
							commitSha: options?.commitSha,
							editing: options?.editing ?? false,
							key,
							line: options?.line,
							mode: options?.mode ?? "file",
							scope: options?.scope,
						},
					},
				};
			});
			setFileTabsBySession((current) => ({
				...current,
				[uiSessionId]: openSessionFile(current[uiSessionId] ?? EMPTY_SESSION_FILE_TABS, path),
			}));
		},
		[setActiveShellTerminal, uiSessionId, dirtyFiles],
	);

	const markCenterFileEditingConsumed = useCallback((path: string, requestKey: number) => {
		consumedCenterEditingRequestsRef.current.add(`${uiSessionId}:${path}:${requestKey}`);
	}, [uiSessionId]);
	const markCenterFileLineConsumed = useCallback((path: string, requestKey: number) => {
		consumedCenterLineRequestsRef.current.add(`${uiSessionId}:${path}:${requestKey}`);
	}, [uiSessionId]);
	const setCenterFileDirty = useCallback((path: string, dirty: boolean) => {
		setDirtyFilesBySession((current) => {
			const sessionFiles = current[uiSessionId] ?? {};
			if (dirty) {
				if (sessionFiles[path]) return current;
				return { ...current, [uiSessionId]: { ...sessionFiles, [path]: true } };
			}
			if (!sessionFiles[path]) return current;
			const nextSessionFiles = { ...sessionFiles };
			delete nextSessionFiles[path];
			return { ...current, [uiSessionId]: nextSessionFiles };
		});
	}, [uiSessionId]);
	const activateCenterFile = useCallback((path: string) => {
		setReviewerChatId(null);
		setActiveShellTerminal(null);
		setTerminalTarget({ kind: "worker" });
		setFileTabsBySession((current) => ({
			...current,
			[uiSessionId]: activateSessionFile(current[uiSessionId] ?? EMPTY_SESSION_FILE_TABS, path),
		}));
	}, [setActiveShellTerminal, uiSessionId]);
	// Closed file tabs, newest last, so ⌘⇧T can bring them back.
	const closedFilePathsRef = useRef<Record<string, string[]>>({});
	const closeCenterFile = useCallback((path: string) => {
		const closed = (closedFilePathsRef.current[uiSessionId] ??= []);
		closed.push(path);
		if (closed.length > 20) closed.shift();
		setFileTabsBySession((current) => ({
			...current,
			[uiSessionId]: closeSessionFile(current[uiSessionId] ?? EMPTY_SESSION_FILE_TABS, path),
		}));
		if (fileTabs.activePath === path) {
			activateAuxiliaryTab(adjacentAuxiliaryTab(`file:${path}`));
		}
		removeAuxiliaryTab(`file:${path}`);
	}, [activateAuxiliaryTab, adjacentAuxiliaryTab, fileTabs.activePath, removeAuxiliaryTab, uiSessionId]);
	// The shell layout owns opening (it is mounted on every route, so the button
	// and ⌘T / Ctrl+T work everywhere); this view only follows the result. When a new
	// shell becomes active while a session is on screen, switch the pane to it —
	// that is what makes the shortcut feel like it opened a terminal *here*.
	return {
		activateCenterFile,
		activeShellTerminalHandleId,
		addShellTerminal,
		availableReviewerChat,
		clearVisibleTerminalKind,
		closeCenterFile,
		closeShellTerminalByHandle,
		closedFilePathsRef,
		markCenterFileEditingConsumed,
		markCenterFileLineConsumed,
		openCenterFile,
		openShellTerminal,
		renameShellTerminalByHandle,
		resolvedAuxiliaryTabOrder,
		reviewerChat,
		reviewerTerminal,
		selectReviewerChat,
		selectReviewerTerminal,
		selectSessionTerminal,
		selectShellTerminal,
		setActiveShellTerminal,
		setCenterFileDirty,
		setVisibleTerminalKind,
		shellTerminals,
		shellTerminalsQuery,
	};
}
