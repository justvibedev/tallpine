import { ChevronDown } from "lucide-react";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import type { PlanView } from "../../lib/plan-api";
import { cn } from "../../lib/utils";
import { correct, type PlanDraft } from "./planDockModel";

/**
 * The planner's assumptions on the answering card, collapsed by default
 * (B831): each is accepted unless the owner corrects it. A collapsed header
 * still says when one of them cuts scope.
 */
export function PlanAssumptions({ plan, draft, busy, update }: {
	plan: PlanView; draft: PlanDraft; busy: boolean;
	update: (change: (d: PlanDraft) => PlanDraft) => void;
}) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const [correcting, setCorrecting] = useState<string | null>(null);
	const listId = useId();
	if (plan.assumptions.length === 0) return null;
	const scopeCuts = plan.assumptions.some((a) => a.scopeCut === true && draft.corrections[a.id] === undefined);
	return (
		<section aria-label={t("planCard.assumptions")} className="px-3">
			<button
				type="button"
				aria-expanded={open}
				aria-controls={listId}
				onClick={() => setOpen((v) => !v)}
				className="flex w-full items-center gap-1.5 pb-1 text-left text-xs font-medium text-muted-foreground hover:text-foreground"
			>
				<ChevronDown aria-hidden="true" className={cn("size-3.5 shrink-0 transition-transform", !open && "-rotate-90")} />
				<span className="min-w-0 flex-1">{t("planCard.assumptions")} · {plan.assumptions.length}</span>
				{!open && scopeCuts ? <span className="shrink-0 text-caption font-medium text-warning">{t("planCard.scopeCutHint")}</span> : null}
			</button>
			{open ? (
				<ul id={listId} className="space-y-1">
					{plan.assumptions.map((a) => {
						const corrected = draft.corrections[a.id];
						const editing = correcting === a.id;
						const scopeCut = corrected === undefined && a.scopeCut === true;
						return (
							<li key={a.id} className={cn("text-xs leading-relaxed", scopeCut && "border-l-2 border-warning pl-2")} data-scope-cut={scopeCut || undefined}>
								{scopeCut ? <div className="text-caption font-medium text-warning">{t("planCard.scopeCut")}</div> : null}
								<div className="flex items-start gap-2">
									<span className={cn("min-w-0 flex-1", corrected !== undefined && "text-muted-foreground line-through")}>{a.text}</span>
									<button
										type="button"
										disabled={busy}
										aria-expanded={editing}
										onClick={() => setCorrecting(editing ? null : a.id)}
										className="shrink-0 text-caption text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
									>
										{t("planCard.correct")}
									</button>
								</div>
								{editing || corrected !== undefined ? (
									<input
										type="text"
										aria-label={t("planCard.correctionLabel", { text: a.text })}
										value={corrected ?? ""}
										autoFocus={editing}
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
			) : null}
		</section>
	);
}
