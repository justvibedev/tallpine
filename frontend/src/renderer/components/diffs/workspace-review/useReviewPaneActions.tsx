import { useCallback, useEffect, useRef } from "react";
import { ChevronRight, FileCode2 } from "lucide-react";
import { invalidateWorkspaceViews } from "../../../hooks/workspace-files/workspace-review-prefetch";
import { workspaceErrorCode } from "../../../hooks/workspace-files/workspace-request-error";
import { type WorkspaceFileSummary } from "../../../hooks/workspace-files/workspace-files-types";
import { cn } from "../../../lib/utils";
import { statusLabel, statusTone } from "../../../lib/workspace-file-status";
import { type FileOpenOptions } from "../../FileContentPane";
import { RetryButton } from "../../workspace-diff/DiffChrome";
import { WorkspaceEntryIcon } from "../../WorkspaceEntryIcon";
import { Button } from "../../ui/button";
import { Checkbox } from "../../ui/checkbox";
import { ImageDiffView, isWorkspaceImagePath } from "../../ImageDiffView";
import type { WorkspaceReviewPaneProps } from "./review-pane-props";
import { useReviewPaneState } from "./useReviewPaneState";
import { useReviewPaneData } from "./useReviewPaneData";
import { isDeferredByDefault } from "./review-helpers";
import { HeaderActionTooltip } from "./HeaderActionTooltip";

/** Annotation, collapse, scroll and fallback rendering for the workspace review pane. */
export function useReviewPaneActions(props: WorkspaceReviewPaneProps, state: ReturnType<typeof useReviewPaneState>, data: ReturnType<typeof useReviewPaneData>) {
	const {
		annotation,
		target,
		sessionId,
		hostId,
		split,
		collapseRequest,
		onAllCollapsedChange,
		scrollRequest,
	} = props;
	const {
		activeBatchCount,
		batches,
		codeViewRef,
		collapsedPaths,
		fallbackRows,
		files,
		loadedDeferredPaths,
		onOpenFile,
		patchQueries,
		pendingScrollPath,
		queryClient,
		regularBatches,
		reviewSelectionKey,
		scope,
		setActiveBatchCount,
		setCollapsedPaths,
		setLoadedDeferredPaths,
		setPendingScrollPath,
		t,
		toggleViewed,
		viewed,
		workspaceVersion,
	} = state;
	const {
		items,
		metadataByPath,
		pendingDiffPaths,
		serverDeferredByPath,
		summaryById,
		endOfFileContents,
	} = data;
	const beginLineAnnotation = useCallback((itemId: string, lineNumber: number, side: "deletions" | "additions") => {
		const file = summaryById.get(itemId);
		if (!file) return;
		annotation.begin({
			path: file.path,
			previousPath: file.previousPath,
			side: side === "deletions" ? "old" : "new",
			line: lineNumber,
			oldLine: side === "deletions" ? lineNumber : undefined,
			newLine: side === "additions" ? lineNumber : undefined,
			scope,
			workspaceVersion,
			fileFingerprint: file.fileFingerprint,
			surface: "review",
		});
	}, [annotation, workspaceVersion, scope, summaryById]);
	const toggleCollapsed = useCallback((file: WorkspaceFileSummary) => {
		const path = file.path;
		// An explicit expansion opts deferred files into loading, just like
		// Expand all. Keep that choice when the file is collapsed again.
		if (collapsedPaths.has(path) && isDeferredByDefault(file) && !loadedDeferredPaths.has(path)) {
			setLoadedDeferredPaths((current) => current.has(path) ? current : new Set(current).add(path));
			setPendingScrollPath(path);
		}
		setCollapsedPaths((current) => {
			const next = new Set(current);
			if (next.has(path)) next.delete(path);
			else next.add(path);
			return next;
		});
	}, [collapsedPaths, loadedDeferredPaths]);
	const collapsePath = useCallback((path: string) => {
		setCollapsedPaths((current) => {
			if (current.has(path)) return current;
			const next = new Set(current);
			next.add(path);
			return next;
		});
	}, []);
	const markViewed = useCallback((file: WorkspaceFileSummary, checked: boolean) => {
		toggleViewed(file);
		if (checked) collapsePath(file.path);
	}, [collapsePath, toggleViewed]);
	const collapseAll = useCallback(() => {
		setCollapsedPaths(new Set(files.map((file) => file.path)));
	}, [files]);
	const expandAll = useCallback(() => {
		setLoadedDeferredPaths(new Set(files.filter(isDeferredByDefault).map((file) => file.path)));
		setCollapsedPaths(new Set());
	}, [files]);
	const allFilesCollapsed = files.length > 0 && files.every((file) => collapsedPaths.has(file.path));
	useEffect(() => {
		onAllCollapsedChange?.(allFilesCollapsed);
	}, [allFilesCollapsed, onAllCollapsedChange]);
	// Only a request made while this pane is showing acts; one already handled
	// before a remount must not collapse the next review.
	const handledCollapseKey = useRef(collapseRequest?.key);
	const collapseActions = useRef({ collapseAll, expandAll });
	collapseActions.current = { collapseAll, expandAll };
	useEffect(() => {
		if (!collapseRequest || collapseRequest.key === handledCollapseKey.current) return;
		handledCollapseKey.current = collapseRequest.key;
		if (collapseRequest.expand) collapseActions.current.expandAll();
		else collapseActions.current.collapseAll();
	}, [collapseRequest]);
	// The rail can pick a file whose diff isn't in the list yet: a deferred file
	// loads, a queued batch starts, and the scroll waits until the diff is there.
	// The picked file opens, since it was asked for.
	const handledScrollKey = useRef(scrollRequest?.key);
	useEffect(() => {
		if (!scrollRequest || scrollRequest.key === handledScrollKey.current) return;
		handledScrollKey.current = scrollRequest.key;
		const { path } = scrollRequest;
		const file = files.find((entry) => entry.path === path);
		if (!file) return;
		if (isDeferredByDefault(file)) setLoadedDeferredPaths((current) => (current.has(path) ? current : new Set(current).add(path)));
		setCollapsedPaths((current) => {
			if (!current.has(path)) return current;
			const next = new Set(current);
			next.delete(path);
			return next;
		});
		setPendingScrollPath(path);
	}, [files, scrollRequest]);
	useEffect(() => {
		if (!pendingScrollPath) return;
		const id = `${reviewSelectionKey}:${pendingScrollPath}`;
		if (items.some((item) => item.id === id)) {
			codeViewRef.current?.scrollTo({ type: "item", id, align: "start" });
			setPendingScrollPath(null);
			return;
		}
		const batchIndex = batches.findIndex((paths) => paths.includes(pendingScrollPath));
		if (batchIndex >= activeBatchCount && batchIndex < regularBatches.length) {
			setActiveBatchCount(batchIndex + 1);
			return;
		}
		if (batchIndex >= 0 && pendingDiffPaths.has(pendingScrollPath)) return;
		// Footer rows are real DOM elements rather than virtualized items. Wait
		// for CodeView's footer portal to commit before bringing one into view.
		const frame = requestAnimationFrame(() => {
			fallbackRows.current.get(pendingScrollPath)?.scrollIntoView({ block: "start" });
			setPendingScrollPath(null);
		});
		return () => cancelAnimationFrame(frame);
	}, [activeBatchCount, batches, items, pendingDiffPaths, pendingScrollPath, regularBatches.length, reviewSelectionKey]);
	const retryAll = () => patchQueries.forEach((query) => void query.refetch());
	const firstError = patchQueries.find((query) => query.error)?.error;
	const groupError = patchQueries.flatMap((query) => query.data?.groups ?? []).flatMap((group) => group.errors ?? [])[0];
	const loading = patchQueries.some((query) => query.isPending);
	const fileOpenContext: FileOpenOptions = {...(target.source ? {source:target.source} : target.sha ? {commitSha:target.sha}:{}),scope};
	const changes = target.changes;
	const emptyMessage = changes.kind === "uncommitted" || changes.kind === "staged" || changes.kind === "unstaged"
		? t(`files.changes.empty.${changes.kind}`)
		: changes.kind === "combined" ? t("files.noneChanged") : t("files.noneInSource");
	const conflict = target.conflict;

	// A moved tip or base fails the read it fenced; the views' new tips key new reads.
	const refMoved = [...patchQueries, ...endOfFileContents].some((query) => workspaceErrorCode(query.error) === "WORKSPACE_REF_MOVED");
	useEffect(() => {
		if (refMoved) invalidateWorkspaceViews(queryClient, sessionId, hostId);
	}, [hostId, queryClient, refMoved, sessionId]);

	const fallbackFiles = files.filter((file) => file.binary || !metadataByPath.has(file.path));
	// Keep non-text rows in Pierre's measured scroll content, including reviews
	// with no text items. Its ResizeObserver tracks late image loads and collapse.
	const renderFallbackFiles = () => (
		<>
			{fallbackFiles.map((file) => {
				const collapsed = collapsedPaths.has(file.path);
				const deferred = isDeferredByDefault(file) && !loadedDeferredPaths.has(file.path);
				const serverDeferredReason = serverDeferredByPath.get(file.path);
				const pending = pendingDiffPaths.has(file.path);
				const unavailable = !file.binary && !deferred && !serverDeferredReason && !pending;
				const image = file.binary && !target.source && target.openable && isWorkspaceImagePath(file.path);
				const isViewed = viewed.has(file.path);
				return (
					<div className="bg-background" key={file.path} ref={(node) => { if (node) fallbackRows.current.set(file.path, node); else fallbackRows.current.delete(file.path); }}>
						<div className="group/file-header flex h-9 min-w-0 items-center gap-2 pl-4 pr-1.5 hover:bg-interactive-hover/40">
							<button
								aria-expanded={!collapsed}
								aria-label={t(collapsed ? "files.expandFile" : "files.collapseFile", { file: file.path })}
								className="flex h-full min-w-0 flex-1 items-center gap-2 text-left text-[length:var(--font-size-base)] focus-visible:underline"
								onClick={() => toggleCollapsed(file)}
								type="button"
							>
								<ChevronRight aria-hidden="true" className={cn("size-3.5 shrink-0 text-muted-foreground", !collapsed && "rotate-90")} />
								<WorkspaceEntryIcon className="size-icon-xl" kind="file" name={file.path.split("/").pop() ?? file.path} />
								<span className="truncate" title={file.path}>{file.path}</span>
								<span className={cn("shrink-0 text-xs font-semibold", statusTone[file.status])}>{statusLabel[file.status]}</span>
							</button>
							{onOpenFile ? (
								<HeaderActionTooltip label={t("files.openFullFileGeneric")}>
									<Button aria-label={t("files.openFullFileGeneric")} className="size-6 shrink-0 text-muted-foreground hover:text-foreground" onClick={() => onOpenFile(file.path, { ...fileOpenContext, mode: "file" })} size="icon-sm" type="button" variant="ghost"><FileCode2 aria-hidden="true" className="size-icon-sm" /></Button>
								</HeaderActionTooltip>
							) : null}
							<HeaderActionTooltip label={isViewed ? t("files.markUnviewed", { file: file.path }) : t("files.markViewed", { file: file.path })}>
								<span className="grid size-6 shrink-0 place-items-center">
									<Checkbox
										aria-label={isViewed ? t("files.markUnviewed", { file: file.path }) : t("files.markViewed", { file: file.path })}
										checked={isViewed}
										className="size-4 border border-muted-foreground/70 bg-transparent data-[state=checked]:border-foreground data-[state=checked]:bg-foreground data-[state=checked]:text-on-foreground"
										onCheckedChange={(checked) => markViewed(file, checked === true)}
									/>
								</span>
							</HeaderActionTooltip>
						</div>
						{!collapsed ? (
							<div className={cn("min-w-0", !image && "p-3")}>
								{image ? (
									<ImageDiffView path={file.path} sessionId={sessionId} hostId={hostId} split={split} status={file.status} version={workspaceVersion ?? ""} />
								) : (
									<p className="text-caption text-muted-foreground">{file.binary ? t("files.binaryUnavailable") : deferred ? t("files.deferredDiff") : serverDeferredReason ? t("files.diffUnavailableReason", { reason: serverDeferredReason }) : pending ? t("files.loadingDiff") : t("files.diffUnavailable")}</p>
								)}
								{deferred ? <Button onClick={() => setLoadedDeferredPaths((current) => new Set(current).add(file.path))} size="sm" type="button" variant="outline">{t("files.loadDiff")}</Button> : null}
								{unavailable ? <RetryButton onClick={retryAll} /> : null}
							</div>
						) : null}
					</div>
				);
			})}
		</>
	);

	return {
		fileOpenContext,
		beginLineAnnotation,
		conflict,
		emptyMessage,
		fallbackFiles,
		firstError,
		groupError,
		loading,
		markViewed,
		renderFallbackFiles,
		retryAll,
		toggleCollapsed,
	};
}
