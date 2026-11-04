import { sessionWorkspaceFilesQueryKey } from "../useSessionWorkspaceFiles";
/** Connection state, changed-file count and default review prefetch for the workspace. */
import { hashKey, useQuery, useQueryClient } from "@tanstack/react-query";
import type { QueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useSyncExternalStore } from "react";
import { CHECKED_OUT_BRANCH, buildReviewTarget, defaultChanges, followsDefaultSelection, isLegacyManifest } from "../../lib/files-review-selection";
import { sessionUiKey } from "../../lib/hosts";
import { WORKSPACE_REVIEW_BATCH_SIZE } from "../../lib/workspace-review";
import { useUiStore } from "../../stores/ui-store";
import {
	getWorkspaceFileConnectionState,
	subscribeWorkspaceFileChanges,
	subscribeWorkspaceFileConnectionState,
	type WorkspaceFileConnectionState,
} from "../../lib/workspace-file-events";
import { historyKeyPrefix, branchesQueryKey, sessionWorkspaceFilesQueryOptions } from "./workspace-list-queries";
import { WorkspaceFileSummary, WorkspaceFilesResponse, ReviewTarget, FilesSource } from "./workspace-files-types";
import { sessionWorkspaceDiffsQueryKey, sessionWorkspaceDiffsQueryOptions, filesReviewEndOfFileQueryKey, fetchWorkspaceFileRevision } from "./workspace-diff-queries";

/** A moved tip or base: refetch the branch list and the views' histories, whose new tips key new reads. */
export function invalidateWorkspaceViews(queryClient: QueryClient, sessionId: string, hostId?: string) {
	const prefix = historyKeyPrefix(sessionId, hostId);
	void queryClient.invalidateQueries({ queryKey: branchesQueryKey(sessionId, hostId) });
	void queryClient.invalidateQueries({ queryKey: prefix, predicate: (query) => query.queryKey.length > prefix.length });
}

export function workspaceFilesRefetchInterval(state: WorkspaceFileConnectionState, degraded = false): false | number {
	return state === "degraded" || degraded ? WORKSPACE_FILES_DEGRADED_REFETCH_MS : false;
}

export function useWorkspaceFileConnectionState(sessionId: string, hostId?: string): WorkspaceFileConnectionState {
	const subscribe = useCallback(
		(listener: () => void) => subscribeWorkspaceFileConnectionState(sessionId, listener, hostId),
		[sessionId, hostId],
	);
	const getSnapshot = useCallback(() => getWorkspaceFileConnectionState(sessionId, hostId), [sessionId, hostId]);
	return useSyncExternalStore(subscribe, getSnapshot);
}

export function isChangedWorkspaceFile(file: WorkspaceFileSummary): boolean {
	return file.status !== "unmodified";
}

// Keep the lightweight summary query warm while the inspector is open. The
// Files view then mounts against current cache data instead of flashing a
// misleading zero while its first request starts. The same moment also
// preloads the default review's diffs, because that view is unmounted until
// the Files tab is selected.
export function useSessionWorkspaceFilesChangedCount(sessionId: string | undefined, hostId?: string): number | undefined {
	const queryClient = useQueryClient();
	const query = useQuery({
		...sessionWorkspaceFilesQueryOptions(sessionId ?? "", "Unable to load workspace files", hostId),
		enabled: Boolean(sessionId),
		// Live invalidations keep the inactive tab fresh; polling starts only
		// when the full Files view is visible.
		refetchInterval: false,
		select: (data: WorkspaceFilesResponse) => data.files.filter(isChangedWorkspaceFile).length,
	});
	useEffect(() => {
		if (!sessionId) return;
		return subscribeWorkspaceFileChanges(sessionId, queryClient, hostId);
	}, [queryClient, sessionId, hostId]);
	useEffect(() => {
		if (!sessionId || query.data === undefined) return;
		// A picked branch or set of changes is not what the default would warm.
		if (!followsDefaultSelection(useUiStore.getState().inspectorSessions[sessionUiKey(sessionId, hostId)]?.filesSelection)) return;
		const data = queryClient.getQueryData<WorkspaceFilesResponse>(sessionWorkspaceFilesQueryKey(sessionId, hostId));
		if (!data) return;
		void prefetchDefaultWorkspaceReviewDiffs(queryClient, sessionId, data, hostId).catch(() => {});
	}, [query.data, query.dataUpdatedAt, queryClient, sessionId, hostId]);
	return sessionId ? query.data : undefined;
}

// Must match WorkspaceReviewPane: the Files tab requests these batches, with
// this context size, and then full contents for files whose patch ends on the
// last hunk. A mismatch leaves the tab on "Loading diff…".
export const REVIEW_PREFETCH_BATCHES = 4;
export const REVIEW_PREFETCH_EOF_MAX = 40;
export const REVIEW_PREFETCH_EOF_MAX_BYTES = 128 * 1024;
export const lastPrefetchedReview = new Map<string, string>();

export function isDeferredReviewFile(file: WorkspaceFileSummary) {
	if (file.binary) return false;
	const name = file.path.split("/").pop()?.toLowerCase() ?? "";
	return file.size > 512 * 1024 || /^(package-lock\.json|pnpm-lock\.yaml|yarn\.lock|bun\.lockb?|go\.sum|cargo\.lock)$/.test(name);
}

/**
 * What the Files review opens on while nothing is picked: the uncommitted
 * changes, else the branch's commits (Changes, on an older daemon). Built
 * through the same helper as the pane's own target, so the keys match.
 */
export function defaultWorkspaceReviewTarget(data: WorkspaceFilesResponse): ReviewTarget {
	const legacy = isLegacyManifest(data);
	return buildReviewTarget({ branch: CHECKED_OUT_BRANCH, changes: defaultChanges(CHECKED_OUT_BRANCH, data, legacy), manifest: data, legacy });
}

/**
 * Every workspace change (an agent's edit) is a new version, and the prefetch
 * caches that version's whole review, patches plus full file contents, under new
 * keys for 30 minutes. With a busy agent and the inspector open that kept
 * hundreds of superseded reviews alive. Drop what an earlier run left for this
 * session and selection: patches of another version, and file contents this run
 * no longer wants. Anything a view still observes is kept.
 */
export function dropSupersededReviewPrefetches(
	queryClient: QueryClient,
	sessionId: string,
	target: Pick<ReviewTarget, "scope" | "selectionKey" | "workspaceVersion">,
	keptEndOfFile: ReadonlySet<string>,
	hostId?: string,
) {
	const owner = hostId ? [hostId, sessionId] : [sessionId];
	queryClient.removeQueries({
		queryKey: ["session-workspace-diffs", ...owner, target.scope, target.selectionKey],
		type: "inactive",
		predicate: (query) => query.queryKey[query.queryKey.length - 1] !== (target.workspaceVersion ?? ""),
	});
	queryClient.removeQueries({
		queryKey: ["files-review-end-of-file", ...owner, target.scope, target.selectionKey],
		type: "inactive",
		predicate: (query) => !keptEndOfFile.has(query.queryHash),
	});
}

export async function prefetchDefaultWorkspaceReviewDiffs(queryClient: QueryClient, sessionId: string, data: WorkspaceFilesResponse, hostId?: string) {
	// The Files tab count can be seeded from a files array alone. Prefetch only
	// runs against a full workspace response.
	if (!data.sections) return;
	const target = defaultWorkspaceReviewTarget(data);
	const files = (target.files ?? []).filter((file) => !isDeferredReviewFile(file)).slice(0, WORKSPACE_REVIEW_BATCH_SIZE * REVIEW_PREFETCH_BATCHES);
	if (files.length === 0) return;
	const token = `${target.workspaceVersion ?? ""}:${target.scope}:${target.selectionKey}:${files.map((file) => file.path).join("\n")}`;

	try {
		const { REVIEW_CONTEXT_LINES, endsAtLastHunk, patchIdentity } = await import("../../components/diffs/trailingContext");
		const { parsePatchFiles } = await import("@pierre/diffs");
		const batches: WorkspaceFileSummary[][] = [];
		for (let index = 0; index < files.length; index += WORKSPACE_REVIEW_BATCH_SIZE) batches.push(files.slice(index, index + WORKSPACE_REVIEW_BATCH_SIZE));
		const cacheKey = sessionUiKey(sessionId, hostId);
		// An existing entry can be invalidated by a file edit without its summary
		// version changing. Skip only if every batch is still present and fresh.
		if (lastPrefetchedReview.get(cacheKey) === token && batches.every((batch) => {
			const state = queryClient.getQueryState(sessionWorkspaceDiffsQueryKey(sessionId, target.scope, batch.map((file) => file.path), REVIEW_CONTEXT_LINES, false, target.workspaceVersion, target.selectionKey, hostId));
			return state?.data !== undefined && !state.isInvalidated;
		})) return;
		lastPrefetchedReview.set(cacheKey, token);
		const responses = await Promise.all(batches.map((batch) => queryClient.fetchQuery({
			...sessionWorkspaceDiffsQueryOptions({
				contextLines: REVIEW_CONTEXT_LINES,
				paths: batch.map((file) => file.path),
				scope: target.scope,
				sessionId,
				workspaceVersion: target.workspaceVersion,
				commitShas: target.commitShas,
				view: target.view,
				hostId,
			}),
			// A prefetch never retries, as fetchQuery's own default.
			retry: false,
			staleTime: Infinity,
			gcTime: 30 * 60 * 1000,
		})));

		const filesByPath = new Map(files.map((file) => [file.path, file]));
		const endOfFile: { file: WorkspaceFileSummary; identity: string }[] = [];
		const keptEndOfFile = new Set<string>();
		for (const response of responses) {
			for (const group of response.groups) {
				if (group.truncated || endOfFile.length >= REVIEW_PREFETCH_EOF_MAX) continue;
				const prefix = group.repository ? `${group.repository}/` : "";
				const parsed = parsePatchFiles(group.patch, `${target.workspaceVersion ?? ""}:${target.scope}:${group.patch.length}`, true).flatMap((entry) => entry.files);
				for (const metadata of parsed) {
					if (endOfFile.length >= REVIEW_PREFETCH_EOF_MAX) break;
					if (prefix && !metadata.name.startsWith(prefix)) metadata.name = prefix + metadata.name;
					if (prefix && metadata.prevName && !metadata.prevName.startsWith(prefix)) metadata.prevName = prefix + metadata.prevName;
					const file = filesByPath.get(metadata.name);
					if (!file || file.binary || file.size > REVIEW_PREFETCH_EOF_MAX_BYTES || !endsAtLastHunk(metadata)) continue;
					endOfFile.push({ file, identity: patchIdentity(metadata) });
				}
			}
		}

		await Promise.all(endOfFile.map(async ({ file, identity }) => {
			const queryKey = filesReviewEndOfFileQueryKey(sessionId, target.scope, target.selectionKey, file.path, file.fileFingerprint ?? "", identity, hostId);
			keptEndOfFile.add(hashKey(queryKey));
			const cached = queryClient.getQueryState(queryKey);
			if (cached?.data !== undefined && !cached.isInvalidated) return;
			try {
				const [before, after] = await Promise.all([
					fetchWorkspaceFileRevision({ commitShas: target.commitShas, view: target.view, sessionId, path: file.path, scope: target.scope, side: "before", workspaceVersion: target.workspaceVersion, hostId }),
					fetchWorkspaceFileRevision({ commitShas: target.commitShas, view: target.view, sessionId, path: file.path, scope: target.scope, side: "after", workspaceVersion: target.workspaceVersion, hostId }),
				]);
				if (before.binary || after.binary || before.truncated || after.truncated) return;
				const loaded = {
					oldFile: { name: file.previousPath || file.path, contents: before.content, cacheKey: before.revision },
					newFile: { name: file.path, contents: after.content, cacheKey: after.revision },
				};
				await queryClient.prefetchQuery({ queryKey, queryFn: () => loaded, retry: false, staleTime: Infinity, gcTime: 30 * 60 * 1000 });
			} catch {
				// Leave the cache empty so the review pane can fetch this file itself.
			}
		}));
		dropSupersededReviewPrefetches(queryClient, sessionId, target, keptEndOfFile, hostId);
	} catch {
		const cacheKey = sessionUiKey(sessionId, hostId);
		if (lastPrefetchedReview.get(cacheKey) === token) lastPrefetchedReview.delete(cacheKey);
	}
}

/** Shared by file, directory and passive-media reads: no live-worktree fallback. */
export function snapshotQuery(source: Extract<FilesSource, { kind: "snapshot" }>) {
	return { snapshot: true as const, ...source.view, commitShas: source.commitShas?.join(",") };
}

export function snapshotBlobUrl(base: string, sessionId: string, path: string, source: Extract<FilesSource, { kind: "snapshot" }>, side: "before" | "after" = "after") {
	const params = new URLSearchParams({ path, side });
	for (const [key, value] of Object.entries(snapshotQuery(source))) if (value !== undefined) params.set(key, String(value));
	return `${base.replace(/\/+$/, "")}/api/v1/sessions/${encodeURIComponent(sessionId)}/workspace/file/blob?${params}`;
}

export const WORKSPACE_FILES_DEGRADED_REFETCH_MS = 30_000;
