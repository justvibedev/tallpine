/** Pressure state helpers and per-session memory facts. */
import { type PressureState, type ResourceSessionFacts } from "@tallpine/product-ui";
import { useWorkspaceQuery } from "../../hooks/useWorkspaceQuery";
import { useAppMemory, usePressureState, useSessionMemory, type SessionMemoryReading } from "../../hooks/useSessionMemory";
import { isOrchestratorSession, type WorkspaceSession, type WorkspaceSummary } from "../../types/workspace";

// Stable identity for the loading state: `data ?? []` would hand useMemo a
// fresh array every render, invalidating it (and the onRows effect below)
// in a loop until the workspace query resolves.
export const EMPTY_WORKSPACES: readonly WorkspaceSummary[] = [];

/** True once the daemon has produced an app-wide reading; gates the archive bar. */
export function useHasAppMemory(local = true): boolean {
	const memory = useAppMemory(local);
	return !memory.isError && (memory.data?.app?.rssBytes ?? 0) > 0;
}

/** What the monitor knows about a session, from the board plus its reading. */
export function toSessionFacts(session: WorkspaceSession, reading: SessionMemoryReading | undefined, now: number): ResourceSessionFacts {
	const lastActivity = session.activity?.lastActivityAt ? Date.parse(session.activity.lastActivityAt) : Number.NaN;
	return {
		id: session.id,
		title: session.title,
		rssBytes: reading?.rssBytes ?? 0,
		working: session.activity?.state === "active",
		idleSeconds: Number.isNaN(lastActivity) ? undefined : Math.max(0, (now - lastActivity) / 1000),
	};
}

export const stateDot: Record<PressureState, string> = {
	fine: "bg-success",
	tight_soon: "bg-warning",
	tight: "animate-status-pulse bg-destructive",
};
export const stateText: Record<PressureState, string> = {
	fine: "",
	tight_soon: "text-warning",
	tight: "text-destructive",
};

/** The pressure state and what each live session holds, for the row chips. */
export function usePressureFacts(projectId?: string) {
	const workspaces = useWorkspaceQuery().data ?? EMPTY_WORKSPACES;
	const readings = useSessionMemory().data;
	const now = Date.now();
	const facts = workspaces
		.filter((workspace) => !projectId || workspace.id === projectId)
		.flatMap((workspace) => workspace.sessions)
		.filter((session) => session.isTerminated !== true && !isOrchestratorSession(session) && readings?.has(session.id))
		.map((session) => toSessionFacts(session, readings?.get(session.id), now));
	const state = usePressureState();
	return { state, facts };
}
