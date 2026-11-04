import type { BacklogLink } from "./backlog-api";
import { sessionIsActive, type PullRequestFacts, type WorkspaceSession, type WorkspaceSummary } from "../types/workspace";

export type ResolvedBacklogLink = {
	link: BacklogLink;
	/** Present for a session link whose session the app knows. */
	session?: WorkspaceSession;
	/** Present for a PR link whose PR the app knows. */
	pr?: PullRequestFacts;
	/** The session that owns a known PR. */
	prSession?: WorkspaceSession;
};

/**
 * Joins an item's links with what the app already holds about sessions and PRs.
 * Links to anything else (issues, artifacts, memories, other items) carry no
 * live status and render from their display text; so do links whose target is
 * not in the workspace data (a deleted session, a PR no session reports).
 */
export function resolveBacklogLinks(links: readonly BacklogLink[], workspaces: readonly WorkspaceSummary[]): ResolvedBacklogLink[] {
	const sessions = workspaces.flatMap((workspace) => workspace.sessions);
	return links.map((link) => {
		if (link.kind === "session") return { link, session: sessions.find((session) => session.id === link.ref) };
		if (link.kind === "pr") {
			for (const session of sessions) {
				const pr = session.prs?.find((candidate) => candidate.url === link.ref);
				if (pr) return { link, pr, prSession: session };
			}
		}
		return { link };
	});
}

/** The still-running sessions linked to an item, newest link first: what the active badge opens. */
export function activeLinkedSessions(links: readonly BacklogLink[], workspaces: readonly WorkspaceSummary[]): WorkspaceSession[] {
	return resolveBacklogLinks([...links].sort((a, b) => Date.parse(b.createdAt) - Date.parse(a.createdAt)), workspaces).flatMap((resolved) =>
		resolved.session && sessionIsActive(resolved.session) ? [resolved.session] : [],
	);
}
