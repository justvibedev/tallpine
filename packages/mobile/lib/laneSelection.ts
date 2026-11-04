import type { OrchestratorLink } from "./api";
import { orchestratorState } from "./orchestratorView";

/** Which lane orchestrator a project is showing, keyed by `laneSelectionKey`. Values are orchestrator ids. */
export type LaneSelection = ReadonlyMap<string, string>;

export function laneSelectionKey(hostId: string, projectId: string): string {
	return `${hostId}\u0000${projectId}`;
}

export function laneLabel(link: Pick<OrchestratorLink, "lane" | "extraLabel">): string {
	return link.lane ? link.lane : link.extraLabel ?? "Default";
}

/**
 * The lane to show: the remembered one while it still exists, else the first
 * (default lane first, live first - see `orchestratorLinks`). A remembered
 * lane that was ended falls back rather than pointing at nothing.
 */
export function pickLane<T extends Pick<OrchestratorLink, "id">>(lanes: readonly T[], selectedId?: string | null): T | null {
	return lanes.find((lane) => lane.id === selectedId) ?? lanes[0] ?? null;
}

/** The one-project selection map `orchestratorProjectSections` takes, from the stored per-machine one. */
export function laneSelectedFor(selection: LaneSelection, hostId: string, projectId: string): Map<string, string> {
	const id = selection.get(laneSelectionKey(hostId, projectId));
	return id ? new Map([[projectId, id]]) : new Map();
}

export type LaneSpawnTarget =
	| { kind: "default" }
	| { kind: "lane"; id: string }
	| { kind: "stopped"; id: string; lane: string };

/**
 * Where a new worker goes. A running lane orchestrator becomes the worker's
 * parent (the daemon gives the child that lane). The default lane keeps the
 * project-level delegate route. A stopped lane is its own answer: the caller
 * must ask the user, never quietly fall back to the default orchestrator.
 */
export function laneSpawnTarget(orchestrators: readonly OrchestratorLink[], projectId: string, selectedId?: string | null): LaneSpawnTarget {
	const link = orchestrators.find((o) => o.projectId === projectId && o.id === selectedId);
	// An extra laneless orchestrator is its own parent target, not the default lane.
	if (link?.extraLabel && orchestratorState(link) === "running") return { kind: "lane", id: link.id };
	if (!link?.lane) return { kind: "default" };
	return orchestratorState(link) === "running" ? { kind: "lane", id: link.id } : { kind: "stopped", id: link.id, lane: link.lane };
}

/** Thrown by spawn when the selected lane's orchestrator is stopped. */
export class LaneStoppedError extends Error {
	constructor(
		readonly hostId: string,
		readonly projectId: string,
		readonly lane: string,
	) {
		super(`Lane ${lane} is stopped.`);
		this.name = "LaneStoppedError";
	}
}

/**
 * The spawn target with the caller's explicit choices applied: a lane parent
 * the caller already holds (the orchestrator it just started) wins over the
 * stored selection, which may still name the stopped predecessor; "use default"
 * skips the lane entirely.
 */
export function resolveSpawnTarget(
	orchestrators: readonly OrchestratorLink[],
	projectId: string,
	selectedId: string | null | undefined,
	choice: { useDefaultLane?: boolean; laneParentId?: string } = {},
): LaneSpawnTarget {
	if (choice.laneParentId) return { kind: "lane", id: choice.laneParentId };
	if (choice.useDefaultLane) return { kind: "default" };
	return laneSpawnTarget(orchestrators, projectId, selectedId);
}
