import { OfflineRemoteTerminal } from "../ChatWorkspaceTabs";
import { TerminalPane } from "../../TerminalPane";
import type { ChatContentState } from "./chat-content-state";
import type { ChatWorkspaceContentProps } from "./chat-content-props";

/** Reviewer and shell terminal tab panels of the chat workspace. */
export function AuxiliaryTabPanels({ props, v }: { props: ChatWorkspaceContentProps; v: ChatContentState }) {
	const {
		assetBaseUrl,
		session,
		reviewerTarget,
		shellTarget,
		daemonReady,
		theme,
	} = props;
	const {
		activeRemoteHostId,
		handleWheelZoom,
		isFullscreen,
		remoteCreateMux,
		t,
		terminalFontSize,
		toggleFullscreen,
		updateTerminalFontSize,
	} = v;
	return (
		<>
				{reviewerTarget && session ? (
					<div
						aria-label={t("chat.reviewerTerminalAria")}
						className="relative min-h-0 flex-1"
						data-testid="chat-reviewer-panel"
						onWheelCapture={handleWheelZoom}
						role="tabpanel"
					>
						<div className="h-full min-h-0" data-testid="chat-reviewer-terminal">
							{activeRemoteHostId && !assetBaseUrl ? <OfflineRemoteTerminal /> : <TerminalPane
								key={activeRemoteHostId ? `${activeRemoteHostId}:${assetBaseUrl}` : undefined}
								createMux={activeRemoteHostId ? remoteCreateMux : undefined}
								daemonReady={activeRemoteHostId ? Boolean(assetBaseUrl) : Boolean(daemonReady)}
								fontSize={terminalFontSize}
								isFullscreen={isFullscreen}
								onChangeFontSize={updateTerminalFontSize}
								onToggleFullscreen={toggleFullscreen}
								session={session}
								terminalTarget={reviewerTarget}
								theme={theme ?? "dark"}
							/>}
						</div>
					</div>
				) : null}
				{shellTarget && session ? (
					<div
						aria-label={t("terminal.shellAria")}
						className="relative min-h-0 flex-1"
						data-testid="chat-shell-panel"
						onWheelCapture={handleWheelZoom}
						role="tabpanel"
					>
						<div className="h-full min-h-0" data-testid="chat-shell-terminal">
							{activeRemoteHostId && !assetBaseUrl ? <OfflineRemoteTerminal /> : <TerminalPane
								key={activeRemoteHostId ? `${activeRemoteHostId}:${assetBaseUrl}` : undefined}
								createMux={activeRemoteHostId ? remoteCreateMux : undefined}
								daemonReady={activeRemoteHostId ? Boolean(assetBaseUrl) : Boolean(daemonReady)}
								fontSize={terminalFontSize}
								focusRequested
								isFullscreen={isFullscreen}
								onChangeFontSize={updateTerminalFontSize}
								onToggleFullscreen={toggleFullscreen}
								session={session}
								terminalTarget={shellTarget}
								theme={theme ?? "dark"}
							/>}
						</div>
					</div>
				) : null}
		</>
	);
}
