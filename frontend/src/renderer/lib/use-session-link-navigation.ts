import { PRODUCT_NAME } from "../../shared/brand";
import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useSyncExternalStore } from "react";
import { fetchSessionView, remoteWorkspaceQueryKey, useWorkspaceQuery } from "../hooks/useWorkspaceQuery";
import { useUiStore } from "../stores/ui-store";
import { STANDALONE_WORKSPACE_ID, type WorkspaceSummary } from "../types/workspace";
import { LOCAL_HOST } from "./hosts";
import { useNavigateToSession } from "./navigate-to-session";
import { parseArtifactLink, parseSessionLink, resolveSessionLink } from "./session-links";

export type SessionLinkSource = {
	ready: boolean;
	isLoading: boolean;
	isError: boolean;
	workspaces: WorkspaceSummary[];
};

function useLocalLinkSource(_hostId: string): SessionLinkSource {
	const query = useWorkspaceQuery();
	return { ready: query.isSuccess, isLoading: query.isLoading, isError: query.isError, workspaces: query.data ?? [] };
}

function useRemoteLinkSource(hostId: string): SessionLinkSource {
	const queryClient = useQueryClient();
	const subscribe = useCallback((notify: () => void) => queryClient.getQueryCache().subscribe(notify), [queryClient]);
	const state = useSyncExternalStore(
		subscribe,
		() => queryClient.getQueryState<WorkspaceSummary[]>(remoteWorkspaceQueryKey(hostId)),
	);
	return {
		ready: state?.status === "success",
		isLoading: state === undefined || state.status === "pending",
		isError: state?.status === "error",
		workspaces: state?.data ?? [],
	};
}

export function useSessionLinkSource(sourceHostId?: string): SessionLinkSource {
	const remoteHostId = sourceHostId && sourceHostId !== LOCAL_HOST ? sourceHostId : undefined;
	const useSource = remoteHostId ? useRemoteLinkSource : useLocalLinkSource;
	return useSource(remoteHostId ?? "");
}

export function useSessionLinkNavigation(sourceHostId?: string): (url: string) => boolean {
	const remoteHostId = sourceHostId && sourceHostId !== LOCAL_HOST ? sourceHostId : undefined;
	const source = useSessionLinkSource(sourceHostId);
	const navigateToSession = useNavigateToSession();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const requestWorkspaceFileOpen = useUiStore((state) => state.requestWorkspaceFileOpen);
	return useCallback((url: string) => {
		const artifact = parseArtifactLink(url);
		const target = parseSessionLink(url) ?? artifact;
		if (!target) {
			showGlobalToast(`This ${PRODUCT_NAME} session link is malformed or unsupported.`, undefined, {
				tone: "error",
				placement: "top-center",
				dismissible: true,
				dedupeKey: "session-link:error",
			});
			return false;
		}
		if (!source.ready) {
			showGlobalToast(`${PRODUCT_NAME} could not verify that session. Check the ${remoteHostId ? "host" : "daemon"} connection and try again.`, undefined, {
				tone: "error",
				placement: "top-center",
				dismissible: true,
				dedupeKey: "session-link:error",
			});
			return false;
		}
		const missingToast = () =>
			showGlobalToast(`That session is missing or is not accessible in this ${PRODUCT_NAME} workspace.`, undefined, {
				tone: "error",
				placement: "top-center",
				dismissible: true,
				dedupeKey: "session-link:error",
			});
		const open = (resolved: { projectId: string; sessionId: string; isTerminated: boolean }) => {
			// A terminated session's artifacts are still served, so its artifact links still open.
			if (resolved.isTerminated && !artifact) {
				showGlobalToast(`Session ${resolved.sessionId} is terminated`, undefined, {
					placement: "top-center",
					dismissible: true,
					durationMs: 5_000,
					dedupeKey: `session-link:${remoteHostId ? `${remoteHostId}:` : ""}${resolved.projectId}:${resolved.sessionId}`,
				});
				return false;
			}
			if (artifact) requestWorkspaceFileOpen(resolved.sessionId, artifact.path, remoteHostId, "artifact");
			if (remoteHostId) navigateToSession(resolved.projectId, resolved.sessionId, remoteHostId);
			else navigateToSession(resolved.projectId, resolved.sessionId);
			return true;
		};
		const resolved = resolveSessionLink(url, source.workspaces);
		if (!resolved) {
			// The workspace list holds live sessions only; a terminated session is read by id.
			void fetchSessionView(remoteHostId, target.sessionId).then((view) => {
				const projectId = view?.projectId || STANDALONE_WORKSPACE_ID;
				if (!view || projectId !== target.projectId) return missingToast();
				open({ projectId, sessionId: view.id, isTerminated: view.isTerminated === true || view.status === "terminated" });
			}, missingToast);
			return true;
		}
		return open(resolved);
	}, [navigateToSession, remoteHostId, requestWorkspaceFileOpen, showGlobalToast, source]);
}
