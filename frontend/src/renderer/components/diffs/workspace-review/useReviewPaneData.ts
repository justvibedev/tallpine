import { useCallback, useEffect, useMemo, useRef } from "react";
import { useQueries } from "@tanstack/react-query";
import { type CodeViewItem, type FileDiffMetadata } from "@pierre/diffs";
import { fetchWorkspaceFileRevision, filesReviewEndOfFileQueryKey } from "../../../hooks/workspace-files/workspace-diff-queries";
import { fileAnnotationKey } from "../../workspace-diff/diff-annotation-types";
import { useAskInChat } from "../../../lib/chat-context-bus";
import { diffContentVersion, endsAtLastHunk, hydratedCopy, patchIdentity, releaseFileDiffs, retainFileDiffs, stableFileDiff } from "../trailingContext";
import { codeReference, useCodeSelection } from "../useCodeSelection";
import type { WorkspaceReviewPaneProps } from "./review-pane-props";
import { useReviewPaneState } from "./useReviewPaneState";
import { parseGroupPatch, END_OF_FILE_PREFETCH_MAX_BYTES, MAX_END_OF_FILE_PREFETCHES, prefetchFullContext, ANNOTATION_REVISION_SPAN } from "./review-helpers";

/** Patch, metadata and end-of-file queries for the workspace review pane. */
export function useReviewPaneData(props: WorkspaceReviewPaneProps, state: ReturnType<typeof useReviewPaneState>) {
	const {
		annotation,
		target,
		sessionId,
		hostId,
	} = props;
	const {
		batches,
		collapsedPaths,
		files,
		patchQueries,
		reviewRef,
		reviewSelectionKey,
		scope,
		selectionKey,
		t,
		viewedSessionKey,
		workspaceVersion,
	} = state;
	const { metadataByPath, endOfFilePaths } = useMemo(() => {
		const result = new Map<string, FileDiffMetadata>();
		const endOfFile = new Set<string>();
		for (const query of patchQueries) {
			for (const group of query.data?.groups ?? []) {
				try {
					for (const metadata of parseGroupPatch(query.data?.workspaceVersion, scope, selectionKey, group.repository, group.patch)) {
						result.set(metadata.name, metadata);
						// A truncated group can cut its last patch short, which would look
						// like that file ending early.
						if (!group.truncated && endsAtLastHunk(metadata)) endOfFile.add(metadata.name);
					}
				} catch {
					// The group retains its retry/error surface below; one malformed patch
					// must not prevent other repositories from rendering.
				}
			}
		}
		return { metadataByPath: result, endOfFilePaths: endOfFile };
	}, [patchQueries, scope, selectionKey]);
	const serverDeferredByPath = useMemo(() => {
		const result = new Map<string, string>();
		for (const query of patchQueries) {
			for (const group of query.data?.groups ?? []) {
				for (const deferred of group.deferred) result.set(deferred.path, deferred.reason);
			}
		}
		return result;
	}, [patchQueries]);
	// A batch still in flight (or still queued behind activeBatchCount) is the
	// only reason a requested file can legitimately have no patch yet. Once its
	// batch settles, a file with no diff is a failure the user can retry or step
	// around, not a load that will finish on its own.
	const pendingDiffPaths = useMemo(() => {
		const result = new Set<string>();
		batches.forEach((paths, index) => {
			const query = patchQueries[index];
			if (!query || query.isPending || query.isFetching) for (const path of paths) result.add(path);
		});
		return result;
	}, [batches, patchQueries]);

	const summaryById = useMemo(() => new Map(files.map((file) => [`${reviewSelectionKey}:${file.path}`, file])), [files, reviewSelectionKey]);
	// Highlighted code gets an "Ask in chat" button (or Cmd/Ctrl+L) while the
	// session has a Chat composer; each file's header carries its item id.
	const askInChat = useAskInChat(sessionId, hostId);
	const codeSelection = useCodeSelection(reviewRef, (selection) => {
		// Pierre portals the custom header into this host's light DOM; the selected
		// code is in its shadow root. Search this host to keep the file lookup local.
		const file = summaryById.get(selection.host.querySelector("[data-review-item-id]")?.getAttribute("data-review-item-id") ?? "");
		if (file) askInChat?.(codeReference(file.path, selection, true));
	}, askInChat !== undefined);

	const loadDiffFiles = useCallback(
		async (metadata: FileDiffMetadata, signal?: AbortSignal) => {
			// Pierre may hand this callback a normalized metadata object rather than
			// the exact object stored in our parse cache, so resolve by stable path.
			const file = files.find((candidate) => candidate.path === metadata.name);
			if (!file) throw new Error(t("files.error.loadFile"));
			const read = { commitShas: target.commitShas, view: target.view, sessionId, path: file.path, scope, workspaceVersion, hostId, signal };
			const [before, after] = await Promise.all([
				fetchWorkspaceFileRevision({ ...read, side: "before" }),
				fetchWorkspaceFileRevision({ ...read, side: "after" }),
			]);
			if (before.binary || after.binary || before.truncated || after.truncated) throw new Error(t("files.error.loadFile"));
			const newFile = { name: file.path, contents: after.content, cacheKey: after.revision };
			if (metadata.type === "rename-pure") return { oldFile: null, newFile };
			return { oldFile: { name: file.previousPath || file.path, contents: before.content, cacheKey: before.revision }, newFile };
		},
		[files, scope, sessionId, hostId, t, target.commitShas, target.view, workspaceVersion],
	);

	// Keyed by patch content (not workspace version), so a refresh that leaves a
	// file's diff unchanged reuses the contents instead of flashing the row back.
	const endOfFileFiles = useMemo(
		() => files.filter((file) => endOfFilePaths.has(file.path) && file.size <= END_OF_FILE_PREFETCH_MAX_BYTES).slice(0, MAX_END_OF_FILE_PREFETCHES),
		[endOfFilePaths, files],
	);
	const endOfFileContents = useQueries({
		queries: endOfFileFiles.map((file) => {
			const metadata = metadataByPath.get(file.path);
			return {
				queryKey: filesReviewEndOfFileQueryKey(sessionId, scope, selectionKey, file.path, file.fileFingerprint ?? "", metadata ? patchIdentity(metadata) : "", hostId),
				queryFn: ({ signal }) => {
					if (!metadata) throw new Error(t("files.error.loadFile"));
					return prefetchFullContext(viewedSessionKey, (activeSignal) => loadDiffFiles(metadata, activeSignal), signal);
				},
				enabled: metadata != null,
				retry: false,
				staleTime: Infinity,
			};
		}),
	});

	// CodeView re-renders an item whenever the item object changes, and Pierre
	// 1.4.1 throws if that render sees a different object with the same cache key.
	const publishedItemsRef = useRef(new Map<string, CodeViewItem<"feedback">>());
	const annotationRevisionsRef = useRef(new Map<string, { key: string; revision: number }>());

	const items = useMemo(
		() => {
			const itemId = (path: string) => `${reviewSelectionKey}:${path}`;
			const endOfFile = new Map<string, FileDiffMetadata>();
			endOfFileFiles.forEach((file, index) => {
				const metadata = metadataByPath.get(file.path);
				const query = endOfFileContents[index];
				const hydrated = metadata && query?.data ? hydratedCopy(metadata, query.data) : null;
				if (hydrated) endOfFile.set(file.path, hydrated);
			});
			return files.flatMap((file): CodeViewItem<"feedback">[] => {
				if (file.binary) return [];
				const metadata = metadataByPath.get(file.path);
				if (!metadata) return [];
				const fileDiff = stableFileDiff(endOfFile.get(file.path) ?? metadata);
				const collapsed = collapsedPaths.has(file.path);
				const fileTargets = annotation.targets.filter((target) => target.surface !== "focused" && target.path === file.path);
				const fileAnnotationActive = fileTargets.some((target) => target.side === "file");
				const activeTargets = fileTargets.filter((target) => target.side !== "file" && target.line != null);
				const id = itemId(file.path);
				// Each set of open boxes gets its own revision, so opening or closing
				// one among several still changes the item's version.
				const annotationKey = activeTargets.map(fileAnnotationKey).join("\n");
				const lastRevision = annotationRevisionsRef.current.get(id);
				const annotationRevision = lastRevision?.key === annotationKey ? lastRevision.revision : (lastRevision?.revision ?? 0) + 1;
				annotationRevisionsRef.current.set(id, { key: annotationKey, revision: annotationRevision });
				const version = (diffContentVersion(fileDiff) * 4 + (collapsed ? 1 : 0) + (fileAnnotationActive ? 2 : 0)) * ANNOTATION_REVISION_SPAN + (annotationRevision % ANNOTATION_REVISION_SPAN);
				const previous = publishedItemsRef.current.get(id);
				if (previous?.type === "diff" && activeTargets.length === 0 && !fileAnnotationActive && previous.annotations == null && previous.fileDiff === fileDiff && previous.version === version && previous.collapsed === collapsed) return [previous];
				const item: CodeViewItem<"feedback"> = {
					id,
					type: "diff",
					fileDiff,
					collapsed,
					annotations: activeTargets.length > 0 ? activeTargets.map((target) => ({
						lineNumber: target.line as number,
						side: target.side === "old" ? "deletions" : "additions",
						metadata: "feedback",
					})) : undefined,
					// CodeView only re-reads an item when its version changes: the content
					// part lets changed or newly hydrated diffs through, the low bits carry
					// the collapsed/annotation state.
					version,
				};
				publishedItemsRef.current.set(id, item);
				return [item];
			});
		},
		[annotation.targets, collapsedPaths, endOfFileContents, endOfFileFiles, files, metadataByPath, reviewSelectionKey],
	);
	// Pin exactly the diffs on screen in the shared diff cache: an older
	// workspace version's diffs are dropped once nothing renders them, and
	// closing the pane (or its session) releases the rest.
	const diffCacheOwner = useRef({}).current;
	const renderedDiffKeys = useMemo(
		() => items.flatMap((item) => (item.type === "diff" && item.fileDiff.cacheKey != null ? [item.fileDiff.cacheKey] : [])),
		[items],
	);
	useEffect(() => retainFileDiffs(diffCacheOwner, renderedDiffKeys), [diffCacheOwner, renderedDiffKeys]);
	useEffect(() => () => releaseFileDiffs(diffCacheOwner), [diffCacheOwner]);

	return {
		endOfFileContents,
		codeSelection,
		items,
		loadDiffFiles,
		metadataByPath,
		pendingDiffPaths,
		serverDeferredByPath,
		summaryById,
	};
}
