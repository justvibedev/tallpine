import { useTranslation } from "react-i18next";
import { useContextBreakdown } from "../../hooks/useContextBreakdown";
import { ContextBreakdown, type ResolvedCompaction } from "./ContextBreakdown";

/**
 * The breakdown inside the open context details: asked of the agent when the
 * details open, and absent when the agent cannot report one (the totals above
 * stay). Never shows a placeholder number while it waits.
 */
export function ContextBreakdownSection({ sessionId, hostId, resolved }: { sessionId: string; hostId?: string; resolved?: ResolvedCompaction }) {
	const { t } = useTranslation();
	const { breakdown, loading } = useContextBreakdown(sessionId, hostId);
	if (loading) return <p className="mt-3 border-t border-border pt-3 text-muted-foreground" role="status">{t("contextMeter.breakdown.loading")}</p>;
	return breakdown ? <ContextBreakdown data={breakdown} resolved={resolved} /> : null;
}
