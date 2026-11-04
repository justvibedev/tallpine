import { useCallback, useEffect, type AnimationEvent } from "react";
import { workspaceQueryKey } from "../../hooks/useWorkspaceQuery";
import { type NavigateTarget, type WorkspaceFileSearchTarget } from "../../lib/command-palette";
import { spawnOrchestrator } from "../../lib/spawn-orchestrator";
import { findProjectOrchestrator, hasConfiguredOrchestratorAgent, STANDALONE_WORKSPACE_ID } from "../../types/workspace";
import { useUiStore } from "../../stores/ui-store";
import { usePaletteData } from "./usePaletteData";
import { PaletteView } from "./palette-constants";

/** Palette open/close, view stack and navigation callbacks. */
export function usePaletteNavigation(data: ReturnType<typeof usePaletteData>) {
	const {
		closeResetTimerRef,
		composerBusyRef,
		composerDirtyRef,
		createProjectFlowMounted,
		createProjectFlowPendingOpen,
		navigate,
		pendingDismiss,
		queryClient,
		resolvedTheme,
		restoreSessionById,
		runGenerationRef,
		scoped,
		setCreateProjectFlowMounted,
		setCreateProjectFlowOpenSignal,
		setCreateProjectFlowPendingOpen,
		setDebouncedQuery,
		setError,
		setOpen,
		setPendingDismiss,
		setQuery,
		setSelectedValue,
		setThemePreference,
		setView,
		t,
		view,
		viewRef,
		workspaces,
	} = data;
	const resetTransient = useCallback(() => {
		runGenerationRef.current += 1;
		setQuery("");
		setDebouncedQuery("");
		setSelectedValue("");
		setError(null);
	}, []);

	const closePalette = useCallback(() => {
		// Keep the current query visible while Radix plays the closing animation.
		// Clearing it here causes the palette to flash an empty search before it
		// is removed, especially when an action also changes the theme.
		runGenerationRef.current += 1;
		setOpen(false);
		setView({ mode: "root" });
		setPendingDismiss(null);
		if (closeResetTimerRef.current !== null) window.clearTimeout(closeResetTimerRef.current);
		// Reduced-motion mode disables the closing animation, so keep a fallback
		// reset for environments where no animationend event will arrive.
		closeResetTimerRef.current = window.setTimeout(() => {
			closeResetTimerRef.current = null;
			if (!useUiStore.getState().isCommandPaletteOpen) resetTransient();
		}, 150);
	}, [resetTransient, setOpen]);
	// The import flow has its own cloud/query/dialog subtree. Mounting it beside a
	// permanently retained palette made every palette render pay for an unrelated
	// feature. Mount it only after the user chooses New project, then pulse its
	// existing programmatic-open signal on the following commit (the flow seeds its
	// signal ref on mount, so doing both in one render would intentionally no-op).
	useEffect(() => {
		if (!createProjectFlowMounted || !createProjectFlowPendingOpen) return;
		setCreateProjectFlowPendingOpen(false);
		setCreateProjectFlowOpenSignal((current) => current + 1);
	}, [createProjectFlowMounted, createProjectFlowPendingOpen]);
	const openNewProject = useCallback(() => {
		closePalette();
		setCreateProjectFlowMounted(true);
		setCreateProjectFlowPendingOpen(true);
	}, [closePalette]);
	const openExistingProject = useCallback(
		(path: string) => {
			const workspace = workspaces.find((candidate) => candidate.path === path);
			if (!workspace) return;
			closePalette();
			void navigate({ to: "/projects/$projectId", params: { projectId: workspace.id } });
		},
		[closePalette, navigate, workspaces],
	);

	const resetAfterClose = useCallback(() => {
		if (closeResetTimerRef.current !== null) {
			window.clearTimeout(closeResetTimerRef.current);
			closeResetTimerRef.current = null;
		}
		if (!useUiStore.getState().isCommandPaletteOpen) resetTransient();
	}, [resetTransient]);

	useEffect(
		() => () => {
			if (closeResetTimerRef.current !== null) window.clearTimeout(closeResetTimerRef.current);
		},
		[],
	);

	const handlePaletteAnimationEnd = useCallback(
		(event: AnimationEvent<HTMLDivElement>) => {
			if (event.target === event.currentTarget) resetAfterClose();
		},
		[resetAfterClose],
	);

	const popView = useCallback(() => {
		const current = viewRef.current;
		if (current.mode === "file-search" && current.parent === "picker") {
			setView({ mode: "file-session-picker", projectId: current.target.projectId });
		} else {
			setView({ mode: "root" });
		}
		setPendingDismiss(null);
		resetTransient();
	}, [resetTransient]);

	const pushView = useCallback(
		(next: PaletteView) => {
			setView(next);
			setPendingDismiss(null);
			resetTransient();
		},
		[resetTransient],
	);

	const requestDismiss = useCallback(
		(target: "pop" | "close") => {
			const current = viewRef.current;
			if (composerBusyRef.current) return;
			if (current.mode === "new-task" && composerDirtyRef.current) {
				setPendingDismiss(target);
				return;
			}
			if (target === "close" || current.mode === "root") {
				closePalette();
			} else {
				popView();
			}
		},
		[closePalette, popView],
	);

	const onComposerDirtyChange = useCallback((dirty: boolean) => {
		composerDirtyRef.current = dirty;
	}, []);

	const onComposerSubmittingChange = useCallback((submitting: boolean) => {
		composerBusyRef.current = submitting;
		if (submitting) setPendingDismiss(null);
	}, []);

	const confirmDiscard = useCallback(() => {
		if (composerBusyRef.current) return;
		const target = pendingDismiss;
		composerDirtyRef.current = false;
		setPendingDismiss(null);
		if (target === "close") closePalette();
		else popView();
	}, [pendingDismiss, closePalette, popView]);

	useEffect(() => {
		if (view.mode === "session-actions" && !scoped) {
			setView({ mode: "root" });
			setQuery("");
			setSelectedValue("");
			setError(t("command.sessionUnavailable"));
		}
	}, [view, scoped, t]);

	const toggleTheme = useCallback(() => {
		setThemePreference(resolvedTheme === "dark" ? "light" : "dark");
	}, [resolvedTheme, setThemePreference]);

	const navigateToTarget = useCallback(
		(target: NavigateTarget) => {
			switch (target.to) {
				case "/":
					void navigate({ to: target.to });
					break;
				case "/settings":
					// Modal — do not route to /settings (that legacy path redirects home).
					useUiStore.getState().openGlobalSettings();
					break;
				case "/sessions/$sessionId":
					void navigate({ to: target.to, params: target.params });
					break;
				case "/projects/$projectId":
					void navigate({ to: target.to, params: target.params });
					break;
				case "/projects/$projectId/settings":
					useUiStore.getState().openProjectSettings(target.params.projectId);
					break;
				case "/projects/$projectId/sessions/$sessionId":
				case "/host/$hostId/project/$projectId/session/$sessionId":
				case "/host/$hostId/session/$sessionId":
					void navigate({ to: target.to, params: target.params });
					break;
			}
		},
		[navigate, workspaces],
	);

	const sessionRoute = useCallback(
		(projectId: string, sessionId: string): Extract<NavigateTarget, { to: "/sessions/$sessionId" | "/projects/$projectId/sessions/$sessionId" }> => {
			return projectId === STANDALONE_WORKSPACE_ID
				? { to: "/sessions/$sessionId", params: { sessionId } }
				: { to: "/projects/$projectId/sessions/$sessionId", params: { projectId, sessionId } };
		},
		[],
	);

	const fileSessionRoute = useCallback((target: WorkspaceFileSearchTarget): NavigateTarget => {
		if (target.hostId) {
			return target.projectId === STANDALONE_WORKSPACE_ID
				? { to: "/host/$hostId/session/$sessionId", params: { hostId: target.hostId, sessionId: target.sessionId } }
				: {
					to: "/host/$hostId/project/$projectId/session/$sessionId",
					params: { hostId: target.hostId, projectId: target.projectId, sessionId: target.sessionId },
				};
		}
		return sessionRoute(target.projectId, target.sessionId);
	}, [sessionRoute]);

	const blockedByRestart = useCallback((projectId: string) => {
		if (!useUiStore.getState().restartingProjectIds.has(projectId)) return false;
		setError(t("command.orchestratorRestarting"));
		return true;
	}, [t]);

	const openOrchestrator = useCallback(
		async (projectId: string) => {
			if (blockedByRestart(projectId)) return;
			const orchestrator = findProjectOrchestrator(workspaces, projectId);
			if (orchestrator) {
				navigateToTarget(sessionRoute(projectId, orchestrator.id));
				closePalette();
				return;
			}
			const workspace = workspaces.find((candidate) => candidate.id === projectId);
			if (!hasConfiguredOrchestratorAgent(workspace)) {
				if (workspace) {
					navigateToTarget({ to: "/projects/$projectId/settings", params: { projectId } });
					closePalette();
				}
				return;
			}
			const sessionId = await spawnOrchestrator(projectId, "command_palette");
			await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
			navigateToTarget(sessionRoute(projectId, sessionId));
			closePalette();
		},
		[workspaces, navigateToTarget, queryClient, closePalette, blockedByRestart, sessionRoute],
	);

	const resumeSession = useCallback(
		async (sessionId: string) => {
			const result = await restoreSessionById(sessionId);
			if (result.status === "success") return null;
			if (result.status === "not_resumable") return t("command.resumeNotResumable");
			return result.message;
		},
		[restoreSessionById, t],
	);

	return {
		blockedByRestart,
		closePalette,
		confirmDiscard,
		fileSessionRoute,
		handlePaletteAnimationEnd,
		navigateToTarget,
		onComposerDirtyChange,
		onComposerSubmittingChange,
		openExistingProject,
		openNewProject,
		openOrchestrator,
		pushView,
		requestDismiss,
		resetTransient,
		resumeSession,
		sessionRoute,
		toggleTheme,
	};
}
