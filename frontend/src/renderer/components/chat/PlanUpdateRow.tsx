/**
 * One quiet line in the timeline for a turn that changed the session-wide plan.
 * The list itself lives in the inspector's Plan tab; this row only points at it.
 */

import { memo } from "react";
import { ListChecks } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { PlanUpdate } from "../../lib/session-plan";
import { Button } from "../ui/button";

export const PlanUpdateRow = memo(function PlanUpdateRow({
	update,
	onOpen,
}: {
	update: PlanUpdate;
	onOpen?: () => void;
}) {
	const { t } = useTranslation();
	return (
		<Button
			type="button"
			variant="ghost"
			size="sm"
			className="w-fit gap-1.5 px-1.5 text-muted-foreground"
			data-plan-update="true"
			disabled={!onOpen}
			onClick={onOpen}
		>
			<ListChecks aria-hidden="true" className="size-3.5" />
			{t("plan.updated", { done: update.done, total: update.total })}
		</Button>
	);
});
