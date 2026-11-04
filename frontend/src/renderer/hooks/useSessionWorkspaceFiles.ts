import type { UseQueryOptions } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiErrorMessage } from "../lib/api-client";
import { clientForSessionHost } from "../lib/host-clients";
import { fileMediaKind } from "../lib/file-media";
import { artifactContentUrl } from "../lib/artifact-content-url";
import { WorkspaceDiffScope, WorkspaceFileDetail, FilesSource } from "./workspace-files/workspace-files-types";
import { workspaceRequestError, WorkspaceRequestError, retryWorkspaceRead } from "./workspace-files/workspace-request-error";
import { snapshotQuery } from "./workspace-files/workspace-review-prefetch";

// Public façade: names imported through this hub by FileTree, the explorer
// tests and the workspace-review e2e harness.
export type { FilesSource, ReviewTarget, WorkspaceBranches, WorkspaceCommitSummary, WorkspaceFilesResponse, WorkspaceHistoryResponse } from "./workspace-files/workspace-files-types";
export { sessionWorkspaceSearchQueryOptions } from "./workspace-files/workspace-list-queries";
export { sessionWorkspaceDiffsQueryOptions } from "./workspace-files/workspace-diff-queries";
export { defaultWorkspaceReviewTarget } from "./workspace-files/workspace-review-prefetch";

export const sessionWorkspaceFilesQueryKey = (sessionId: string, hostId?: string) =>
	hostId ? ["session-workspace-files", hostId, sessionId] as const : ["session-workspace-files", sessionId] as const;
const MAX_ARTIFACT_TEXT_BYTES = 256 * 1024;

export const sessionWorkspaceFileQueryKey = (sessionId: string, path: string, scope: WorkspaceDiffScope = "combined", commitSha?: string, hostId?: string) =>
	hostId ? ["session-workspace-file", hostId, sessionId, scope, commitSha ?? "", path] as const
		: ["session-workspace-file", sessionId, scope, commitSha ?? "", path] as const;

async function fetchSessionWorkspaceFile(sessionId: string, path: string, scope: WorkspaceDiffScope, errorMessage: string, commitSha?: string, hostId?: string): Promise<WorkspaceFileDetail> {
	const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/workspace/file", {
		params: { path: { sessionId }, query: { path, section: scope === "combined" ? undefined : scope, commitSha } },
	});
	if (error) throw new Error(apiErrorMessage(error, errorMessage));
	if (!data) throw new Error(errorMessage);
	return data as WorkspaceFileDetail;
}

export type WorkspacePathResolution = components["schemas"]["WorkspacePathResolutionResponse"];

/**
 * Ask the daemon which session root owns a path. Resolves undefined when the
 * daemon cannot say (network failure, or a remote host running a build without
 * this route), so a caller always has a defined fallback.
 */
export async function resolveSessionWorkspacePath(sessionId: string, path: string, hostId?: string): Promise<WorkspacePathResolution | undefined> {
	try {
		const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/workspace/resolve", {
			params: { path: { sessionId }, query: { path } },
		});
		return error ? undefined : data;
	} catch {
		return undefined;
	}
}

async function fetchSessionPRFile(sessionId: string, number: number, sourceUrl: string, path: string, previousPath: string, errorMessage: string, commitSha?: string, hostId?: string): Promise<WorkspaceFileDetail> {
	const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/pr/{prNumber}/file", {
		params: { path: { sessionId, prNumber: number }, query: { path, previousPath, sourceUrl, commitSha } },
	});
	if (error) throw new Error(apiErrorMessage(error, errorMessage));
	if (!data) throw new Error(errorMessage);
	return data as WorkspaceFileDetail;
}

// Artifact files live in the session's artifact directory, outside the git
// workspace, so they have no diff/status and aren't reachable through the
// workspace-files endpoint. Local preview hostnames can resolve to ::1, where
// another app may own the daemon's port. Read through the trusted daemon API
// instead, explicitly selecting the artifact root. Remote proxy URLs
// already identify their content endpoint and must keep their original routing.
async function fetchSessionArtifactFile(sessionId: string, path: string, rawUrl: string | undefined, errorMessage: string): Promise<WorkspaceFileDetail> {
	if (!rawUrl) throw new Error(errorMessage);
	const contentUrl = artifactContentUrl(sessionId, path, rawUrl);
	if (!contentUrl) throw new Error(errorMessage);
	const media = fileMediaKind(path);
	// The artifact route only supports GET; media elements stream their own body.
	const response = await fetch(contentUrl);
	if (media) void response.body?.cancel();
	if (!response.ok) throw new Error(errorMessage);
	const { binary, bytes, content, size, truncated } = media
		? { binary: true, bytes: new Uint8Array(), content: "", size: Number(response.headers.get("content-length")) || 0, truncated: false }
		: await readArtifactTextResponse(response);
	// Only a complete local text file can be edited: the save sends it back whole.
	const editable = !binary && !truncated;
	return {
		additions: 0,
		binary,
		content: binary ? "" : content,
		contentTruncated: truncated,
		deleted: false,
		deletions: 0,
		diff: "",
		diffTruncated: false,
		editable,
		fileFingerprint: editable ? await artifactFingerprint(bytes) : "",
		imageMediaType: media === "image" ? response.headers.get("content-type") ?? "image" : undefined,
		mediaVersion: media ? response.headers.get("etag") ?? response.headers.get("last-modified") ?? undefined : undefined,
		path,
		sessionId,
		size,
		status: "unmodified",
		workspaceVersion: "",
	};
}

async function readArtifactTextResponse(response: Response): Promise<{ binary: boolean; bytes: Uint8Array; content: string; size: number; truncated: boolean }> {
	const headerSize = Number.parseInt(response.headers.get("content-length") ?? "", 10);
	if (!response.body && Number.isFinite(headerSize) && headerSize > MAX_ARTIFACT_TEXT_BYTES) {
		return { binary: false, bytes: new Uint8Array(), content: "", size: headerSize, truncated: true };
	}
	const { bytes, size, truncated } = await readBoundedResponseBytes(response, MAX_ARTIFACT_TEXT_BYTES);
	const decoded = decodeArtifactText(bytes, truncated);
	return { ...decoded, bytes, size, truncated };
}

/** The hex SHA-256 of an artifact's bytes, the fingerprint its write endpoint checks. */
async function artifactFingerprint(bytes: Uint8Array): Promise<string> {
	const digest = await crypto.subtle.digest("SHA-256", bytes as Uint8Array<ArrayBuffer>);
	return Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, "0")).join("");
}

async function readBoundedResponseBytes(response: Response, limit: number): Promise<{ bytes: Uint8Array; size: number; truncated: boolean }> {
	if (!response.body) {
		const buffer = await response.arrayBuffer();
		const bytes = new Uint8Array(buffer);
		return { bytes: bytes.slice(0, limit), size: bytes.byteLength, truncated: bytes.byteLength > limit };
	}

	const reader = response.body.getReader();
	const chunks: Uint8Array[] = [];
	let total = 0;
	let truncated = false;
	while (true) {
		const { done, value } = await reader.read();
		if (done) break;
		const remaining = limit - total;
		if (value.byteLength > remaining) {
			if (remaining > 0) chunks.push(value.slice(0, remaining));
			total += value.byteLength;
			truncated = true;
			await reader.cancel();
			break;
		}
		chunks.push(value);
		total += value.byteLength;
	}

	const kept = chunks.reduce((sum, chunk) => sum + chunk.byteLength, 0);
	const bytes = new Uint8Array(kept);
	let offset = 0;
	for (const chunk of chunks) {
		bytes.set(chunk, offset);
		offset += chunk.byteLength;
	}
	return { bytes, size: total, truncated };
}

function decodeArtifactText(bytes: Uint8Array, truncated: boolean): { binary: boolean; content: string } {
	if (bytes.includes(0)) return { binary: true, content: "" };
	try {
		return { binary: false, content: new TextDecoder("utf-8", { fatal: true }).decode(bytes) };
	} catch {
		if (truncated) {
			for (let trim = 1; trim < 4 && trim < bytes.byteLength; trim += 1) {
				try {
					return { binary: false, content: new TextDecoder("utf-8", { fatal: true }).decode(bytes.slice(0, bytes.byteLength - trim)) };
				} catch {
					// Invalid UTF-8 away from the bounded suffix is handled below as binary.
				}
			}
		}
		return { binary: true, content: "" };
	}
}

export function sessionArtifactFileQueryOptions(sessionId: string, path: string, rawUrl: string | undefined, errorMessage = "Unable to load artifact"): UseQueryOptions<WorkspaceFileDetail> {
	return {
		queryKey: ["session-artifact-file", rawUrl ?? "", sessionId, path],
		queryFn: () => fetchSessionArtifactFile(sessionId, path, rawUrl, errorMessage),
	};
}

// Shared so the diff view (expand-on-demand) and the plain read-only viewer
// always resolve to the same cache entry for a given (session, path).
export function sessionWorkspaceFileQueryOptions(sessionId: string, path: string, errorMessage = "Unable to load workspace file", scope: WorkspaceDiffScope = "combined", commitSha?: string, hostId?: string) {
	return {
		queryKey: sessionWorkspaceFileQueryKey(sessionId, path, scope, commitSha, hostId),
		queryFn: () => fetchSessionWorkspaceFile(sessionId, path, scope, errorMessage, commitSha, hostId),
		staleTime: 5_000,
	};
}

export function sessionSourceFileQueryOptions(
	sessionId: string,
	source: FilesSource,
	path: string,
	errorMessage = "Unable to load file",
	scope: WorkspaceDiffScope = "combined",
	commitSha?: string,
	previousPath = "",
	hostId?: string,
): UseQueryOptions<WorkspaceFileDetail> {
	if (source.kind === "snapshot")
		return {
			queryKey: ["session-snapshot-file", hostId ?? "local", sessionId, JSON.stringify(source), path],
			queryFn: async ({ signal }) => {
				const { data, error, response } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/workspace/file", {
					signal,
					params: { path: { sessionId }, query: { path, ...snapshotQuery(source) } },
				});
				if (error) throw workspaceRequestError(error, response, errorMessage);
				if (!data) throw new Error(errorMessage);
    if (!data.historical) throw new WorkspaceRequestError("This host does not support snapshot file browsing. Update its daemon to open this revision.","WORKSPACE_SNAPSHOT_UNSUPPORTED",400);
				return data as WorkspaceFileDetail;
			},
			retry: retryWorkspaceRead,
			staleTime: Infinity,
		};
	if (source.kind === "workspace") return sessionWorkspaceFileQueryOptions(sessionId, path, errorMessage, scope, commitSha, hostId);
	if (source.kind === "artifact") return sessionArtifactFileQueryOptions(sessionId, path, source.rawUrl, errorMessage);
	return {
		queryKey: hostId
			? ["session-source-file", hostId, sessionId, "pull_request", source.url, source.snapshot ?? "", commitSha ?? "", path]
			: ["session-source-file", sessionId, "pull_request", source.url, source.snapshot ?? "", commitSha ?? "", path],
		queryFn: () => fetchSessionPRFile(sessionId, source.number, source.url, path, previousPath, errorMessage, commitSha, hostId),
	};
}

/** `selectionKey` names the view and commits read (workspaceSelectionKey); "" is the worktree's own scope or range. */

export async function updateSessionWorkspaceFile({
	content,
	expectedFileFingerprint,
	path,
	sessionId,
	hostId,
}: {
	content: string;
	expectedFileFingerprint: string;
	path: string;
	sessionId: string;
	hostId?: string;
}): Promise<WorkspaceFileDetail> {
	const { data, error } = await clientForSessionHost(hostId).PUT("/api/v1/sessions/{sessionId}/workspace/file", {
		params: { path: { sessionId } },
		body: { content, expectedFileFingerprint, path },
	});
	if (error) throw new Error(apiErrorMessage(error, "Unable to save workspace file"));
	if (!data) throw new Error("Unable to save workspace file");
	return data as WorkspaceFileDetail;
}

/**
 * Replaces the content of one listed artifact file, provided it still holds
 * the content the edit started from, and returns its new fingerprint.
 */
export async function updateSessionArtifactFile({
	content,
	expectedFileFingerprint,
	path,
	sessionId,
	hostId,
}: {
	content: string;
	expectedFileFingerprint: string;
	path: string;
	sessionId: string;
	hostId?: string;
}): Promise<components["schemas"]["ArtifactFileResponse"]> {
	const { data, error } = await clientForSessionHost(hostId).PUT("/api/v1/sessions/{sessionId}/artifact-files", {
		params: { path: { sessionId } },
		body: { content, expectedFileFingerprint, path },
	});
	if (error) throw new Error(apiErrorMessage(error, "Unable to save artifact"));
	if (!data) throw new Error("Unable to save artifact");
	return data;
}

/** Deletes one listed file from the session's artifact directory. */
export async function deleteSessionArtifactFile({ path, sessionId, hostId }: { path: string; sessionId: string; hostId?: string }): Promise<void> {
	const { error } = await clientForSessionHost(hostId).DELETE("/api/v1/sessions/{sessionId}/artifact-files", {
		params: { path: { sessionId }, query: { path } },
	});
	if (error) throw new Error(apiErrorMessage(error, "Unable to delete artifact"));
}
