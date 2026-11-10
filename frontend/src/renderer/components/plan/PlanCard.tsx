import { useMutation } from "@tanstack/react-query";
import { ChevronDown, Loader2 } from "lucide-react";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { useBacklogInvalidate } from "../../hooks/useBacklogInvalidate";
import {
	acceptPlanDefaults, answerPlan, approvePlan, cancelPlan, replanItem, startPlan,
	type PlanView,
} from "../../lib/plan-api";
import { formatUsageReset } from "@tallpine/shared/format/usage-reset";
import { cn } from "../../lib/utils";
import { ACCENT_ACTION_PILL, QUIET_ACTION_PILL } from "../chat/action-pill";
import { QuestionOptions } from "../chat/QuestionOptions";
import {
	OTHER_OPTION, acceptedAssumptionCount, chooseOption, correct, initialDraft, isComplete, isDefaults, optionLabel, planPhase,
	toAnswersRequest, writeOther, type PlanDraft,
} from "./planDockModel";
import { clearPlanDraft, loadPlanDraft, savePlanDraft } from "./planDraftStore";
import { PlanReader } from "./PlanReader";
import { Spinner } from "../ui/spinner";

/**
 * The owner's surface for one plan (B205): the planner's questions as option
 * rows with the recommended answer preselected plus Other, its assumptions
 * accepted unless corrected, and the buttons that move the plan on. Waiting is
 * the default: nothing here times out or answers for the owner.
 */
export function PlanCard({ projectId, itemRef, itemTitle, plan, collapsed: controlled, onToggle, onAnswered }: {
	projectId: string; itemRef: string; itemTitle: string; plan: PlanView;
	/** Controlled by the card stack so only one card is open; uncontrolled when absent. */
	collapsed?: boolean; onToggle?: () => void;
	/** Answers or defaults were accepted by the server. */
	onAnswered?: () => void;
}) {
	const { t, i18n } = useTranslation();
	const phase = planPhase(plan);
	// A planner stopped on its provider's usage limit is waiting for the reset, not writing.
	const resetAt = (phase === "drafting" || phase === "writing") && plan.pausedUntil ? formatUsageReset(plan.pausedUntil, i18n.language) : null;
	const needsOwner = phase === "answering" || phase === "review" || phase === "failed";
	const [ownCollapsed, setOwnCollapsed] = useState(!needsOwner);
	// Fold when the plan moves on from needing the owner (answers accepted); a card needing them again reopens.
	const [seenNeedsOwner, setSeenNeedsOwner] = useState(needsOwner);
	if (seenNeedsOwner !== needsOwner) { setSeenNeedsOwner(needsOwner); setOwnCollapsed(!needsOwner); }
	const collapsed = controlled ?? ownCollapsed;
	const toggle = onToggle ?? (() => setOwnCollapsed((v) => !v));
	const headerId = useId();
	const invalidate = useBacklogInvalidate(projectId);
	const act = useMutation({
		mutationFn: (run: () => Promise<PlanView>) => run(),
		onSettled: () => invalidate(),
	});
	const busy = act.isPending;
	const answered = () => { clearPlanDraft(plan.id); setOwnCollapsed(true); onAnswered?.(); };
	const cancel = (
		<button type="button" disabled={busy} onClick={() => act.mutate(async () => { const p = await cancelPlan(projectId, itemRef); clearPlanDraft(plan.id); return p; })} className={cn(QUIET_ACTION_PILL, "mr-auto")}>
			{t("planCard.cancel")}
		</button>
	);
	const error = act.error instanceof Error ? act.error.message : undefined;

	return (
		<section
			aria-labelledby={headerId}
			data-testid="plan-card"
			data-plan-status={plan.status}
			className="flex max-h-[min(60vh,32rem)] shrink-0 flex-col overflow-hidden rounded-[var(--radius-chat-composer)] border border-border-strong bg-surface shadow-sm animate-in fade-in-0 duration-200"
		>
			<button
				type="button"
				aria-expanded={!collapsed}
				onClick={toggle}
				className="flex w-full min-w-0 shrink-0 items-center gap-2 px-3 py-2 text-left"
			>
				<ChevronDown aria-hidden="true" className={cn("size-3.5 shrink-0 text-muted-foreground transition-transform", collapsed && "-rotate-90")} />
				<span id={headerId} className="min-w-0 flex-1 truncate text-xs font-medium text-foreground">
					{t("planCard.title", { ref: itemRef, title: itemTitle })}
				</span>
				<span className="shrink-0 text-caption text-muted-foreground">{resetAt ? t("planCard.status.waitingForReset") : t(`planCard.status.${phase}`)}</span>
			</button>
			{collapsed ? null : (
				<div className="flex min-h-0 flex-1 flex-col pb-2">
					{phase === "drafting" || phase === "writing" ? (
						<p className="flex items-center gap-2 px-3 pb-1 text-xs text-muted-foreground" role="status">
							<Spinner icon={Loader2} className="size-3.5" />
							{resetAt ? t("planCard.waitingForReset", { time: resetAt }) : t(phase === "drafting" ? "planCard.drafting" : "planCard.writing")}
						</p>
					) : null}
					{phase === "answering" ? (
						<AnsweringBody
							plan={plan}
							busy={busy}
							cancel={cancel}
							onSend={(draft) => act.mutate(() => answerPlan(projectId, itemRef, toAnswersRequest(plan, draft)), { onSuccess: answered })}
							onDefaults={() => act.mutate(() => acceptPlanDefaults(projectId, itemRef), { onSuccess: answered })}
						/>
					) : null}
					{phase === "writing" && plan.answers ? (
						<div data-testid="plan-card-scroll" className="min-h-0 overflow-y-auto"><AnswerSummary plan={plan} /></div>
					) : null}
					{phase === "review" ? (
						<>
							<div data-testid="plan-card-scroll" className="min-h-0 overflow-y-auto"><PlanReader plan={plan} /></div>
							<Footer>
								{cancel}
								<button type="button" disabled={busy} onClick={() => act.mutate(() => replanItem(projectId, itemRef))} className={QUIET_ACTION_PILL}>
									{t("planCard.replan")}
								</button>
								<button type="button" disabled={busy} onClick={() => act.mutate(() => approvePlan(projectId, itemRef))} className={ACCENT_ACTION_PILL}>
									{t("planCard.approve")}
								</button>
							</Footer>
						</>
					) : null}
					{phase === "failed" ? (
						<>
							<div data-testid="plan-card-scroll" className="min-h-0 overflow-y-auto">
								<p className="px-3 pb-1 text-xs text-muted-foreground">{plan.failure || t("planCard.failed")}</p>
							</div>
							<Footer>
								{cancel}
								<button type="button" disabled={busy} onClick={() => act.mutate(() => startPlan(projectId, itemRef))} className={ACCENT_ACTION_PILL}>
									{t("planCard.planAgain")}
								</button>
							</Footer>
						</>
					) : null}
					{error ? <p role="alert" className="px-3 pt-1 text-xs leading-snug text-destructive">{error}</p> : null}
				</div>
			)}
		</section>
	);
}

function Footer({ children }: { children: React.ReactNode }) {
	return <div className="flex items-center justify-end gap-2 px-3 pt-1">{children}</div>;
}

function AnsweringBody({ plan, busy, cancel, onSend, onDefaults }: { plan: PlanView; busy: boolean; cancel: React.ReactNode; onSend: (draft: PlanDraft) => void; onDefaults: () => void }) {
	const { t } = useTranslation();
	const [draft, setDraft] = useState<PlanDraft>(() => loadPlanDraft(plan) ?? initialDraft(plan));
	const update = (change: (d: PlanDraft) => PlanDraft) => setDraft((d) => { const next = change(d); savePlanDraft(plan.id, next); return next; });
	const [correcting, setCorrecting] = useState<string | null>(null);
	const complete = isComplete(plan, draft);
	const defaults = isDefaults(plan, draft);
	const accepted = acceptedAssumptionCount(plan, draft);
	return (
		<div className="flex min-h-0 flex-1 flex-col">
			<div data-testid="plan-card-scroll" className="min-h-0 space-y-3 overflow-y-auto">
				{plan.questions.map((q) => {
					const choice = draft.choices[q.id];
					const labelId = `plan-q-${plan.id}-${q.id}`;
					return (
						<fieldset key={q.id} aria-labelledby={labelId} disabled={busy} className="min-w-0">
							<legend id={labelId} className="px-3 pb-1 text-xs font-medium text-foreground">{q.text}</legend>
							{q.reason ? <p className="px-3 pb-1 text-caption leading-relaxed text-muted-foreground">{q.reason}</p> : null}
							<QuestionOptions
								name={`plan-${plan.id}-${q.id}`}
								options={[
									...q.options.map((o) => ({
										value: o.id,
										label: o.label,
										description: o.id === q.recommended ? t("planCard.recommended") : undefined,
									})),
									{ value: OTHER_OPTION, label: t("planCard.other") },
								]}
								isChecked={(value) => choice?.selected === value}
								onSelect={(value) => update((d) => chooseOption(d, q.id, value))}
							/>
							{choice?.selected === OTHER_OPTION ? (
								<div className="px-4 pt-1">
									<label className="sr-only" htmlFor={`${labelId}-other`}>{t("planCard.otherLabel")}</label>
									<input
										id={`${labelId}-other`}
										type="text"
										value={choice.other}
										autoFocus
										placeholder={t("planCard.otherPlaceholder")}
										onChange={(event) => update((d) => writeOther(d, q.id, event.target.value))}
										className="h-8 w-full rounded-lg border border-border bg-background/40 px-2.5 text-xs text-foreground outline-none focus-visible:border-border-strong"
									/>
								</div>
							) : null}
						</fieldset>
					);
				})}
				{plan.assumptions.length > 0 ? (
					<section aria-label={t("planCard.assumptions")} className="px-3">
						<h4 className="pb-1 text-xs font-medium text-muted-foreground">{t("planCard.assumptions")}</h4>
						<ul className="space-y-1">
							{plan.assumptions.map((a) => {
								const corrected = draft.corrections[a.id];
								const open = correcting === a.id;
								const scopeCut = corrected === undefined && a.scopeCut === true;
								return (
									<li key={a.id} className={cn("text-xs leading-relaxed", scopeCut && "border-l-2 border-warning pl-2")} data-scope-cut={scopeCut || undefined}>
										{scopeCut ? <div className="text-caption font-medium text-warning">{t("planCard.scopeCut")}</div> : null}
										<div className="flex items-start gap-2">
											<span className={cn("min-w-0 flex-1", corrected !== undefined && "text-muted-foreground line-through")}>{a.text}</span>
											<button
												type="button"
												disabled={busy}
												aria-expanded={open}
												onClick={() => setCorrecting(open ? null : a.id)}
												className="shrink-0 text-caption text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
											>
												{t("planCard.correct")}
											</button>
										</div>
										{open || corrected !== undefined ? (
											<input
												type="text"
												aria-label={t("planCard.correctionLabel", { text: a.text })}
												value={corrected ?? ""}
												autoFocus={open}
												disabled={busy}
												placeholder={t("planCard.correctionPlaceholder")}
												onChange={(event) => update((d) => correct(d, a.id, event.target.value))}
												className="mt-1 h-8 w-full rounded-lg border border-border bg-background/40 px-2.5 text-xs text-foreground outline-none focus-visible:border-border-strong"
											/>
										) : null}
									</li>
								);
							})}
						</ul>
					</section>
				) : null}
			</div>
			<Footer>
				{cancel}
				{plan.assumptions.length > 0 ? (
					<span className="text-caption text-muted-foreground">{t("planCard.acceptedCount", { count: accepted })}</span>
				) : null}
				<button type="button" disabled={busy} onClick={onDefaults} className={QUIET_ACTION_PILL}>
					{t("planCard.defaults")}
				</button>
				<button
					type="button"
					disabled={busy || !complete}
					onClick={() => (defaults ? onDefaults() : onSend(draft))}
					className={ACCENT_ACTION_PILL}
				>
					{t("planCard.send")}
				</button>
			</Footer>
		</div>
	);
}

function AnswerSummary({ plan }: { plan: PlanView }) {
	const { t } = useTranslation();
	const answers = plan.answers;
	if (!answers) return null;
	return (
		<dl className="space-y-1 px-3 text-xs" aria-label={t("planCard.yourAnswers")}>
			{plan.questions.map((q) => {
				const a = answers.questions?.[q.id];
				if (!a) return null;
				return (
					<div key={q.id}>
						<dt className="text-muted-foreground">{q.text}</dt>
						<dd className="text-foreground">{a.other ? a.other : optionLabel(plan, q.id, a.option ?? "")}</dd>
					</div>
				);
			})}
		</dl>
	);
}
