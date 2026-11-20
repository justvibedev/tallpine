import { useState } from "react";
import { useTranslation } from "react-i18next";
import { planCommentState } from "@tallpine/shared/plan/plan-comments";
import type { PlanView } from "../../lib/plan-api";
import { ACCENT_ACTION_PILL, QUIET_ACTION_PILL } from "../chat/action-pill";
import { PlanReader } from "./PlanReader";

export function Footer({ children }: { children: React.ReactNode }) {
	return <div className="flex items-center justify-end gap-2 px-3 pt-1">{children}</div>;
}

/**
 * A submitted plan on the card (B831): the plan, the owner's comments with
 * what the planner made of them, and one comment box that is sent on its own,
 * with Approve, or with Re-plan. Approve waits while the planner answers a comment.
 */
export function PlanReviewBody({ plan, busy, cancel, onComment, onReplan, onApprove }: {
	plan: PlanView; busy: boolean; cancel: React.ReactNode;
	/** Each takes the comment and a callback that clears the box once the server took it. */
	onComment: (text: string, sent: () => void) => void;
	onReplan: (comment: string, sent: () => void) => void;
	onApprove: (comment: string, sent: () => void) => void;
}) {
	const { t } = useTranslation();
	const [comment, setComment] = useState("");
	const state = planCommentState(plan);
	const hasText = comment.trim() !== "";
	const sent = () => setComment("");
	return (
		<>
			<div data-testid="plan-card-scroll" className="min-h-0 overflow-y-auto">
				<PlanReader plan={plan} />
				<PlanComments plan={plan} />
			</div>
			{state !== "none" ? (
				<p role="status" className="px-3 pb-1 text-xs leading-snug text-muted-foreground">
					{t(state === "held" ? "planCard.commentHeld" : "planCard.commentConsidering")}
				</p>
			) : null}
			<div className="px-3 pt-1">
				<label className="sr-only" htmlFor={`plan-comment-${plan.id}`}>{t("planCard.commentLabel")}</label>
				<textarea
					id={`plan-comment-${plan.id}`}
					rows={2}
					value={comment}
					disabled={busy}
					placeholder={t("planCard.commentPlaceholder")}
					onChange={(event) => setComment(event.target.value)}
					className="block max-h-32 min-h-[3.25rem] w-full resize-y rounded-lg border border-border bg-background/40 px-2.5 py-1.5 text-xs leading-relaxed text-foreground outline-none focus-visible:border-border-strong"
				/>
			</div>
			<Footer>
				{cancel}
				<button type="button" disabled={busy || !hasText} onClick={() => onComment(comment, sent)} className={QUIET_ACTION_PILL}>
					{t("planCard.comment")}
				</button>
				<button type="button" disabled={busy} onClick={() => onReplan(comment, sent)} className={QUIET_ACTION_PILL}>
					{t(hasText ? "planCard.replanWithComment" : "planCard.replan")}
				</button>
				<button type="button" disabled={busy || state !== "none"} onClick={() => onApprove(comment, sent)} className={ACCENT_ACTION_PILL}>
					{t(hasText ? "planCard.approveWithComment" : "planCard.approve")}
				</button>
			</Footer>
		</>
	);
}

function PlanComments({ plan }: { plan: PlanView }) {
	const { t } = useTranslation();
	if (plan.comments.length === 0) return null;
	return (
		<section aria-label={t("planCard.comments")} className="space-y-1 px-3 pb-2 text-xs leading-relaxed">
			<h4 className="font-medium text-muted-foreground">{t("planCard.comments")}</h4>
			<ul className="space-y-1">
				{plan.comments.map((c) => (
					<li key={c.id} data-comment-outcome={c.outcome}>
						<span className="whitespace-pre-wrap text-foreground">{c.text}</span>
						<span className="text-caption text-muted-foreground"> · {t(`planCard.commentOutcome.${c.outcome}`)}</span>
					</li>
				))}
			</ul>
		</section>
	);
}
