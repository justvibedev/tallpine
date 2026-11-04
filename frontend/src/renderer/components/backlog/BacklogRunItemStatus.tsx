import type { MessageKey } from "../../i18n/messages";
import { CircleAlert, CircleDashed, CircleX, GitMerge, GitPullRequest, Loader2, CirclePause } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { BacklogRunItem } from "../../lib/backlog-run-api";
import { Spinner } from "../ui/spinner";

const ICON_CLASS = "size-3.5 shrink-0";

function glyph(status: BacklogRunItem["displayStatus"]) {
	switch (status) {
		case "active":
			return <Spinner icon={Loader2} className={`${ICON_CLASS} text-status-working`} />;
		case "in_review":
			return <GitPullRequest aria-hidden="true" className={`${ICON_CLASS} text-status-in-review`} />;
		case "needs_you":
			return <CircleAlert aria-hidden="true" className={`${ICON_CLASS} text-status-needs-you`} />;
		case "landed":
			return <GitMerge aria-hidden="true" className={`${ICON_CLASS} text-status-merged`} />;
		case "held":
			return <CirclePause aria-hidden="true" className={`${ICON_CLASS} text-muted-foreground`} />;
		case "failed":
			return <CircleX aria-hidden="true" className={`${ICON_CLASS} text-destructive`} />;
		default:
			return <CircleDashed aria-hidden="true" className={`${ICON_CLASS} text-muted-foreground`} />;
	}
}

/** One planned item's status: a glyph in a fixed slot plus its word (DESIGN section 5: the glyph is never the only signal). */
export function BacklogRunItemStatus({ status }: { status: BacklogRunItem["displayStatus"] }) {
	const { t } = useTranslation();
	return (
		<span className="inline-flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground">
			{glyph(status)}
			{t(`backlog.run.item.${status}` as MessageKey)}
		</span>
	);
}
