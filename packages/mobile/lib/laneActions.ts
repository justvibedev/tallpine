import type { OrchestratorLink } from "./api";
import { orchestratorState } from "./orchestratorView";

/** A backlog item ref such as "B12", normalised, or null when the text is not one. */
export function parseLaneRef(input: string): string | null {
	const match = /^b(\d+)$/i.exec(input.trim());
	return match ? `B${match[1]}` : null;
}

/** Only a lane orchestrator can be ended; the default one is the project's. */
export function canEndLane(link: OrchestratorLink | null | undefined): link is OrchestratorLink & { lane: string } {
	return !!link?.lane;
}

/**
 * A hand-off replaces a live orchestrator, so a stopped one has nothing to hand off.
 * An extra laneless orchestrator is excluded: a clean restart targets the default lane
 * and would retire the main orchestrator instead of the selected one.
 */
export function canHandoff(link: OrchestratorLink | null | undefined): link is OrchestratorLink {
	return orchestratorState(link) === "running" && !link?.extraLabel;
}

/** The End-lane confirmation text, mirroring the desktop `endLane.body`. */
export function endLaneBody(lane: string, workers: number): string {
	const moved = workers === 1 ? "1 live worker moves" : `${workers} live workers move`;
	return `This removes the lane orchestrator for good: its worktree, branch, artifacts and history are deleted. ${moved} to the default orchestrator and ${workers === 1 ? "is" : "are"} told. Backlog item ${lane} is left as it is.`;
}
