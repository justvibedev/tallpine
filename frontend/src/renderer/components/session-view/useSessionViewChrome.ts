import { useCallback, useEffect, useLayoutEffect, useRef } from "react";
import { usePinnedActionsWidth } from "../../hooks/usePinnedActionsWidth";
import { initialInspectorSize } from "../SessionInspectorRail";
import { openBacklogPage } from "../../lib/open-backlog-page";
import { tallpineBridge } from "../../lib/bridge";
import { focusAfterPaint, useSessionShortcut } from "../../hooks/useAppShortcut";
import { useUiStore } from "../../stores/ui-store";
import { INSPECTOR_SEPARATOR_RESERVE_PX } from "../../lib/inspector-width";
import { useSessionViewData } from "./useSessionViewData";
import { useSessionViewTerminals } from "./useSessionViewTerminals";
import { useSessionViewWorkspace } from "./useSessionViewWorkspace";
import { useSessionViewActions } from "./useSessionViewActions";
import { SessionViewProps } from "./session-view-types";
import { INSPECTOR_COMPACT_MAX_PX, topbarSecondaryLabelMode } from "./session-view-helpers";

/** Shortcuts, pinned actions and inspector motion effects for SessionView. */
export function useSessionViewChrome(props: SessionViewProps, data: ReturnType<typeof useSessionViewData>, terms: ReturnType<typeof useSessionViewTerminals>, workspace: ReturnType<typeof useSessionViewWorkspace>, actions: ReturnType<typeof useSessionViewActions>) {
	const {
		sessionId,
		hostId,
	} = props;
	const {
		browserOnly,
		centerFileRequestsBySession,
		inspectorView,
		isInspectorOpen,
		navigate,
		session,
		sessionSplitRef,
		setBrowserContentRevealed,
		setInspectorOpenForSession,
		setInspectorSettledClosed,
		showGlobalToast,
		startTerminalLiveResize,
		stopTerminalLiveResize,
		t,
		terminalTarget,
		toggleInspector,
		uiSessionId,
		workspaceResizeTimerRef,
	} = data;
	const {
		addShellTerminal,
		closedFilePathsRef,
		openCenterFile,
		selectSessionTerminal,
		selectShellTerminal,
		shellTerminals,
	} = terms;
	const {
		browserEntryWidthFloorRef,
		hasInspector,
		isOrchestrator,
		sizing,
	} = workspace;
	const {
		prepareFilesInspector,
	} = actions;
	const pinnedActionsRef = usePinnedActionsWidth();
	const handleToggleInspector = useCallback(() => {
		if (browserOnly) setBrowserContentRevealed(uiSessionId, true);
		toggleInspector(uiSessionId);
	}, [browserOnly, uiSessionId, toggleInspector, setBrowserContentRevealed]);

	// Keyboard commands scoped to the session on screen. The main process matches
	// the chord (see main/app-shortcuts.ts), so these also work from xterm and the
	// native Browser view.
	useSessionShortcut("toggle-inspector", handleToggleInspector, hasInspector);
	useSessionShortcut("open-backlog-panel", () => openBacklogPage(navigate), hasInspector && !hostId);
	useSessionShortcut(
		"toggle-diff",
		() => {
			if (browserOnly) return;
			const showingDiff =
				isInspectorOpen &&
				inspectorView === "files" &&
				(useUiStore.getState().inspectorSessions[uiSessionId]?.filesChangedOnly ?? true);
			if (showingDiff) setInspectorOpenForSession(uiSessionId, false);
			else prepareFilesInspector();
		},
		hasInspector,
	);
	useSessionShortcut(
		"toggle-terminal",
		() => {
			if (terminalTarget.kind === "shell") {
				selectSessionTerminal();
				focusAfterPaint("[aria-label='Message the agent']");
				return;
			}
			const shell = shellTerminals[shellTerminals.length - 1];
			if (shell) selectShellTerminal(shell.handleId);
			else if (!isOrchestrator) addShellTerminal();
			focusAfterPaint(".xterm-helper-textarea");
		},
		hasInspector,
	);
	useSessionShortcut(
		"reopen-closed-tab",
		() => {
			const path = closedFilePathsRef.current[uiSessionId]?.pop();
			if (path) {const request=centerFileRequestsBySession[uiSessionId]?.[path];openCenterFile(request?.path ?? path, { ...request, editing:false, mode: "file" });}
		},
		hasInspector,
	);
	useSessionShortcut(
		"copy-session-path",
		() => {
			if (!session || hostId) return;
			tallpineBridge.editorHandoff.copyWorkspacePath(sessionId).then(
				() => showGlobalToast(t("shortcut.sessionPathCopied")),
				(error: unknown) =>
					showGlobalToast(t("shortcut.sessionPathCopyFailed"), error instanceof Error ? error.message : undefined, "error"),
			);
		},
		hasInspector,
	);
	useSessionShortcut(
		"model-picker",
		() => {
			const picker = document.querySelector<HTMLElement>("[data-shortcut-target='model-picker']:not([disabled])");
			if (!picker) return;
			// Radix menu triggers open on pointerdown or Enter, never on a synthetic click.
			picker.dataset.shortcutOpened = "";
			picker.focus();
			picker.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
		},
		hasInspector,
	);

	const inspectorMotionReadyRef = useRef<string | null>(null);
	const handleInspectorCloseAnimationComplete = useCallback(() => {
		setInspectorSettledClosed(true);
	}, []);
	useLayoutEffect(() => {
		if (!hasInspector) {
			setInspectorSettledClosed(true);
			stopTerminalLiveResize();
			return;
		}
		if (inspectorMotionReadyRef.current !== uiSessionId) {
			setInspectorSettledClosed(!isInspectorOpen);
			stopTerminalLiveResize();
			if (workspaceResizeTimerRef.current !== null) window.clearTimeout(workspaceResizeTimerRef.current);
			workspaceResizeTimerRef.current = null;
			sessionSplitRef.current?.removeAttribute("data-workspace-resizing");
			browserEntryWidthFloorRef.current = null;
		}
	}, [hasInspector, isInspectorOpen, uiSessionId, stopTerminalLiveResize]);
	useEffect(() => {
		if (!hasInspector || inspectorMotionReadyRef.current !== uiSessionId) return;
		if (isInspectorOpen) {
			setInspectorSettledClosed(false);
			const groupWidth = sessionSplitRef.current?.clientWidth || window.innerWidth;
			const availableWidth = Math.max(0, groupWidth - INSPECTOR_SEPARATOR_RESERVE_PX);
			const targetInspectorWidth = Number.parseFloat(initialInspectorSize(sizing, availableWidth));
			startTerminalLiveResize(
				targetInspectorWidth <= INSPECTOR_COMPACT_MAX_PX ? "compact" : "expanded",
				topbarSecondaryLabelMode(Math.max(0, availableWidth - targetInspectorWidth)),
			);
			return;
		}
		const groupWidth = sessionSplitRef.current?.clientWidth || window.innerWidth;
		startTerminalLiveResize("expanded", topbarSecondaryLabelMode(groupWidth));
	}, [hasInspector, isInspectorOpen, uiSessionId, sizing, startTerminalLiveResize]);
	useEffect(() => {
		if (!hasInspector) {
			inspectorMotionReadyRef.current = null;
			return;
		}
		inspectorMotionReadyRef.current = uiSessionId;
		return () => {
			inspectorMotionReadyRef.current = null;
		};
	}, [hasInspector, uiSessionId]);
	return {
		handleInspectorCloseAnimationComplete,
		handleToggleInspector,
		pinnedActionsRef,
	};
}
