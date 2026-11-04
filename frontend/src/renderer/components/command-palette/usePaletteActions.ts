import { useCallback, useEffect } from "react";
import { workspaceQueryKey } from "../../hooks/useWorkspaceQuery";
import { apiClient, apiErrorMessage } from "../../lib/api-client";
import { tallpineBridge } from "../../lib/bridge";
import { type CommandItem as CommandItemModel } from "../../lib/command-palette";
import { backlogInboxCountsKey, backlogProjectKey, createBacklogItem } from "../../lib/backlog-api";
import { isDialogOrMenuOpen } from "../../lib/dom-selectors";
import { useUiStore } from "../../stores/ui-store";
import { useAppShortcut } from "../../hooks/useAppShortcut";
import { usePaletteData } from "./usePaletteData";
import { usePaletteNavigation } from "./usePaletteNavigation";

/** Palette action execution and task-created handling. */
export function usePaletteActions(data: ReturnType<typeof usePaletteData>, nav: ReturnType<typeof usePaletteNavigation>) {
	const {
		currentFileTarget,
		enabled,
		isOpen,
		navigate,
		params,
		pendingRef,
		queryClient,
		runGenerationRef,
		setError,
		setOpen,
		setPendingId,
		setView,
		t,
		viewRef,
	} = data;
	const {
		blockedByRestart,
		closePalette,
		fileSessionRoute,
		navigateToTarget,
		openNewProject,
		openOrchestrator,
		pushView,
		requestDismiss,
		resetTransient,
		resumeSession,
		sessionRoute,
		toggleTheme,
	} = nav;
	const runAction = useCallback(
		async (item: CommandItemModel) => {
			const action = item.action;
			if (!action) return;
			setError(null);
			pendingRef.current = true;
			setPendingId(item.id);
			const generation = runGenerationRef.current;
			const isCurrentRun = () => runGenerationRef.current === generation;
			try {
				switch (action.kind) {
					case "navigate":
						navigateToTarget(action.target);
						closePalette();
						break;
					case "toggle-theme":
						toggleTheme();
						closePalette();
						break;
					case "open-backlog-panel":
						void navigate({ to: "/backlog" });
						closePalette();
						break;
					case "open-lane-orchestrator":
						useUiStore.getState().requestLaneOrchestrator(action.projectId);
						closePalette();
						break;
					case "open-backlog-capture":
						useUiStore.getState().requestBacklogCapture(action.title);
						closePalette();
						break;
					case "add-to-backlog": {
						// Without a project in view there is nothing to file under: hand the text to the capture popover.
						if (!action.projectId) {
							useUiStore.getState().requestBacklogCapture(action.title);
							closePalette();
							break;
						}
						const created = await createBacklogItem(action.projectId, { title: action.title });
						void queryClient.invalidateQueries({ queryKey: backlogProjectKey(action.projectId) });
						void queryClient.invalidateQueries({ queryKey: backlogInboxCountsKey });
						useUiStore.getState().showGlobalToast(t("backlog.capture.added", { ref: created.ref, title: created.title }), undefined, {
							action: { label: t("backlog.capture.open"), onClick: () => void navigate({ to: "/backlog", search: { project: created.projectId, item: created.id } }) },
						});
						closePalette();
						break;
					}
					case "copy-branch":
						await tallpineBridge.clipboard.writeText(action.branch);
						closePalette();
						break;
				case "open-pr":
					await tallpineBridge.app.openExternal(action.url);
					closePalette();
					break;
				case "copy-pr-url":
					await tallpineBridge.clipboard.writeText(action.url);
					closePalette();
					break;
				case "trigger-review": {
					const { error: triggerError } = await apiClient.POST("/api/v1/sessions/{sessionId}/reviews/trigger", {
						params: { path: { sessionId: action.sessionId } },
					});
					if (triggerError) throw new Error(apiErrorMessage(triggerError, "Unable to start review"));
					await queryClient.invalidateQueries({ queryKey: ["session-reviews", action.sessionId] });
					await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
					closePalette();
					break;
				}
				case "open-session-actions":
					pushView({ mode: "session-actions", sessionId: action.sessionId });
					break;
				case "open-file-search":
					if (action.target) {
						pushView({
							mode: "file-search",
							target: action.target,
							parent: viewRef.current.mode === "file-session-picker" ? "picker" : "root",
						});
					} else {
						pushView({ mode: "file-session-picker", projectId: action.projectId });
					}
					break;
				case "open-workspace-file": {
					useUiStore.getState().requestWorkspaceFileOpen(
						action.target.sessionId,
						action.path,
						action.target.hostId,
					);
					const alreadyOpen = params.sessionId === action.target.sessionId
						&& (params.hostId ?? "") === (action.target.hostId ?? "");
					if (!alreadyOpen) navigateToTarget(fileSessionRoute(action.target));
					closePalette();
					break;
				}
				case "resume-session": {
					const message = await resumeSession(action.sessionId);
					if (!isCurrentRun()) break;
					if (message) {
						setError(message);
						break;
					}
					navigateToTarget(sessionRoute(action.projectId, action.sessionId));
						closePalette();
						break;
					}
					case "open-new-task":
						if (blockedByRestart(action.projectId)) break;
						pushView({ mode: "new-task", projectId: action.projectId });
						break;
					case "open-new-project":
						openNewProject();
						break;
					case "open-orchestrator":
							await openOrchestrator(action.projectId);
							break;
				}
			} catch (err) {
				if (isCurrentRun()) setError(err instanceof Error ? err.message : t("command.failed"));
			} finally {
				pendingRef.current = false;
				setPendingId(null);
			}
		},
		[navigateToTarget, closePalette, toggleTheme, openOrchestrator, resumeSession, pushView, blockedByRestart, openNewProject, queryClient, t, params.hostId, params.sessionId, fileSessionRoute],
	);

	const onSelectItem = useCallback(
		(item: CommandItemModel) => {
			if (item.disabled || !item.action) return;
			if (pendingRef.current) return;
			void runAction(item);
		},
		[runAction],
	);

	const handleTaskCreated = useCallback(
		async (projectId: string, sessionId: string) => {
			closePalette();
			await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
			void navigateToTarget(sessionRoute(projectId, sessionId));
		},
		[queryClient, closePalette, navigateToTarget, sessionRoute],
	);

	// ⌘K / ⌘⇧P and ⌘P arrive from the main process (it also yields plain Ctrl+K and
	// Ctrl+P to a focused terminal on Windows/Linux), so they work from xterm and
	// the Browser view alike.
	useAppShortcut(
		"command-palette",
		() => {
			if (isOpen) {
				requestDismiss("close");
				return;
			}
			if (isDialogOrMenuOpen()) return;
			setOpen(true);
		},
		enabled,
	);
	// Quick open: the palette's file search for the session on screen. Pressed
	// again while it is showing, it closes, like ⌘K.
	useAppShortcut(
		"quick-open-file",
		() => {
			if (isOpen) {
				requestDismiss("close");
				return;
			}
			if (isDialogOrMenuOpen()) return;
			resetTransient();
			setView(currentFileTarget ? { mode: "file-search", target: currentFileTarget, parent: "root" } : { mode: "root" });
			setOpen(true);
		},
		enabled,
	);

	// Digits belong to the palette's own list while it is open.
	useEffect(() => {
		if (!enabled || !isOpen) return;
		const swallowIndexedChords = (event: KeyboardEvent) => {
			if ((event.metaKey || event.ctrlKey) && /^[1-9]$/.test(event.key)) {
				event.preventDefault();
				event.stopPropagation();
			}
		};
		window.addEventListener("keydown", swallowIndexedChords, true);
		return () => window.removeEventListener("keydown", swallowIndexedChords, true);
	}, [enabled, isOpen]);

	return {
		handleTaskCreated,
		onSelectItem,
	};
}
