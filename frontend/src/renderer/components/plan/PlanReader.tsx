import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { PlanView } from "../../lib/plan-api";
import { ChatMarkdown } from "../chat/ChatMarkdown";

/**
 * Read-only view of a submitted plan: the decisions table first, then the PR
 * split, with the full plan one click away. The plan text is not editable here.
 */
export function PlanReader({ plan }: { plan: PlanView }) {
	const { t } = useTranslation();
	const [full, setFull] = useState(false);
	return (
		<div className="min-w-0 space-y-3 px-3 pb-2 text-xs leading-relaxed" data-testid="plan-reader">
			{plan.decisionsMarkdown ? (
				<section aria-label={t("planCard.decisions")} className="space-y-1">
					<h4 className="font-medium text-muted-foreground">{t("planCard.decisions")}</h4>
					<ChatMarkdown text={plan.decisionsMarkdown} />
				</section>
			) : null}
			{plan.prSplit.length > 0 ? (
				<section aria-label={t("planCard.prSplit")} className="space-y-1">
					<h4 className="font-medium text-muted-foreground">{t("planCard.prSplit")}</h4>
					<ol className="list-decimal space-y-1 pl-5">
						{plan.prSplit.map((pr, index) => (
							<li key={`${index}-${pr.title}`}>
								<span className="font-medium text-foreground">{pr.title}</span>
								{pr.summary ? <span className="text-muted-foreground"> — {pr.summary}</span> : null}
							</li>
						))}
					</ol>
				</section>
			) : null}
			{plan.planMarkdown ? (
				<div>
					<button
						type="button"
						aria-expanded={full}
						onClick={() => setFull((v) => !v)}
						className="text-xs text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
					>
						{full ? t("planCard.hideFullPlan") : t("planCard.showFullPlan")}
					</button>
					{full ? <div className="mt-2 max-h-96 overflow-y-auto"><ChatMarkdown text={plan.planMarkdown} /></div> : null}
				</div>
			) : null}
		</div>
	);
}
