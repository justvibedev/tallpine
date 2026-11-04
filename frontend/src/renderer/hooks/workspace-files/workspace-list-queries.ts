import { sessionWorkspaceFilesQueryKey } from "../useSessionWorkspaceFiles";
/** Search, file list, history and branch queries for the workspace. */
import { apiErrorMessage } from "../../lib/api-client";
import { CHECKED_OUT_BRANCH } from "../../lib/files-review-selection";
import { clientForSessionHost } from "../../lib/host-clients";
import { WorkspaceFileSearchResponse, FilesBranchTarget, WorkspaceViewParams } from "./workspace-files-types";
import { retryWorkspaceRead } from "./workspace-request-error";
import { WorkspaceBranches, WorkspaceFilesResponse, WorkspaceHistoryResponse, WorkspaceCommitSelection } from "./workspace-files-types";
import { workspaceRequestError } from "./workspace-request-error";

export function sessionWorkspaceSearchQueryOptions(
	sessionId: string,
	query: string,
	errorMessage = "Unable to search workspace files",
	hostId?: string,
	limit = 100,
) {
	return {
		queryKey: hostId ? ["session-workspace-search", hostId, sessionId, query, limit] as const : ["session-workspace-search", sessionId, query, limit] as const,
		queryFn: async ({ signal }: { signal?: AbortSignal } = {}): Promise<WorkspaceFileSearchResponse> => {
			const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/workspace/search", {
				params: { path: { sessionId }, query: { query, limit } },
				signal,
			});
			if (error) throw new Error(apiErrorMessage(error, errorMessage));
			if (!data) throw new Error(errorMessage);
			return data;
		},
	};
}

// Shared so SessionFileExplorer and SessionInspector resolve to the same cache
// entry while SSE invalidation remains the normal refresh path.
export function sessionWorkspaceFilesQueryOptions(sessionId: string, errorMessage = "Unable to load workspace files", hostId?: string) {
	return {
		queryKey: sessionWorkspaceFilesQueryKey(sessionId, hostId),
		queryFn: () => fetchSessionWorkspaceFiles(sessionId, errorMessage, hostId),
	};
}

export const historyKeyPrefix = (sessionId: string, hostId?: string) =>
	hostId ? ["session-workspace-history", hostId, sessionId] as const : ["session-workspace-history", sessionId] as const;
export const branchesQueryKey = (sessionId: string, hostId?: string) =>
	hostId ? ["session-workspace-branches", hostId, sessionId] as const : ["session-workspace-branches", sessionId] as const;

/**
 * The commits a branch lists. The checked-out branch keeps its original key;
 * another branch or a PR is keyed by the tip the branch list shows, so a moved
 * tip reads a new history.
 */
export function sessionWorkspaceHistoryQueryOptions(sessionId: string, errorMessage = "Unable to load workspace history", hostId?: string, branch: FilesBranchTarget = CHECKED_OUT_BRANCH) {
	const prefix = historyKeyPrefix(sessionId, hostId);
	return {
		queryKey: branch.kind === "branch" ? [...prefix, "branch", branch.name, branch.tipSha ?? ""] as const
			: branch.kind === "pull_request" ? [...prefix, "pr", branch.url, branch.tipSha ?? ""] as const
			: prefix,
		queryFn: () => fetchSessionWorkspaceHistory(sessionId, errorMessage, hostId, branch),
		staleTime: Infinity,
		retry: retryWorkspaceRead,
	};
}

/** The branches and PRs the review can read. Refetched when the branch menu opens, since nothing watches other branches. */
export function sessionWorkspaceBranchesQueryOptions(sessionId: string, errorMessage = "Unable to load branches", hostId?: string) {
	return {
		queryKey: branchesQueryKey(sessionId, hostId),
		queryFn: () => fetchSessionWorkspaceBranches(sessionId, errorMessage, hostId),
		staleTime: 15_000,
		retry: retryWorkspaceRead,
	};
}

/**
 * The files changed by ticked commits taken together, or by a view's whole
 * range. Its inputs are immutable SHAs (and the checked-out manifest version),
 * so a result never goes stale.
 */
export function sessionWorkspaceCommitSelectionQueryOptions({
	commitShas,
	errorMessage = "Unable to combine commits",
	hostId,
	selectionKey,
	sessionId,
	view,
	workspaceVersion,
}: {
	commitShas: readonly string[];
	errorMessage?: string;
	hostId?: string;
	selectionKey: string;
	sessionId: string;
	view?: WorkspaceViewParams;
	workspaceVersion?: string;
}) {
	return {
		queryKey: hostId ? ["session-workspace-commit-selection", hostId, sessionId, selectionKey, workspaceVersion ?? ""] as const
			: ["session-workspace-commit-selection", sessionId, selectionKey, workspaceVersion ?? ""] as const,
		queryFn: () => fetchWorkspaceCommitSelection({ commitShas, errorMessage, hostId, sessionId, view, workspaceVersion }),
		staleTime: Infinity,
		retry: retryWorkspaceRead,
	};
}

export async function fetchSessionWorkspaceBranches(sessionId: string, errorMessage: string, hostId?: string): Promise<WorkspaceBranches> {
	const { data, error, response } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/workspace/branches", {
		params: { path: { sessionId } },
	});
	if (error) throw workspaceRequestError(error, response, errorMessage);
	if (!data) throw new Error(errorMessage);
	return { ...data, branches: data.branches ?? [] };
}

export async function fetchSessionWorkspaceFiles(sessionId: string, errorMessage: string, hostId?: string): Promise<WorkspaceFilesResponse> {
	const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/workspace/manifest", {
		params: { path: { sessionId } },
	});
	if (error) throw new Error(apiErrorMessage(error, errorMessage));
	const response = (data ? { ...data, commits: [] } : {
		sessionId,
		files: [],
		truncated: false,
		sections: { staged: [], unstaged: [], untracked: [], committed: [] },
		commits: [],
		summary: { files: 0, additions: 0, deletions: 0 },
	}) as WorkspaceFilesResponse;
	return {
		...response,
		commits: (response.commits ?? []).map((commit) => ({ ...commit, files: commit.files ?? [] })),
		files: response.files ?? [],
		sections: response.sections ?? { staged: [], unstaged: [], untracked: [], committed: [] },
	};
}

export async function fetchSessionWorkspaceHistory(sessionId: string, errorMessage: string, hostId?: string, branch: FilesBranchTarget = CHECKED_OUT_BRANCH): Promise<WorkspaceHistoryResponse> {
	const query = branch.kind === "branch" ? { branch: branch.name }
		: branch.kind === "pull_request" ? { prNumber: branch.number, sourceUrl: branch.url || undefined }
		: undefined;
	const { data, error, response } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/workspace/history", {
		params: query ? { path: { sessionId }, query } : { path: { sessionId } },
	});
	if (error) throw workspaceRequestError(error, response, errorMessage);
	if (!data) throw new Error(errorMessage);
	return {
		...data,
		commits: (data.commits ?? []).map((commit) => ({ ...commit, files: commit.files ?? [] })),
	} as WorkspaceHistoryResponse;
}

export async function fetchWorkspaceCommitSelection({
	commitShas,
	errorMessage,
	hostId,
	sessionId,
	view,
	workspaceVersion,
}: {
	commitShas: readonly string[];
	errorMessage: string;
	hostId?: string;
	sessionId: string;
	view?: WorkspaceViewParams;
	workspaceVersion?: string;
}): Promise<WorkspaceCommitSelection> {
	const { data, error, response } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/workspace/history/selection", {
		params: { path: { sessionId } },
		body: { ...view, commitShas: [...commitShas], workspaceVersion },
	});
	if (error) throw workspaceRequestError(error, response, errorMessage);
	if (!data) throw new Error(errorMessage);
	return { ...data, files: data.files ?? [] } as WorkspaceCommitSelection;
}
