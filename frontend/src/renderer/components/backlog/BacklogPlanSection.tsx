import { useMutation, useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useBacklogInvalidate } from "../../hooks/useBacklogInvalidate";
import type { BacklogItem } from "../../lib/backlog-api";
import { buildPlanAnyway, getItemPlan, isPlannerEnabled, itemPlanKey, plannerEnabledKey, replanItem, startPlan, type PlanView } from "../../lib/plan-api";
import { ACCENT_ACTION_PILL, QUIET_ACTION_PILL } from "../chat/action-pill";
import { PlanCard } from "../plan/PlanCard";
import { planPhase } from "../plan/planDockModel";
import { PlanReader } from "../plan/PlanReader";

/**
 * The plan of one backlog item (B205): a status chip, "Plan now" while there is
 * none, the plan card for a plan that waits on someone, and, once approved, the
 * plan to read with the stale and item-changed warnings. Renders nothing while
 * the planner switch is off. The item's own `plan` summary says whether to
 * fetch the full record.
 */
export function BacklogPlanSection({ item }: { item: BacklogItem }) {
	const summary = item.plan;
	// A child made from a plan's PR split is built from its parent's plan, not planned itself.
	const canPlan = item.state === "open" && item.childCount === 0 && !item.fields?.["plan.pr"];
	const enabled = useQuery({
		queryKey: plannerEnabledKey(item.projectId),
		queryFn: () => isPlannerEnabled(item.projectId),
		enabled: !summary && canPlan,
		retry: false,
	});
	const full = useQuery({
		queryKey: itemPlanKey(item.projectId, item.ref),
		queryFn: () => getItemPlan(item.projectId, item.ref),
		enabled: Boolean(summary),
		retry: false,
	});
	const plan = full.data ?? null;
	if (!summary) return canPlan && enabled.data ? <PlanNow item={item} /> : null;
	if (!plan) return null;
	return <PlanSection item={item} plan={plan} />;
}

function PlanNow({ item }: { item: BacklogItem }) {
	const { t } = useTranslation();
	const { run, busy, error } = usePlanAction(item.projectId);
	return (
		<section aria-label={t("backlog.plan.heading")} className="space-y-1">
			<button type="button" disabled={busy} onClick={() => run(() => startPlan(item.projectId, item.ref))} className={ACCENT_ACTION_PILL}>
				{t("backlog.plan.planNow")}
			</button>
			{error ? <p role="alert" className="text-xs text-destructive">{error}</p> : null}
		</section>
	);
}

function PlanSection({ item, plan }: { item: BacklogItem; plan: PlanView }) {
	const { t } = useTranslation();
	const { run, busy, error } = usePlanAction(item.projectId);
	const phase = planPhase(plan);
	const approved = phase === "approved";
	const waiting = phase !== "closed" && !approved;
	return (
		<section aria-label={t("backlog.plan.heading")} data-testid="backlog-plan" className="space-y-2">
			<div className="flex items-center gap-2">
				<h3 className="text-xs font-medium text-muted-foreground">{t("backlog.plan.heading")}</h3>
				<span data-testid="plan-chip" className="rounded-full border border-border px-2 py-0.5 text-caption text-muted-foreground">
					{(phase === "drafting" || phase === "writing") && plan.pausedUntil ? t("planCard.status.waitingForReset") : t(`planCard.status.${phase}`)}
				</span>
			</div>
			{waiting ? <PlanCard projectId={item.projectId} itemRef={item.ref} itemTitle={item.title} plan={plan} /> : null}
			{approved ? <PlanReader plan={plan} /> : null}
			{approved && item.childCount > 0 ? <p className="text-xs text-muted-foreground">{t("backlog.plan.children")}</p> : null}
			{plan.itemChanged && (phase === "approved" || phase === "review" || phase === "answering") ? (
				<p role="status" className="text-xs text-status-in-review">{t("backlog.plan.itemChanged")}</p>
			) : null}
			{approved && plan.stale ? (
				<div role="status" className="space-y-1">
					<p className="text-xs text-status-in-review">{t("backlog.plan.stale")}</p>
					<div className="flex items-center gap-2">
						<button type="button" disabled={busy} onClick={() => run(() => replanItem(item.projectId, item.ref))} className={QUIET_ACTION_PILL}>
							{t("planCard.replan")}
						</button>
						<button type="button" disabled={busy} onClick={() => run(() => buildPlanAnyway(item.projectId, item.ref))} className={ACCENT_ACTION_PILL}>
							{t("backlog.plan.buildAnyway")}
						</button>
					</div>
				</div>
			) : null}
			{approved && plan.buildAnyway ? <p className="text-xs text-muted-foreground">{t("backlog.plan.builtAnyway")}</p> : null}
			{approved && plan.itemChanged && !plan.stale ? (
				<button type="button" disabled={busy} onClick={() => run(() => replanItem(item.projectId, item.ref))} className={QUIET_ACTION_PILL}>
					{t("planCard.replan")}
				</button>
			) : null}
			{error ? <p role="alert" className="text-xs text-destructive">{error}</p> : null}
		</section>
	);
}

function usePlanAction(projectId: string) {
	const invalidate = useBacklogInvalidate(projectId);
	const act = useMutation({ mutationFn: (fn: () => Promise<PlanView>) => fn(), onSettled: () => invalidate() });
	return {
		run: (fn: () => Promise<PlanView>) => act.mutate(fn),
		busy: act.isPending,
		error: act.error instanceof Error ? act.error.message : undefined,
	};
}
