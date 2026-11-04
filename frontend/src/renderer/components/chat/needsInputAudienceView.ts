import type { TFunction } from "i18next";
import type { WorkspaceSession } from "../../types/workspace";

export interface NeedsInputView {
	/** Row label for a `needs_input` report. */
	label: string;
	/** Status text for a spawned-agent row. */
	status: string;
	/** Whether the human has to act: the only audience that earns the attention treatment. */
	attention: boolean;
}

/**
 * How a `needs_input` report reads, taken from the daemon's `needsInputAudience`
 * rather than re-derived. An unknown session keeps the attention treatment: a
 * missed wait costs more than a spurious one.
 */
export function needsInputView(session: Pick<WorkspaceSession, "needsInputAudience"> | undefined, t: TFunction): NeedsInputView {
	if (!session || session.needsInputAudience === "human") return { label: t("chat.workerNeedsYouLabel"), status: t("chat.workerNeedsInputStatus"), attention: true };
	if (session.needsInputAudience === "orchestrator") {
		return { label: t("chat.workerWaitingOnOrchestratorLabel"), status: t("chat.workerWaitingOnOrchestratorStatus"), attention: false };
	}
	// The orchestrator has since acted: the report is answered, not waiting.
	return { label: t("chat.workerAnsweredLabel"), status: t("chat.workerAnsweredStatus"), attention: false };
}
