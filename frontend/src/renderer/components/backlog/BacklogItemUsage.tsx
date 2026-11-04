import { useTranslation } from "react-i18next";
import { useBacklogItemUsage } from "../../hooks/useBacklogItemUsage";
import { useBacklogRunEnabled } from "../../hooks/useBacklogRunEnabled";
import { BacklogRunUsage } from "./BacklogRunUsage";

/** Cost and elapsed time of an item's sessions, on its detail pane. Absent when run mode is off or no session was linked. */
export function BacklogItemUsage({ projectId, itemRef }: { projectId: string; itemRef: string }) {
	const { t } = useTranslation();
	const enabled = useBacklogRunEnabled();
	const usage = useBacklogItemUsage(projectId, itemRef, enabled).data;
	if (!enabled || !usage || (usage.costNanos == null && usage.elapsedSeconds == null)) return null;
	return (
		<div className="flex items-center justify-between gap-3">
			<span className="text-xs font-medium text-muted-foreground">{t("backlog.detail.usage")}</span>
			<BacklogRunUsage usage={usage} />
		</div>
	);
}
