import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { memo, useCallback, useEffect, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { SessionInspectorShellView, type InspectorView } from "@tallpine/product-ui";
import { sessionUiKey } from "../lib/hosts";
import { PanelSwitcher, type PanelSwitcherItem } from "./PanelSwitcher";
import { SessionArtifactsView, type ArtifactOpenRequest } from "./SessionArtifactsView";
import { orchestratorInspectorView } from "../lib/orchestrator-inspector-view";
import { PlanView } from "./PlanView";
import { useSessionPlan } from "../hooks/useSessionPlan";
import { sessionWorkspaceFilesQueryKey } from "../hooks/useSessionWorkspaceFiles";
import { useSessionWorkspaceFilesChangedCount } from "../hooks/workspace-files/workspace-review-prefetch";
import type { WorkspaceSession } from "../types/workspace";
import { type BrowserAnnotationQueueModel } from "./browser/browser-types";
import type { BrowserViewModel } from "../hooks/useBrowserView";
import { FilesActionsHostContext, FilesTopbarHostContext } from "./files-topbar-host";
import { useUiStore } from "../stores/ui-store";
import { OpenReviewerTerminal } from "./SessionInspector/types";
import { PANEL_SWITCHER_VIEWS, noDragStyle, VIEW_DEFS } from "./SessionInspector/viewDefs";
import { reviewsTabVisible } from "./SessionInspector/reviewActors";
import { SummaryView } from "./SessionInspector/SummaryView";
import { ReviewsView } from "./SessionInspector/ReviewsView";
import { BrowserView } from "./SessionInspector/BrowserView";
import { FilesView } from "./SessionInspector/FilesView";

export type { InspectorView } from "@tallpine/product-ui";

/**
 * Tabbed inspector rail beside the terminal (Summary · Reviews · Browser · Side chat · Files · Artifacts).
 */
export const SessionInspector = memo(function SessionInspector({
	browserOnly = false,
	session,
	hostId: hostIdProp,
	onOpenReviewerTerminal,
	browserPoppedOut = false,
	browserAnnotationQueue,
	isInspectorVisible = true,
	onToggleBrowserPopOut,
	artifactRequest,
	onArtifactRequestConsumed,
	onOpenArtifact,
	onOpenFiles,
	onOpenReviewFile,
	onOpenReviewerChat,
	onWorkerMessageSent,
	filesView,
	browserView,
	sideChatView,
	onTogglePanel,
	panelOpen = true,
	switcherHost,
	view: viewProp,
	onViewChange,
}: {
	browserOnly?: boolean;
	session?: WorkspaceSession;
	hostId?: string;
	onOpenReviewerTerminal?: OpenReviewerTerminal;
	browserPoppedOut?: boolean;
	browserAnnotationQueue?: BrowserAnnotationQueueModel;
	isInspectorVisible?: boolean;
	onToggleBrowserPopOut?: (next: boolean) => void;
	/** The artifact the Artifacts view should show, e.g. one clicked in the Summary. */
	artifactRequest?: ArtifactOpenRequest | null;
	onArtifactRequestConsumed?: (key: number) => void;
	onOpenArtifact?: (target: { feedback?: boolean; path: string }) => void;
	onOpenFiles?: () => void;
	onOpenReviewFile?: (target: { line?: number; path: string }) => void;
	onOpenReviewerChat?: (reviewId: string) => void;
	onWorkerMessageSent?: () => void;
	filesView?: ReactNode;
	browserView?: BrowserViewModel;
	sideChatView?: ReactNode;
	/** Toggles the panel; the switcher's active icon uses it to close, an icon on a closed panel to open. */
	onTogglePanel?: () => void;
	/** Whether the panel is open; the switcher marks the active icon only then. */
	panelOpen?: boolean;
	/** Top-bar slot the panel switcher renders into (owned by the session view, left of the sidebar toggle). */
	switcherHost?: HTMLElement | null;
	/** Controlled active tab. Omit to let the inspector own its own selection. */
	view?: InspectorView;
	onViewChange?: (view: InspectorView) => void;
}) {
	const { t } = useTranslation();
	const hostId = hostIdProp ?? session?.hostId;
	const onlyBrowser = browserOnly;
	const [internalView, setInternalView] = useState<InspectorView>("summary");
	const [filesTopbarHost, setFilesTopbarHost] = useState<HTMLDivElement | null>(null);
	const [filesActionsHost, setFilesActionsHost] = useState<HTMLDivElement | null>(null);
	const requestedView = viewProp ?? internalView;
	// The Plan tab shows only for a session whose agent keeps one plan across turns.
	const sessionPlan = useSessionPlan(session?.id, hostId);
	// Badge the Browser tab when a preview target arrived without us opening it.
	const browserUnseen = useUiStore((state) =>
		session ? Boolean(state.inspectorSessions[sessionUiKey(session.id, hostId)]?.browserUnseen) : false,
	);
	const inspectorQueryClient = useQueryClient();
	const localFilesChangedCount = useSessionWorkspaceFilesChangedCount(
		onlyBrowser ? undefined : session?.id,
		hostId,
	);
	const localWorkspaceData = session ? inspectorQueryClient.getQueryData<{ files?: unknown[] }>(sessionWorkspaceFilesQueryKey(session.id, hostId)) : undefined;
	const filesChangedCount = localFilesChangedCount;
	const hasWorkspaceInventory = Boolean(localWorkspaceData?.files?.length);
	// An artifact link can open the Artifacts tab before a session refresh lists
	// the file. The request keeps the tab available until the file is listed or
	// another tab is picked.
	const [settledArtifactRequestKey, setSettledArtifactRequestKey] = useState<number>();
	const artifactRequestPending = artifactRequest != null && artifactRequest.key !== settledArtifactRequestKey;
	useEffect(() => {
		if (artifactRequest && session?.artifactFiles?.some((file) => file.path === artifactRequest.path)) {
			setSettledArtifactRequestKey(artifactRequest.key);
		}
	}, [artifactRequest, session?.artifactFiles]);
	const setView = useCallback((next: InspectorView) => {
		setInternalView(next);
		onViewChange?.(next);
		if (next === "files") onOpenFiles?.();
		if (next !== "artifacts" && artifactRequest) setSettledArtifactRequestKey(artifactRequest.key);
	}, [artifactRequest, onOpenFiles, onViewChange]);
	const openReviews = useCallback(() => setView("reviews"), [setView]);
	const openFilesView = useCallback(() => setView("files"), [setView]);
	// A persisted/controlled Reviews selection can outlive the last reviewable PR.
	// Keep the shell on a real, visible tab instead of rendering an empty, unlabelled body.
	const reviewsAvailable = reviewsTabVisible(session);
	// An orchestrator's inspector is the browser, plus the plan when it keeps one.
	const availableViewDefs = onlyBrowser ? VIEW_DEFS.filter((entry) => entry.id === "browser" || (entry.id === "plan" && sessionPlan !== undefined))
		: VIEW_DEFS.filter((entry) => (!hostId || entry.id !== "browser" || browserView) && (reviewsAvailable || entry.id !== "reviews")
			// The host passes a side chat view only where side chats can run.
			&& (entry.id !== "sideChat" || sideChatView !== undefined)
			// The Plan tab shows only when the agent keeps a session-wide plan.
			&& (entry.id !== "plan" || sessionPlan !== undefined)
			// The Artifacts tab shows only when the session has artifact files to list.
			&& (entry.id !== "artifacts" || (session?.artifactFiles?.length ?? 0) > 0 || artifactRequestPending));
	const view: InspectorView = onlyBrowser ? orchestratorInspectorView(requestedView, Boolean(sessionPlan)) : availableViewDefs.some((entry) => entry.id === requestedView) ? requestedView : "summary";
	const sessionLoaded = session !== undefined;
	useEffect(() => {
		// Until the session loads, its tabs are unknown: keep the requested view.
		if (view === requestedView || (!sessionLoaded && !onlyBrowser)) return;
		setInternalView(view);
		onViewChange?.(view);
	}, [onViewChange, onlyBrowser, requestedView, sessionLoaded, view]);
	const tabs = availableViewDefs.map((entry) => {
		const label = t(entry.labelKey);
		return {
			...entry,
			badge: entry.id === "browser" && browserUnseen,
			count: entry.id === "files" ? filesChangedCount : undefined,
			displayLabel:
				entry.id === "files" && filesChangedCount !== undefined && (filesChangedCount > 0 || hasWorkspaceInventory)
					? t("files.tabCount", { count: filesChangedCount })
					: label,
			label,
		};
	});
	// The switcher owns Browser and Plan, in this order; the in-panel tab strip keeps the rest.
	const switcherItems: PanelSwitcherItem[] = PANEL_SWITCHER_VIEWS.flatMap((id) => {
		const tab = tabs.find((entry) => entry.id === id);
		if (!tab) return [];
		return [{
			id,
			label: tab.label,
			icon: tab.icon,
			dot: id === "browser" && browserUnseen,
		}];
	});
	return (
		// SessionInspectorShellView (packages/product-ui) doesn't accept a
		// className, but styles.css's native-composition transparency cascade
		// targets a `.session-inspector` ancestor around it (to punch a
		// see-through hole for the live browser page when the compositor's
		// shell is raised for an overlay). `contents` keeps this wrapper out of
		// layout/flex entirely — it exists purely as a CSS selector anchor.
		<div className="session-inspector contents">
			{switcherHost && onTogglePanel ? createPortal(
				<PanelSwitcher
					activeView={view}
					items={switcherItems}
					onClose={onTogglePanel}
					onSelect={(next) => {
						setView(next);
						if (!panelOpen) onTogglePanel();
					}}
					open={panelOpen}
					style={noDragStyle}
				/>,
				switcherHost,
			) : null}
			<SessionInspectorShellView
				sideChatView={sideChatView ?? <p className="p-4 text-xs text-muted-foreground">{t("sideChat.unavailable")}</p>}
				activeView={view}
				ariaLabel={t("inspector.aria")}
				artifactsView={
					session ? (
						<SessionArtifactsView hostId={hostId} key={sessionUiKey(session.id, hostId)} onRequestConsumed={onArtifactRequestConsumed} request={artifactRequest} session={session} />
					) : undefined
				}
				browserPoppedOut={browserPoppedOut}
				planView={sessionPlan ? <PlanView plan={sessionPlan} sessionTitle={session?.title} /> : undefined}
				browserView={
					session && (!hostId || browserView) ? (
						<BrowserView
							browserPoppedOut={browserPoppedOut}
							browserAnnotationQueue={browserAnnotationQueue}
							browserView={browserView}
							isActive={isInspectorVisible && !browserPoppedOut}
							onTogglePopOut={onToggleBrowserPopOut}
							session={session}
						/>
					) : undefined
				}
				filesView={
					session ? (
						<FilesTopbarHostContext.Provider value={filesTopbarHost}>
							<FilesActionsHostContext.Provider value={filesActionsHost}>
								<FilesView filesView={filesView} onOpenFiles={onOpenFiles} />
							</FilesActionsHostContext.Provider>
						</FilesTopbarHostContext.Provider>
					) : undefined
				}
				headerActions={
					<>
						{view === "files" && filesView ? (
							<div className="files-panel__topbar-host min-w-0 flex-1" ref={setFilesTopbarHost} />
						) : null}
						<div className="session-inspector__trailing flex shrink-0 items-center">
							{view === "files" && filesView ? <div className="session-inspector__files-actions flex items-center gap-1" ref={setFilesActionsHost} /> : null}
							<span aria-hidden="true" className="session-inspector-actions-spacer" />
						</div>
					</>
				}
				isVisible={isInspectorVisible}
				loadingText={session ? undefined : t("inspector.loadingSession")}
				onViewChange={setView}
				reviewsView={
					session ? <ReviewsView hostId={hostId} key={sessionUiKey(session.id, hostId)} onOpenReviewFile={onOpenReviewFile} onOpenReviewerTerminal={onOpenReviewerTerminal} onOpenReviewerChat={onOpenReviewerChat} onWorkerMessageSent={onWorkerMessageSent} session={session} /> : undefined
				}
				summaryView={
					session ? (
						<SummaryView
							canOpenReviews={reviewsAvailable}
							hostId={hostId}
							key={sessionUiKey(session.id, hostId)}
							onOpenArtifact={onOpenArtifact}
							onOpenFiles={openFilesView}
							onOpenReviews={openReviews}
							session={session}
						/>
					) : undefined
				}
				tabs={tabs.filter((tab) => !PANEL_SWITCHER_VIEWS.includes(tab.id))}
			/>
		</div>
	);
});
