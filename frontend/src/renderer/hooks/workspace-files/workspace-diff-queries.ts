/** Diff and file revision queries for workspace review. */
import type { UseQueryOptions } from "@tanstack/react-query";
import { apiErrorMessage } from "../../lib/api-client";
import { workspaceSelectionKey } from "../../lib/files-review-selection";
import { clientForSessionHost } from "../../lib/host-clients";
import { WorkspaceDiffScope, WorkspaceViewParams, WorkspaceDiffsResponse, WorkspaceFileRevision, FilesSource } from "./workspace-files-types";
import { workspaceRequestError, retryWorkspaceRead } from "./workspace-request-error";

export const sessionWorkspaceDiffsQueryKey = (
	sessionId: string,
	scope: WorkspaceDiffScope,
	paths: readonly string[],
	contextLines: number,
	ignoreWhitespace: boolean,
	workspaceVersion?: string,
	selectionKey?: string,
	hostId?: string,
) => hostId ? ["session-workspace-diffs", hostId, sessionId, scope, selectionKey ?? "", paths, contextLines, ignoreWhitespace, workspaceVersion ?? ""] as const
	: ["session-workspace-diffs", sessionId, scope, selectionKey ?? "", paths, contextLines, ignoreWhitespace, workspaceVersion ?? ""] as const;

/**
 * Patches for `paths`. `view` reads another branch or a PR; `commitShas` (or
 * the single `commitSha`) narrows the committed scope to those commits.
 */
export function sessionWorkspaceDiffsQueryOptions({
	contextLines = 3,
	errorMessage = "Unable to load workspace changes",
	ignoreWhitespace = false,
	paths,
	scope,
	sessionId,
	workspaceVersion,
	commitSha,
	commitShas,
	view,
	hostId,
}: {
	contextLines?: number;
	errorMessage?: string;
	ignoreWhitespace?: boolean;
	paths: readonly string[];
	scope: WorkspaceDiffScope;
	sessionId: string;
	workspaceVersion?: string;
	commitSha?: string;
	commitShas?: readonly string[];
	view?: WorkspaceViewParams;
	hostId?: string;
}) {
	return {
		queryKey: sessionWorkspaceDiffsQueryKey(sessionId, scope, paths, contextLines, ignoreWhitespace, workspaceVersion, selectionKeyFor(view, commitSha, commitShas), hostId),
		queryFn: async (): Promise<WorkspaceDiffsResponse> => {
			const { data, error, response } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/workspace/diffs", {
				params: { path: { sessionId } },
				body: { ...view, ...commitParams(commitSha, commitShas), contextLines, ignoreWhitespace, paths: [...paths], scope, workspaceVersion },
			});
			if (error) throw workspaceRequestError(error, response, errorMessage);
			if (!data) throw new Error(errorMessage);
			return data;
		},
		retry: retryWorkspaceRead,
	};
}

export async function fetchWorkspaceFileRevision({
	errorMessage = "Unable to load file revision",
	expectedRevision,
	path,
	scope,
	sessionId,
	side,
	workspaceVersion,
	commitSha,
	commitShas,
	view,
	hostId,
	signal,
}: {
	errorMessage?: string;
	expectedRevision?: string;
	path: string;
	scope: WorkspaceDiffScope;
	sessionId: string;
	side: "before" | "after";
	workspaceVersion?: string;
	commitSha?: string;
	commitShas?: readonly string[];
	view?: WorkspaceViewParams;
	hostId?: string;
	signal?: AbortSignal;
}): Promise<WorkspaceFileRevision> {
	const commits = commitParams(commitSha, commitShas);
	const { data, error, response } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/workspace/file/revision", {
		signal,
		params: {
			path: { sessionId },
			query: { path, scope, side, workspaceVersion, expectedRevision, ...view, commitSha: commits.commitSha, commitShas: commits.commitShas?.join(","), snapshot: scope === "committed" && (view !== undefined || commitShas !== undefined) ? true : undefined },
		},
	});
	if (error) throw workspaceRequestError(error, response, errorMessage);
	if (!data) throw new Error(errorMessage);
	return data;
}

export async function fetchPRFileRevision(sessionId: string, number: number, sourceUrl: string, path: string, side: "before" | "after", commitSha?: string, hostId?: string): Promise<WorkspaceFileRevision> {
	const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/pr/{prNumber}/file/revision", {
		params: { path: { sessionId, prNumber: number }, query: { path, side, sourceUrl, commitSha } },
	});
	if (error || !data) throw new Error(apiErrorMessage(error, "Unable to load pull request file revision"));
	return data as WorkspaceFileRevision;
}

export function sessionWorkspaceFileRevisionQueryOptions({
	path,
	scope,
	sessionId,
	side,
	workspaceVersion,
	commitSha,
	commitShas,
	view,
	hostId,
}: {
	path: string;
	scope: WorkspaceDiffScope;
	sessionId: string;
	side: "before" | "after";
	workspaceVersion?: string;
	commitSha?: string;
	commitShas?: readonly string[];
	view?: WorkspaceViewParams;
	hostId?: string;
}) {
	const selectionKey = selectionKeyFor(view, commitSha, commitShas);
	return {
		queryKey: hostId ? ["session-workspace-file-revision", hostId, sessionId, scope, selectionKey, side, path, workspaceVersion ?? ""] as const
			: ["session-workspace-file-revision", sessionId, scope, selectionKey, side, path, workspaceVersion ?? ""] as const,
		queryFn: () => fetchWorkspaceFileRevision({ sessionId, path, scope, side, workspaceVersion, commitSha, commitShas, view, hostId }),
	};
}

/** The full contents the review pane loads up front for a file whose patch ends on its last hunk. */
export const filesReviewEndOfFileQueryKey = (
	sessionId: string,
	scope: WorkspaceDiffScope,
	selectionKey: string,
	path: string,
	fileFingerprint: string,
	patchIdentity: string,
	hostId?: string,
) => hostId ? ["files-review-end-of-file", hostId, sessionId, scope, selectionKey, path, fileFingerprint, patchIdentity] as const
	: ["files-review-end-of-file", sessionId, scope, selectionKey, path, fileFingerprint, patchIdentity] as const;

export function sessionSourceFileRevisionQueryOptions({
	path,
	scope,
	sessionId,
	side,
	source,
	workspaceVersion,
	commitSha,
	hostId,
}: {
	path: string;
	scope: WorkspaceDiffScope;
	sessionId: string;
	side: "before" | "after";
	source: FilesSource;
	workspaceVersion?: string;
	commitSha?: string;
	hostId?: string;
}): UseQueryOptions<WorkspaceFileRevision> {
 if(source.kind === "snapshot") return sessionWorkspaceFileRevisionQueryOptions({path,scope:"committed",sessionId,side,view:source.view,commitShas:source.commitShas,hostId});
	if (source.kind === "workspace") return sessionWorkspaceFileRevisionQueryOptions({ path, scope, sessionId, side, workspaceVersion, commitSha, hostId });
	if (source.kind === "pull_request") {
		return {
			queryKey: hostId ? ["session-source-file-revision", hostId, sessionId, "pull_request", source.url, source.snapshot ?? "", commitSha ?? "", side, path] as const
				: ["session-source-file-revision", sessionId, "pull_request", source.url, source.snapshot ?? "", commitSha ?? "", side, path] as const,
			queryFn: () => fetchPRFileRevision(sessionId, source.number, source.url, path, side, commitSha, hostId),
		};
	}
	// Artifacts have no split before/after comparison — they're not diffed
	// against anything, just standalone output files. Callers gate this query
	// with `enabled: detail.deleted || detail.contentTruncated`, neither of
	// which an artifact ever sets, so the queryFn below never actually runs;
	// it still needs to type-check and exist, since options are constructed
	// unconditionally before `enabled` is evaluated.
	return {
		queryKey: ["session-source-file-revision", sessionId, "artifact", side, path] as const,
		queryFn: (): Promise<WorkspaceFileRevision> => {
			throw new Error("Artifact sources do not support file revisions");
		},
	};
}

// One SHA travels as commitSha, which every daemon reads; several need commitShas.
export function commitParams(commitSha: string | undefined, commitShas: readonly string[] | undefined): { commitSha?: string; commitShas?: string[] } {
	if (commitShas && commitShas.length > 1) return { commitShas: [...commitShas] };
	return { commitSha: commitShas?.[0] ?? commitSha };
}

export function selectionKeyFor(view: WorkspaceViewParams | undefined, commitSha: string | undefined, commitShas: readonly string[] | undefined): string {
	return workspaceSelectionKey(view, commitShas ?? (commitSha ? [commitSha] : []));
}
