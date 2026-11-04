import { isOrchestratorSession, newestActiveOrchestrator, sessionIsActive, type WorkspaceSummary } from "../types/workspace";

/**
 * Where opening a project lands when the caller did not ask for the board: the
 * project's live orchestrator chat when no worker is live, otherwise undefined
 * (keep the board). Never spawns anything; no orchestrator also means board.
 */
export function projectLandingOrchestratorId(
	workspaces: readonly WorkspaceSummary[] | undefined,
	projectId: string,
): string | undefined {
	const workspace = workspaces?.find((candidate) => candidate.id === projectId);
	if (!workspace) return undefined;
	const hasLiveWorker = workspace.sessions.some((session) => !isOrchestratorSession(session) && sessionIsActive(session));
	if (hasLiveWorker) return undefined;
	return newestActiveOrchestrator(workspace.sessions)?.id;
}
