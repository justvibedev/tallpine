import { useEffect, useMemo, useRef, useState } from "react";
import { useQueries, useQueryClient } from "@tanstack/react-query";
import { type CodeViewHandle } from "@pierre/diffs/react";
import { useTranslation } from "react-i18next";
import { sessionWorkspaceDiffsQueryOptions } from "../../../hooks/workspace-files/workspace-diff-queries";
import { WORKSPACE_REVIEW_BATCH_SIZE, WORKSPACE_REVIEW_INITIAL_BATCHES, WORKSPACE_REVIEW_BATCH_WAVE } from "../../../lib/workspace-review";
import { useUiStore } from "../../../stores/ui-store";
import { cancelFileAnnotations } from "../../workspace-diff/diff-annotation-types";
import { sessionUiKey } from "../../../lib/hosts";
import { REVIEW_CONTEXT_LINES } from "../trailingContext";
import { usePersistentGutterUtility } from "../usePersistentGutterUtility";
import type { WorkspaceReviewPaneProps } from "./review-pane-props";
import { useViewedFiles, readViewedRecords, viewedStorageKey, isDeferredByDefault, isViewedRecord, chunked } from "./review-helpers";

/** Collapse, viewed and selection state for the workspace review pane. */
export function useReviewPaneState(props: WorkspaceReviewPaneProps) {
	const {
		annotation,
		target,
		filter,
		onOpenFile: onOpenFileProp,
		sessionId,
		hostId,
	} = props;
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const resolvedTheme = useUiStore((state) => state.resolvedTheme);
	const [collapsedPaths, setCollapsedPaths] = useState<Set<string>>(() => new Set());
	const [loadedDeferredPaths, setLoadedDeferredPaths] = useState<Set<string>>(() => new Set());
	const [activeBatchCount, setActiveBatchCount] = useState(WORKSPACE_REVIEW_INITIAL_BATCHES);
	const reviewRef = useRef<HTMLDivElement>(null);
	const fallbackRows = useRef(new Map<string, HTMLDivElement>());
	const codeViewRef = useRef<CodeViewHandle<"feedback", undefined> | null>(null);
	const [pendingScrollPath, setPendingScrollPath] = useState<string | null>(null);
	const gutterHover = usePersistentGutterUtility(reviewRef);
	// Set when a file row's overflow menu hands off to another surface (an editor,
	// a preview, the feedback composer) so closing the menu doesn't pull focus
	// back to its trigger; dismissing it without a choice still restores focus.
	const fileMenuHandoffRef = useRef(false);

	const { scope, selectionKey, workspaceVersion } = target;
	// Reads and navigation share the selected source and revision.
	const onOpenFile = onOpenFileProp;
	const allFiles = useMemo(() => target.files ?? [], [target.files]);
	const filesKnown = target.files !== undefined;
	const reviewSelectionKey = target.key;
	const normalizedFilter = filter.trim().toLowerCase();
	const files = useMemo(
		() => (normalizedFilter ? allFiles.filter((file) => `${file.path} ${file.previousPath ?? ""}`.toLowerCase().includes(normalizedFilter)) : allFiles),
		[allFiles, normalizedFilter],
	);
	const viewedSessionKey = sessionUiKey(sessionId, hostId);
	const { viewed, toggle: toggleViewed } = useViewedFiles(viewedSessionKey, reviewSelectionKey, allFiles);

	// Feedback drafted against one review target doesn't apply to the next.
	const previousTargetKey = useRef(reviewSelectionKey);
	useEffect(() => {
		if (previousTargetKey.current === reviewSelectionKey) return;
		previousTargetKey.current = reviewSelectionKey;
		cancelFileAnnotations(annotation, (entry) => entry.surface === "review");
	}, [annotation, reviewSelectionKey]);

	// Reset the collapse / deferred / batch state only when the review target
	// itself changes (workspace version, target, its files arriving, session) — computed
	// from the unfiltered allFiles. Keying on the filtered `files` instead would
	// reset all of this on every file-filter keystroke, discarding the user's manual
	// expand/collapse, loaded deferred diffs, and progressive batch progress.
	useEffect(() => {
		const savedViewed = readViewedRecords(viewedStorageKey(viewedSessionKey, reviewSelectionKey));
		setCollapsedPaths(new Set(allFiles.filter((file) => isDeferredByDefault(file) || isViewedRecord(file, savedViewed)).map((file) => file.path)));
		setLoadedDeferredPaths(new Set());
		setActiveBatchCount(WORKSPACE_REVIEW_INITIAL_BATCHES);
		setPendingScrollPath(null);
		// eslint-disable-next-line react-hooks/exhaustive-deps -- reset on review-target identity, not on allFiles' reference (which changes on every poll) or the filtered files (which changes per keystroke).
	}, [workspaceVersion, reviewSelectionKey, filesKnown, viewedSessionKey]);

	// Opening a deferred file must not change the regular batches' query keys.
	const regularBatches = useMemo(() => chunked(files.filter((file) => !isDeferredByDefault(file)).map((file) => file.path), WORKSPACE_REVIEW_BATCH_SIZE), [files]);
	const batches = useMemo(() => [
		...regularBatches,
		...Array.from(loadedDeferredPaths).filter((path) => files.some((file) => file.path === path && isDeferredByDefault(file))).map((path) => [path]),
	], [files, loadedDeferredPaths, regularBatches]);
	const patchQueries = useQueries({
		queries: batches.map((paths, index) => ({
			...sessionWorkspaceDiffsQueryOptions({
				// endsAtLastHunk reads the trailing context, so request exactly what it assumes.
				contextLines: REVIEW_CONTEXT_LINES,
				errorMessage: t("files.error.loadWorkspace"),
				paths,
				scope,
				sessionId,
				hostId,
				workspaceVersion,
				commitShas: target.commitShas,
				view: target.view,
			}),
			enabled: target.state === "ready" && paths.length > 0 && (index < activeBatchCount || index >= regularBatches.length),
			staleTime: Infinity,
		})),
	});
	useEffect(() => {
		const active = patchQueries.slice(0, Math.min(activeBatchCount, regularBatches.length));
		if (active.some((query) => query.isPending || query.isFetching)) return;
		if (activeBatchCount < regularBatches.length) {
			setActiveBatchCount((current) => Math.min(current + WORKSPACE_REVIEW_BATCH_WAVE, regularBatches.length));
		}
	}, [activeBatchCount, regularBatches.length, patchQueries]);

	return {
		activeBatchCount,
		allFiles,
		batches,
		codeViewRef,
		collapsedPaths,
		fallbackRows,
		fileMenuHandoffRef,
		files,
		gutterHover,
		loadedDeferredPaths,
		onOpenFile,
		patchQueries,
		pendingScrollPath,
		queryClient,
		regularBatches,
		resolvedTheme,
		reviewRef,
		reviewSelectionKey,
		scope,
		selectionKey,
		setActiveBatchCount,
		setCollapsedPaths,
		setLoadedDeferredPaths,
		setPendingScrollPath,
		t,
		toggleViewed,
		viewed,
		viewedSessionKey,
		workspaceVersion,
	};
}
