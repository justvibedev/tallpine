import { BROWSER_BACKEND_CHROME } from "../../shared/chrome-mode";
import type { WorkspaceSession } from "../types/workspace";
import { useProjectSettings } from "./useProjectSettings";

/**
 * Whether the session's project runs agent browsing in Google Chrome (Real
 * Chrome mode, B849). Only local sessions: a remote host keeps its own agent
 * browser (a14).
 */
export function useProjectBrowsesInChrome(session: Pick<WorkspaceSession, "hostId" | "workspaceId">): boolean {
	const local = !session.hostId && Boolean(session.workspaceId);
	const { data } = useProjectSettings(session.workspaceId, { enabled: local });
	return local && data?.config?.browserBackend === BROWSER_BACKEND_CHROME;
}
