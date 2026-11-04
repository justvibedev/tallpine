/** Retention of recently opened sessions across workspace refetches. */
import { replaceEqualDeep } from "@tanstack/react-query";
import { LOCAL_HOST } from "../../lib/hosts";
import { type WorkspaceSummary } from "../../types/workspace";

// A session opened by id that the live list does not hold (a terminated one,
// reached by URL, notification or link) is kept in the cache across list
// refetches, so shell chrome that looks the routed session up there still finds it.
// Only the most recently opened ones: over days of orchestrators spawning
// workers, an unbounded set re-merged hundreds of dead sessions on every write.
export const MAX_RETAINED_SESSIONS = 20;
export const retainedSessions = new Set<string>();
export const retainKey = (sessionId: string, hostId?: string) => `${hostId ?? LOCAL_HOST}\0${sessionId}`;

export function retainWorkspaceSession(sessionId: string, hostId?: string): void {
	const key = retainKey(sessionId, hostId);
	// Re-adding moves the key to the end: the Set iterates oldest first.
	retainedSessions.delete(key);
	retainedSessions.add(key);
	for (const oldest of retainedSessions) {
		if (retainedSessions.size <= MAX_RETAINED_SESSIONS) break;
		retainedSessions.delete(oldest);
	}
}

export function carryRetainedSessions(old: unknown, next: unknown): unknown {
	if (retainedSessions.size === 0 || !Array.isArray(old) || !Array.isArray(next)) return next;
	const present = new Set<string>();
	for (const workspace of next as WorkspaceSummary[]) {
		for (const session of workspace.sessions) present.add(retainKey(session.id, workspace.hostId));
	}
	let result = next as WorkspaceSummary[];
	for (const workspace of old as WorkspaceSummary[]) {
		const kept = workspace.sessions.filter((session) => {
			const key = retainKey(session.id, workspace.hostId);
			return retainedSessions.has(key) && !present.has(key);
		});
		if (kept.length === 0 || !result.some((candidate) => candidate.id === workspace.id)) continue;
		result = result.map((candidate) => (candidate.id === workspace.id ? { ...candidate, sessions: [...candidate.sessions, ...kept] } : candidate));
	}
	return result;
}

/** Keeps retained sessions, then shares unchanged structure with the previous data. */
export const retainingStructuralSharing = (old: unknown, next: unknown) => replaceEqualDeep(old, carryRetainedSessions(old, next));
