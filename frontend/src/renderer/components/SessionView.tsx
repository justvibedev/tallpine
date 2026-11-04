import { Suspense } from "react";
import { createPortal } from "react-dom";
import { BrowserPanelView } from "./BrowserPanel";
import { SidebarToggleButton } from "./SidebarToggleButton";
import { FilesTopbarHostContext } from "./files-topbar-host";
import { SessionFilesPopOut } from "./SessionFilesPopOut";
import { SessionBrowserPopOut } from "./SessionBrowserPopOut";
import { apiErrorCode, apiErrorMessage } from "../lib/api-client";
import { sessionAgentExited } from "../types/workspace";
import { LazyChunkBoundary } from "./LazyChunkBoundary";

import { SessionFileExplorer } from "./session-view/session-view-lazy";
import { SessionViewBody } from "./session-view/SessionViewBody";
import type { SessionViewProps } from "./session-view/session-view-types";
import { useSessionViewData } from "./session-view/useSessionViewData";
import { useSessionViewTerminals } from "./session-view/useSessionViewTerminals";
import { useSessionViewWorkspace } from "./session-view/useSessionViewWorkspace";
import { useSessionViewActions } from "./session-view/useSessionViewActions";
import { useSessionViewChrome } from "./session-view/useSessionViewChrome";
import { noDragStyle } from "./session-view/session-view-helpers";

// The session detail screen: terminal + git rail. On Win/Linux the shell owns
// ShellTopbar above this view; when the platform hides the shell topbar
// (macOS), the same topbar mounts here so the outer panel stays full-height.
// Rendered by both the project-scoped and cross-project session routes.
// The persistent shell cache owns terminal lifetime by logical session + handle:
// route switches retain the xterm instance and latest output, while a replacement
// handle gets a clean xterm/mux binding.
//
// The inspector uses the same Motion spring as the left sidebar (gap width +
// x-transform). Summary/Reviews/Files share a utility width, while Browser
// automatically grows into a co-work canvas. Chat readability clamps either
// profile before the conversation can become unusably narrow.
export function SessionView(props: SessionViewProps) {
	const {
		sessionId,
		hostId,
	} = props;
	const data = useSessionViewData(props);
	const terms = useSessionViewTerminals(props, data);
	const workspace = useSessionViewWorkspace(props, data, terms);
	const actions = useSessionViewActions(props, data, terms, workspace);
	const chrome = useSessionViewChrome(props, data, terms, workspace, actions);
	const v = { ...data, ...terms, ...workspace, ...actions, ...chrome };
	const {
		autoResume,
		browserPopOutMounted,
		browserPopOutPhase,
		browserPopoutTopbarHost,
		browserPoppedOut,
		filesKind,
		filesPoppedOut,
		filesSplit,
		interfaceUi,
		isInspectorOpen,
		remoteBase,
		remoteSessionQuery,
		session,
		sessionGone,
		setBrowserPopoutTopbarHost,
		setFilesSplit,
		t,
		workspaceQuery,
		workspaceSessionQuery,
	} = data;
	const {
		openCenterFile,
	} = terms;
	const {
		browserAnnotationQueue,
		browserView,
		hasInspector,
	} = workspace;
	const {
		handleToggleBrowserPopOut,
		handleToggleFilesPopOut,
		setSwitcherHost,
	} = actions;
	const {
		handleToggleInspector,
		pinnedActionsRef,
	} = chrome;

	if (sessionGone) return null;
	if (!session && (hostId ? !remoteSessionQuery.isLoading : !workspaceQuery.isLoading && !workspaceSessionQuery.isLoading)) {
		const remoteCode = hostId && remoteSessionQuery.error ? apiErrorCode(remoteSessionQuery.error) : undefined;
		const remoteError = hostId ? t(remoteCode === "BAD_PASSWORD" ? "remote.hostUnauthorized"
			: remoteCode === "HOST_API_INCOMPATIBLE" ? "remote.hostIncompatible"
			: !remoteBase || remoteCode === "UPSTREAM_UNAVAILABLE" || remoteCode === "HOST_IDENTITY_UNVERIFIED"
				? "remote.hostOffline" : remoteSessionQuery.isError ? "remote.loadSessionFailed" : "session.notFound") : undefined;
		return (
			<div className="grid h-full place-items-center p-6 text-center font-mono text-xs text-passive" role={remoteError ? "alert" : undefined}>
				{remoteError ?? t("session.notFound")}
			</div>
		);
	}


	return (
		<div className="relative flex h-full min-h-0 flex-col bg-background text-foreground" data-testid="session-detail">
			{session?.unsupportedHarnessMessage ? (
				<p className="border-b border-border px-4 py-2 text-xs text-warning" role="status" data-testid="unsupported-harness-notice">
					{session.unsupportedHarnessMessage}
				</p>
			) : null}
			{!hostId && session?.mode !== "chat" && sessionAgentExited(session) && autoResume.variables === sessionId && autoResume.isError ? (
				<p className="px-4 py-2 text-xs text-error" role="alert">
					{apiErrorMessage(autoResume.error)}
				</p>
			) : null}
			<SessionViewBody props={props} v={v} />
			{hasInspector ? (
				<div className="session-pinned-actions" data-testid="session-pinned-actions" ref={pinnedActionsRef} style={noDragStyle}>
					{/* The panel switcher renders here (left of the toggle) from SessionInspector, so the pair stays at the window's right edge open or closed. */}
					<div className="flex items-center" ref={setSwitcherHost} />
					<SidebarToggleButton open={isInspectorOpen} onClick={handleToggleInspector} style={noDragStyle} />
				</div>
			) : null}
			{interfaceUi.dialogs}
			{/* Maximized files wear the maximized browser's chrome: a backdrop, the
          filter pinned in the titlebar band where the browser's address bar
          sits, and an inset frame for the explorer. The explorer mounts once
          the band exists so the filter never renders inline first. */}
			{filesPoppedOut && session
				? createPortal(
						<SessionFilesPopOut onClose={() => handleToggleFilesPopOut(false)}>{(topbarHost) =>
							<FilesTopbarHostContext.Provider value={topbarHost}>
								<LazyChunkBoundary>
								<Suspense fallback={null}>
									<SessionFileExplorer
 onOpenFile={openCenterFile}
										hostId={hostId}
										isMaximized
										onSplitChange={setFilesSplit}
										onToggleMaximized={handleToggleFilesPopOut}
										checkedOutBranch={session.branch}
										projectKind={filesKind}
										sessionId={session.id}
										split={filesSplit}
									/>
								</Suspense>
								</LazyChunkBoundary>
							</FilesTopbarHostContext.Provider>
						}</SessionFilesPopOut>,
						document.body,
					)
				: null}
			{/* Maximized browser: a fixed overlay across the app workspace,
          portaled to <body> so it escapes the shell layout (covering the
          sidebar + topbar, not just the session area) and sits outside any
          `[data-panel]` column, so the native WebContentsView is not clamped
          and fills the window below any native titlebar overlay. */}
			{browserPopOutMounted && session
				? createPortal(
						<SessionBrowserPopOut onClose={() => handleToggleBrowserPopOut(false)} onTopbarHost={setBrowserPopoutTopbarHost} phase={browserPopOutPhase === "open" ? "open" : "mounting"}>
								{browserPoppedOut && browserPopoutTopbarHost ? <BrowserPanelView
									active
									annotationQueue={browserAnnotationQueue}
									browserView={browserView}
									onTogglePopOut={handleToggleBrowserPopOut}
									poppedOut
									session={session}
									topbarHost={browserPopoutTopbarHost}
								/> : null}
						</SessionBrowserPopOut>,
						document.body,
					)
				: null}
		</div>
	);
}
