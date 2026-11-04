import {
	STANDALONE_WORKSPACE_ID,
	workerSessions,
	type WorkspaceSession,
	type WorkspaceSummary,
} from "../types/workspace";

export function isArchivedSession(session: WorkspaceSession): boolean {
	return (
		session.kanbanColumn === "archive" ||
		session.isTerminated === true ||
		session.status === "terminated"
	);
}

export function archivedStandaloneSessions(
	workspaces: readonly WorkspaceSummary[],
): WorkspaceSession[] {
	const standalone = workspaces.find((workspace) => workspace.id === STANDALONE_WORKSPACE_ID);
	return workerSessions(standalone?.sessions ?? [])
		.filter(isArchivedSession)
		.sort((left, right) => right.updatedAt.localeCompare(left.updatedAt));
}

/** How many archived standalone sessions the daemon holds, loaded or not. */
export function standaloneArchivedCount(workspaces: readonly WorkspaceSummary[]): number {
	const standalone = workspaces.find((workspace) => workspace.id === STANDALONE_WORKSPACE_ID);
	return Math.max(standalone?.archivedCount ?? 0, archivedStandaloneSessions(workspaces).length);
}
