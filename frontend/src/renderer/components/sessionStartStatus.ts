import type { BoardSessionStatusPresentation } from "@tallpine/product-ui";
import { agentLabel } from "../lib/agent-options";
import type { WorkspaceSession } from "../types/workspace";

/** Card status override while a session is still starting or failed to start. */
export function sessionStartStatus(
	session: Pick<WorkspaceSession, "provider" | "provisionState">,
): BoardSessionStatusPresentation | undefined {
	if (session.provisionState === "provisioning") {
		return {
			className: "text-status-working",
			indicatorClassName: "bg-status-working animate-status-pulse",
			label: `Starting ${agentLabel(session.provider)}…`,
			tone: "var(--color-status-working)",
		};
	}
	if (session.provisionState === "failed") {
		return {
			className: "text-status-exited",
			indicatorClassName: "bg-status-exited",
			label: "Start failed",
			tone: "var(--color-status-exited)",
		};
	}
	return undefined;
}
