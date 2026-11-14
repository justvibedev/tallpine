import { useMutation } from "@tanstack/react-query";
import { CornerDownRight } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useBacklogInvalidate } from "../../hooks/useBacklogInvalidate";
import { answerAsk, type AskAnswerInput, type OwnerAsk } from "../../lib/ask-api";
import { cn } from "../../lib/utils";
import { useUiStore } from "../../stores/ui-store";
import { QUIET_ACTION_PILL } from "../chat/action-pill";

/**
 * One tracked ask. Open: each option is a one-tap answer (the recommended one
 * marked), plus an Other box. Answered or withdrawn: what the owner chose, or
 * why it was dropped. A re-asked ask shows why and the answer it repeats.
 */
export function AskCard({ projectId, ask, earlier, onJump }: {
	projectId: string;
	ask: OwnerAsk;
	/** The earlier answered ask this one repeats. */
	earlier?: OwnerAsk;
	/** Scrolls to the inline card on the asking turn; offered only when the ask has one. */
	onJump?: (ask: OwnerAsk) => void;
}) {
	const { t } = useTranslation();
	const invalidate = useBacklogInvalidate(projectId);
	const [other, setOther] = useState("");
	const answer = useMutation({
		mutationFn: (input: AskAnswerInput) => answerAsk(projectId, ask.ref, input),
		onSuccess: (r) => { if (r.deliveryFailed) useUiStore.getState().showGlobalToast(t("ask.deliveryFailed"), undefined, "error"); },
		onSettled: () => invalidate(),
	});
	const open = ask.state === "open";
	const error = answer.error instanceof Error ? answer.error.message : undefined;
	return (
		<section data-testid="ask-card" data-ask-state={ask.state} aria-label={`${ask.ref} ${ask.text}`} className="flex flex-col gap-2 rounded-xl border border-border bg-card px-3 py-2.5 text-sm">
			<div className="flex items-start gap-2">
				<span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-xs font-medium text-muted-foreground">{ask.ref}</span>
				<p className="min-w-0 flex-1 text-foreground">{ask.text}</p>
				{open && ask.anchored && onJump ? (
					<button type="button" data-testid="ask-jump" onClick={() => onJump(ask)} className="shrink-0 text-xs text-muted-foreground underline-offset-2 hover:text-foreground hover:underline">
						{t("ask.jump")}
					</button>
				) : null}
			</div>
			{ask.reason ? <p className="text-xs text-muted-foreground">{ask.reason}</p> : null}
			{ask.reaskOf ? (
				<div data-testid="ask-reask" className="flex flex-col gap-0.5 text-xs text-muted-foreground">
					<p><CornerDownRight aria-hidden="true" className="mr-1 inline size-3" />{t("ask.askedAgain", { reason: ask.reaskReason ?? "" })}</p>
					{earlier?.answer ? <p>{t("ask.earlierAnswer", { ref: earlier.ref, answer: earlier.answer.text })}</p> : null}
				</div>
			) : null}
			{open ? (
				<div className="flex flex-col gap-1.5">
					<div role="group" aria-label={t("ask.options")} className="flex flex-wrap gap-1.5">
						{ask.options.map((o) => (
							<button
								key={o.id}
								type="button"
								disabled={answer.isPending}
								onClick={() => answer.mutate({ option: o.id })}
								className={cn(QUIET_ACTION_PILL, "h-auto min-h-7 max-w-full justify-start gap-2 rounded-2xl py-1 text-left", o.id === ask.recommended && "border-logo-accent/60")}
							>
								<span className="min-w-0 whitespace-normal break-words">{o.label}</span>
								{o.id === ask.recommended ? <span className="shrink-0 text-2xs text-muted-foreground">{t("ask.recommended")}</span> : null}
							</button>
						))}
					</div>
					<form
						className="flex gap-1.5"
						onSubmit={(event) => {
							event.preventDefault();
							const text = other.trim();
							if (text) answer.mutate({ other: text }, { onSuccess: () => setOther("") });
						}}
					>
						<input
							value={other}
							onChange={(event) => setOther(event.target.value)}
							aria-label={t("ask.otherLabel")}
							placeholder={t("ask.otherPlaceholder")}
							maxLength={2000}
							className="min-w-0 flex-1 rounded-md border border-input bg-background px-2 py-1 text-xs"
						/>
						<button type="submit" disabled={answer.isPending || other.trim() === ""} className={cn(QUIET_ACTION_PILL, "shrink-0")}>{t("ask.send")}</button>
					</form>
					{error ? <p role="alert" className="text-xs text-destructive">{error}</p> : null}
				</div>
			) : ask.state === "answered" && ask.answer ? (
				<p data-testid="ask-answer" className="text-xs text-muted-foreground">{t("ask.answered", { answer: ask.answer.text })}</p>
			) : (
				<p data-testid="ask-withdrawn" className="text-xs text-muted-foreground">{ask.withdrawReason ? t("ask.withdrawnWhy", { reason: ask.withdrawReason }) : t("ask.withdrawn")}</p>
			)}
		</section>
	);
}
