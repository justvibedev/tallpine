import { useState } from "react";
import { Play } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useBacklogRunEnabled } from "../../hooks/useBacklogRunEnabled";
import { useLatestBacklogRun } from "../../hooks/useLatestBacklogRun";
import type { BacklogRunSelection } from "../../lib/backlog-run-api";
import { BacklogReasonButton } from "./BacklogReasonButton";
import { BacklogRunDialog } from "./BacklogRunDialog";

type BacklogRunButtonProps = {
	projectId: string;
	selection: BacklogRunSelection;
	/** False when the project has no running orchestrator to plan the run. */
	hasOrchestrator: boolean;
	variant?: "secondary" | "ghost";
};

/** Opens the run dialog. Absent when the daemon has run mode off; disabled, with the reason, while a run is open or nobody can plan it. */
export function BacklogRunButton({ projectId, selection, hasOrchestrator, variant = "secondary" }: BacklogRunButtonProps) {
	const { t } = useTranslation();
	const enabled = useBacklogRunEnabled();
	const latest = useLatestBacklogRun(projectId, enabled).data;
	const [open, setOpen] = useState(false);
	if (!enabled) return null;
	const runOpen = latest !== undefined && latest.status !== "done";
	const reason = runOpen ? t("backlog.run.reason.open", { ref: latest.ref }) : hasOrchestrator ? undefined : t("backlog.reason.noOrchestrator");
	return (
		<>
			<BacklogReasonButton type="button" size="sm" variant={variant} disabledReason={reason} onClick={() => setOpen(true)}>
				<Play aria-hidden="true" className="size-3.5" />
				{t("backlog.run.button")}
			</BacklogReasonButton>
			{open ? <BacklogRunDialog open={open} projectId={projectId} selection={selection} onOpenChange={setOpen} /> : null}
		</>
	);
}
