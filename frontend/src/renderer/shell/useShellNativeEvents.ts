import type { UseNavigateResult } from "@tanstack/react-router";
import type { useOpenShellTerminal } from "../hooks/useShellTerminals";
import { useEffect } from "react";
import { tallpineBridge } from "../lib/bridge";
import { useUiStore } from "../stores/ui-store";

export function useShellNativeEvents({ navigate, requestCreateProjectFromPath, requestNewShellTerminal, newShellTerminalNonce, setActiveShellTerminal, openShellTerminal, handledShellNonceRef, routeParams, scopedProjectId, isTerminalsRoute, navigateSession }: {
	navigate: UseNavigateResult<string>;
	requestCreateProjectFromPath: (path: string) => void;
	requestNewShellTerminal: () => void;
	newShellTerminalNonce: number;
	setActiveShellTerminal: (handleId: string | null) => void;
	openShellTerminal: ReturnType<typeof useOpenShellTerminal>;
	handledShellNonceRef: React.RefObject<number>;
	routeParams: { hostId?: string | undefined; projectId?: string | undefined; sessionId?: string | undefined; };
	scopedProjectId: string | undefined;
	isTerminalsRoute: boolean;
	navigateSession: (direction: 1 | -1, onlyNeedingAttention?: boolean) => void;
}) {
	// A folder was dropped on the app's taskbar icon/shortcut (main process,
	// cold start or an already-running instance) — feeds the same drop flow as
	// dragging a folder into the open window.
	useEffect(
		() => tallpineBridge.app.onOpenFolderPath((path) => requestCreateProjectFromPath(path)),
		[requestCreateProjectFromPath],
	);

	// New standalone terminal (⌘T / Ctrl+T), also detected in the main process so it
	// fires from inside a terminal pane. It raises the same store signal as the
	// tab-strip + button so the two cannot drift apart.
	useEffect(
		() =>
			tallpineBridge.app.onNewShellTerminalShortcut(() => {
				// The project board is not a terminal surface — ⌘T here used to yank
				// users into the standalone /terminals route (#4772). Sessions and the
				// dedicated terminals view keep the shortcut; explicit UI can still
				// open shells from the board.
				if (!routeParams.hostId && (routeParams.sessionId || isTerminalsRoute)) {
					requestNewShellTerminal();
				}
			}),
		[isTerminalsRoute, requestNewShellTerminal, routeParams.hostId, routeParams.sessionId],
	);

	// The shell layout is the single consumer of that signal, because it is the
	// only component mounted on EVERY route. Owning it here is what lets the
	// topbar + button and the keyboard shortcut work from a session or the
	// standalone terminals view alike — when the session view owned it, both
	// silently did nothing outside a session, since nothing was listening.
	//
	// Where the new shell becomes visible depends on where the user is: inside a
	// session it joins that pane's tab strip; on /terminals it joins that strip;
	// explicit board UI can still route here without the keyboard shortcut.
	useEffect(() => {
		if (handledShellNonceRef.current === newShellTerminalNonce) return;
		handledShellNonceRef.current = newShellTerminalNonce;
		if (routeParams.hostId) return;
		const shell = openShellTerminal.open({
			projectId: scopedProjectId,
			sessionId: routeParams.sessionId,
		});
		if (!shell) return;
		setActiveShellTerminal(shell.handleId);
		if (!routeParams.sessionId) {
			void navigate({ to: "/terminals" });
		}
	}, [
		newShellTerminalNonce,
		openShellTerminal,
		scopedProjectId,
		routeParams.sessionId,
		routeParams.hostId,
		navigate,
		setActiveShellTerminal,
	]);

	useEffect(
		() => tallpineBridge.app.onOpenSettingsShortcut(() => useUiStore.getState().openGlobalSettings()),
		[],
	);

	useEffect(() => {
		const disposePrevious = tallpineBridge.app.onPreviousSessionShortcut(() => navigateSession(-1));
		const disposeNext = tallpineBridge.app.onNextSessionShortcut(() => navigateSession(1));
		return () => {
			disposePrevious();
			disposeNext();
		};
	}, [navigateSession]);
}
