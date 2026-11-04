/** Batched session patches applied to the workspace cache. */
import { type QueryClient } from "@tanstack/react-query";
import { apiErrorCode } from "../../lib/api-client";
import { applyOptimisticSessionKills } from "../optimistic-session-kills";
import { type WorkspaceSession, type WorkspaceSummary, STANDALONE_PROJECT_KIND, STANDALONE_WORKSPACE_ID } from "../../types/workspace";
import { SessionView, fetchSessionView } from "./wq-session-fetch";
import { standaloneWorkspaceName, placeStandaloneWorkspaceLast, toLocalWorkspaceSession, toWorkspaceSession } from "./session-mapping";
import { retainedSessions, retainKey } from "./retained-sessions";
import { workspaceQueryKey, sessionArtifactsQueryKey } from "./wq-keys";

/**
 * Put one freshly read session into the cached live list, the way a full refetch
 * would have placed it. Returns undefined when the session's project is not in
 * the cache (the caller refetches), and reports whether the session joined or
 * left the live list, which also moves the project's archive size.
 */
export function applySessionToWorkspaces(
	current: WorkspaceSummary[],
	view: SessionView,
	hostId?: string,
): { workspaces: WorkspaceSummary[]; membershipChanged: boolean } | undefined {
	const workspaceId = view.projectId || STANDALONE_WORKSPACE_ID;
	let base = current;
	let target = base.find((workspace) => workspace.id === workspaceId);
	if (!target) {
		if (workspaceId !== STANDALONE_WORKSPACE_ID) return undefined;
		target = {
			id: STANDALONE_WORKSPACE_ID,
			name: standaloneWorkspaceName(),
			kind: STANDALONE_PROJECT_KIND,
			path: "Not attached to a project",
			archivedCount: 0,
			sessions: [],
		};
		base = placeStandaloneWorkspaceLast([...base, target]);
	}
	const known = target.sessions.some((session) => session.id === view.id);
	const retained = retainedSessions.has(retainKey(view.id, hostId));
	const converted = workspaceId === STANDALONE_WORKSPACE_ID
		? toLocalWorkspaceSession(view, workspaceId, target.name)
		: toWorkspaceSession(view, target);
	const entry = hostId ? { ...converted, hostId } : converted;
	let sessions: WorkspaceSession[];
	let archivedCount = target.archivedCount;
	let membershipChanged = false;
	if (view.isTerminated && !retained) {
		sessions = target.sessions.filter((session) => session.id !== view.id);
		if (known) {
			membershipChanged = true;
			archivedCount = (archivedCount ?? 0) + 1;
		}
	} else if (known) {
		sessions = target.sessions.map((session) => (session.id === view.id ? entry : session));
	} else {
		sessions = [...target.sessions, entry];
		membershipChanged = !view.isTerminated;
	}
	const patched = { ...target, sessions, archivedCount };
	return { workspaces: base.map((workspace) => (workspace.id === workspaceId ? patched : workspace)), membershipChanged };
}

// Session reads that resolve within this window land in the cached list as one
// write. Every write re-renders each list subscriber (sidebar, board, shell), so
// under many busy sessions one write per read meant a commit per read, dozens
// per second.
export const SESSION_PATCH_APPLY_WINDOW_MS = 100;

export type SessionPatch = {
	sessionId: string;
	/** The fresh read, or undefined when the daemon no longer has the session. */
	view?: SessionView;
	resolve: (outcome: "patched" | "refetch") => void;
};
export const pendingSessionPatches = new WeakMap<QueryClient, { patches: SessionPatch[] }>();

export function applySessionPatches(client: QueryClient, patches: SessionPatch[]) {
	const outcomes = new Map<SessionPatch, "patched" | "refetch">();
	client.setQueryData<WorkspaceSummary[]>(workspaceQueryKey, (current) => {
		let next = current;
		for (const patch of patches) {
			if (!patch.view) {
				outcomes.set(patch, "refetch");
				next = next?.map((workspace) =>
					workspace.sessions.some((session) => session.id === patch.sessionId)
						? { ...workspace, sessions: workspace.sessions.filter((session) => session.id !== patch.sessionId) }
						: workspace,
				);
				continue;
			}
			if (!next) continue;
			const applied = applySessionToWorkspaces(next, patch.view);
			if (!applied) {
				outcomes.set(patch, "refetch");
				continue;
			}
			if (applied.membershipChanged) outcomes.set(patch, "refetch");
			next = applied.workspaces;
		}
		if (next === current) return current;
		return applyOptimisticSessionKills(next) ?? next;
	});
	for (const patch of patches) patch.resolve(outcomes.get(patch) ?? "patched");
}

export function queueSessionPatch(client: QueryClient, sessionId: string, view: SessionView | undefined): Promise<"patched" | "refetch"> {
	return new Promise((resolve) => {
		let pending = pendingSessionPatches.get(client);
		if (!pending) {
			const batch = { patches: [] as SessionPatch[] };
			pending = batch;
			pendingSessionPatches.set(client, batch);
			setTimeout(() => {
				pendingSessionPatches.delete(client);
				applySessionPatches(client, batch.patches);
			}, SESSION_PATCH_APPLY_WINDOW_MS);
		}
		pending.patches.push({ sessionId, view, resolve });
	});
}

/**
 * Read one session and patch it into the cached live list, instead of
 * refetching every session on each CDC event. Resolves to "refetch" when the
 * cache cannot take the patch alone (unknown project, a session that joined or
 * left the live list, or one the daemon no longer has). Patches read close
 * together are written to the cache together (SESSION_PATCH_APPLY_WINDOW_MS).
 */
export async function refreshWorkspaceSession(client: QueryClient, sessionId: string): Promise<"patched" | "refetch"> {
	let view: SessionView | undefined;
	try {
		view = await fetchSessionView(undefined, sessionId);
	} catch (error) {
		if (apiErrorCode(error) !== "SESSION_NOT_FOUND") throw error;
	}
	if (view && !view.isTerminated) client.setQueryData(sessionArtifactsQueryKey(view.id), view.artifactFiles ?? []);
	return queueSessionPatch(client, sessionId, view);
}
