import { Outlet } from "@tanstack/react-router";
import { memo, Suspense } from "react";
import { CenterPanelShell } from "../components/CenterPanelShell";
import { ShellTopbar } from "../components/ShellTopbar";
import { LazyChunkBoundary } from "../components/LazyChunkBoundary";
import { useUiStore } from "../stores/ui-store";
import { type WorkspaceSession } from "../types/workspace";
import { OrchestratorStartingChat } from "./lazyChunks";
import { ShellOutlet } from "./ShellOutlet";
import { isLinux, framedAppTopbar } from "./platform";

/**
 * The shell must observe the complete workspace list for the sidebar, but a
 * streamed update there should not reconcile the active route surface. Keep
 * the center frame behind a primitive-only memo boundary; SessionView and the
 * board own their more granular workspace subscriptions.
 */
export const ShellCenter = memo(function ShellCenter({
	hideShellTopbar,
	isSessionRoute,
	selfFramedCenterPanel,
	startingOrchestrator,
	startingSteps,
}: {
	hideShellTopbar: boolean;
	isSessionRoute: boolean;
	selfFramedCenterPanel: boolean;
	startingOrchestrator: boolean;
	startingSteps?: WorkspaceSession["provisionSteps"];
}) {
	const panelClassName = isSessionRoute || startingOrchestrator ? "center-panel-shell--session" : undefined;
	// Linux retains an outer drag strip. macOS uses the shared header itself;
	// an extra strip there would displace session tabs from the native controls.
	// Windows already owns a separate WindowTitlebar.
	const draggableSessionFrame = (isSessionRoute || startingOrchestrator) && isLinux;
	const settingsOpen = useUiStore((state) => state.settingsModal?.scope === "global");
	const startingChat = startingOrchestrator ? <LazyChunkBoundary><Suspense fallback={null}><OrchestratorStartingChat steps={startingSteps} /></Suspense></LazyChunkBoundary> : null;
	// Settings is a self-framed route, so its starting chat gets its own panel.
	const selfFramedOutlet = <>
		<div className={startingOrchestrator ? "absolute inset-0 invisible pointer-events-none" : "contents"} inert={startingOrchestrator || undefined}><Outlet /></div>
		{startingChat ? <CenterPanelShell>{startingChat}</CenterPanelShell> : null}
	</>;
	if (hideShellTopbar) {
		return selfFramedCenterPanel ? selfFramedOutlet : (
			<CenterPanelShell className={panelClassName} draggableSessionFrame={draggableSessionFrame}>
				<ShellOutlet startingChat={startingChat} />
			</CenterPanelShell>
		);
	}
	if (framedAppTopbar) {
		return (
			<CenterPanelShell className={panelClassName} draggableSessionFrame={draggableSessionFrame}>
				{isSessionRoute || startingOrchestrator || settingsOpen ? null : <ShellTopbar />}
				<ShellOutlet startingChat={startingChat} />
			</CenterPanelShell>
		);
	}
	return (
		<CenterPanelShell className={panelClassName} draggableSessionFrame={draggableSessionFrame}>
			<ShellOutlet startingChat={startingChat} />
		</CenterPanelShell>
	);
});
