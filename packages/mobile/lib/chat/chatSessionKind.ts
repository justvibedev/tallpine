import type { DashboardSession, OrchestratorLink } from "../api";

/**
 * Whether a chat screen's session is an orchestrator. The board lists
 * orchestrators as OrchestratorLink rows, which carry no `kind`; a session the
 * route looked up directly is a DashboardSession that names its kind. Checking
 * `kind` alone missed every listed orchestrator, so its chat lost the plan
 * cards and called itself a worker (B681, B552).
 */
export function isOrchestratorChat(session: DashboardSession | OrchestratorLink): boolean {
	return "projectName" in session || session.kind === "orchestrator";
}
