/**
 * The session-wide plan, as the inspector's Plan tab.
 *
 * Open work leads — in progress, then not started — and finished steps fold away
 * under a count, because a long-lived list is mostly history. This only reads the
 * agent's list: the agent owns it and would re-send anything edited here.
 */

import { memo, useState } from "react";
import { ChevronRight } from "lucide-react";
import { useTranslation } from "react-i18next";
import { cn } from "../lib/utils";
import { splitPlanSteps } from "../lib/session-plan";
import type { ConversationPlan, PlanStep } from "../types/conversation";
import { Button } from "./ui/button";
import { STEP_LABEL, StepMark } from "./chat/TurnPlan";

export const PlanView = memo(function PlanView({ plan, sessionTitle }: { plan: ConversationPlan; sessionTitle?: string }) {
	const { t } = useTranslation();
	const [showDone, setShowDone] = useState(false);
	const { open, done } = splitPlanSteps(plan.steps);

	return (
		<section aria-label={t("inspector.plan")} className="flex flex-col gap-2 text-xs" data-session-plan="true">
			{sessionTitle ? (
				<h3 className="px-1.5 text-caption font-medium text-foreground">{t("plan.owner", { name: sessionTitle })}</h3>
			) : null}
			{open.length === 0 ? (
				<p className="px-1.5 text-caption text-muted-foreground" data-plan-hint="true">{t("plan.hint")}</p>
			) : null}
			{plan.explanation ? (
				<p className="text-caption leading-relaxed text-muted-foreground">{plan.explanation}</p>
			) : null}
			{open.length > 0 ? <StepList steps={open} /> : null}
			{done.length > 0 ? (
				<>
					<Button
						type="button"
						variant="ghost"
						size="sm"
						className="w-fit gap-1 px-1.5 text-muted-foreground"
						aria-expanded={showDone}
						onClick={() => setShowDone((shown) => !shown)}
					>
						<ChevronRight aria-hidden="true" className={cn("size-3.5 transition-transform", showDone && "rotate-90")} />
						{t("plan.done", { count: done.length })}
					</Button>
					{showDone ? <StepList steps={done} /> : null}
				</>
			) : null}
		</section>
	);
});

function StepList({ steps }: { steps: PlanStep[] }) {
	return (
		<ol className="flex flex-col">
			{steps.map((step, index) => (
				<li
					key={`${index}-${step.text}`}
					className="flex items-start gap-2.5 px-1.5 py-1"
					aria-label={`${step.text} — ${STEP_LABEL[step.status]}`}
				>
					<StepMark status={step.status} />
					<span
						className={cn(
							"min-w-0 flex-1 leading-[1.45]",
							step.status === "completed" && "text-muted-foreground/70 line-through",
							step.status === "in_progress" && "text-foreground",
							step.status === "pending" && "text-muted-foreground",
						)}
					>
						{step.text}
					</span>
				</li>
			))}
		</ol>
	);
}
