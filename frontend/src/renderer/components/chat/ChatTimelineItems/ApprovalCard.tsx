import { PRODUCT_NAME } from "../../../../shared/brand";
import { ACCENT_ACTION_SEGMENT, ACCENT_ACTION_SHELL, QUIET_ACTION_PILL } from "../action-pill";
import { useEffect, useRef, useState } from "react";
import { ChevronDown, CornerDownLeft } from "lucide-react";
import { cn } from "../../../lib/utils";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "../../ui/dropdown-menu";
import { type ActivityKind, type ConversationActivity, type DecisionOption } from "../../../types/conversation";

/**
 * A decision the agent is blocked on.
 *
 * Decisions come from `activity.decisions` — the provider's own list — never from
 * a fixed set. The UI still presents common permission choices with Tallpine/Codex copy
 * so provider-flavored labels do not leak into the chat surface.
 */
export function ApprovalCard({
	activity,
	onDecide,
	busy,
	embedded,
}: {
	activity: ConversationActivity;
	onDecide?: (requestId: string, decisionId: string) => void;
	busy?: boolean;
	/** Render inside the shared chat composer instead of drawing another card shell. */
	embedded?: boolean;
}) {
	const resolved = activity.status !== "pending";
	const decisions = orderedApprovalDecisions(activity.decisions ?? []);
	const detail = activity.detail;
	const command = detail?.command ?? activity.summary;
	const subjectKind = approvalSubjectKind(activity);
	const rejectOnceDecision = decisions.find(
		(decision) => approvalDecisionKind(decision) === "reject_once",
	);
	const denyDecision =
		rejectOnceDecision ??
		decisions.find((decision) => approvalDecisionKind(decision) === "reject_always");
	const allowOnceDecision = decisions.find(
		(decision) => approvalDecisionKind(decision) === "allow_once",
	);
	const alternateAllowDecisions = allowOnceDecision
		? decisions.filter((decision) => approvalDecisionKind(decision) === "allow_always")
		: [];
	const otherDecisions = decisions.filter(
		(decision) =>
			decision !== denyDecision &&
			decision !== allowOnceDecision &&
			!alternateAllowDecisions.includes(decision),
	);
	// Some agents ask a question through a permission request, offering each
	// answer as its own option of the same kind. A generic per-kind label would
	// render identical buttons, so a kind offered more than once keeps the
	// provider's own wording.
	const repeatedKinds = repeatedDecisionKinds(decisions);
	const decisionLabel = (decision: DecisionOption) =>
		repeatedKinds.has(approvalDecisionKind(decision) ?? "") && decision.label.trim()
			? decision.label
			: approvalDecisionLabel(decision, subjectKind);
	const requestId = activity.requestId ?? "";
	const cardRef = useRef<HTMLDivElement>(null);
	const [commandExpanded, setCommandExpanded] = useState(false);
	const commandText = detail?.rawCommand ?? command;
	const commandIsLong = commandText.length > 240 || commandText.split("\n").length > 4;

	useEffect(() => {
		if (!embedded || resolved || busy || !requestId) return;
		const card = cardRef.current;
		if (card && (document.activeElement === document.body || !document.activeElement)) card.focus();
	}, [busy, embedded, requestId, resolved]);

	if (resolved) {
		return <ResolvedApprovalRow activity={activity} command={command} />;
	}

	return (
		<div
			ref={cardRef}
			role="group"
			tabIndex={-1}
			aria-label={`Approval request ${requestId}`.trim()}
			className={cn(
				"cursor-chat-activity-panel",
				embedded ? "px-1 py-0.5" : "rounded-lg border border-border px-3 py-2.5",
			)}
			onKeyDown={(event) => {
				if (busy || event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey) return;
				if (event.key === "Escape" && rejectOnceDecision) {
					event.preventDefault();
					onDecide?.(requestId, rejectOnceDecision.id);
					return;
				}
				const target = event.target;
				const interactive =
					target instanceof HTMLElement &&
					Boolean(target.closest("button, a, input, textarea, select, [contenteditable='true']"));
				if (event.key === "Enter" && !event.shiftKey && !interactive && allowOnceDecision) {
					event.preventDefault();
					onDecide?.(requestId, allowOnceDecision.id);
				}
			}}
		>
			<div className="flex flex-col">
				<p className="whitespace-pre-wrap text-sm leading-[1.4] text-foreground/90">
					{detail?.reason ?? approvalPrompt(subjectKind)}
				</p>

				<pre
					className={cn(
						"mt-2 scrollbar-none overflow-auto whitespace-pre-wrap break-words rounded-md border border-border/70 bg-background/45 px-2.5 py-1.5 font-mono text-xs leading-[1.45] text-muted-foreground",
						commandIsLong && !commandExpanded ? "max-h-[4.5rem] overflow-hidden" : "max-h-64",
					)}
				>
					{commandText}
				</pre>
				{commandIsLong ? (
					<button
						type="button"
						className="mt-1 self-start text-xs text-muted-foreground hover:text-foreground"
						aria-expanded={commandExpanded}
						onClick={() => setCommandExpanded((open) => !open)}
					>
						{commandExpanded ? "Show less" : "Show full command"}
					</button>
				) : null}

				<div className="mt-2 flex flex-wrap justify-end gap-1.5">
					{denyDecision ? (
						<button
							type="button"
							className={QUIET_ACTION_PILL}
							disabled={busy}
							onClick={() => onDecide?.(requestId, denyDecision.id)}
						>
							{decisionLabel(denyDecision)}
							{denyDecision === rejectOnceDecision ? (
								<kbd className="rounded-full bg-foreground/10 px-1.5 py-0.5 font-sans text-caption leading-none text-muted-foreground">
									Esc
								</kbd>
							) : null}
						</button>
					) : null}

					{allowOnceDecision ? (
						<div className={ACCENT_ACTION_SHELL}>
							<button
								type="button"
								className={ACCENT_ACTION_SEGMENT}
								disabled={busy}
								onClick={() => onDecide?.(requestId, allowOnceDecision.id)}
							>
								{decisionLabel(allowOnceDecision)}
								<kbd className="rounded-full bg-logo-accent-foreground/15 p-0.5" aria-label="Press Return">
									<CornerDownLeft aria-hidden="true" className="size-3" />
								</kbd>
							</button>
							{alternateAllowDecisions.length > 0 ? (
								<DropdownMenu>
									<DropdownMenuTrigger asChild>
										<button
											type="button"
											aria-label="More approval options"
											className="flex w-7 items-center justify-center border-l border-logo-accent-foreground/20 transition-colors hover:bg-logo-accent-bright focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-50"
											disabled={busy}
										>
											<ChevronDown aria-hidden="true" className="size-3.5" />
										</button>
									</DropdownMenuTrigger>
									<DropdownMenuContent
										align="end"
										side="bottom"
										className="min-w-52"
										data-approval-menu=""
										onEscapeKeyDown={(event) => event.stopPropagation()}
									>
										{alternateAllowDecisions.map((decision) => (
											<DropdownMenuItem
												key={decision.id}
												disabled={busy}
												onSelect={() => onDecide?.(activity.requestId ?? "", decision.id)}
											>
												{decisionLabel(decision)}
											</DropdownMenuItem>
										))}
									</DropdownMenuContent>
								</DropdownMenu>
							) : null}
						</div>
					) : null}
					{otherDecisions.map((decision) => (
						<button
							key={decision.id}
							type="button"
							className={QUIET_ACTION_PILL}
							disabled={busy}
							onClick={() => onDecide?.(requestId, decision.id)}
						>
							{decisionLabel(decision)}
						</button>
					))}
					{decisions.length === 0 ? (
						<p className="text-xs text-warning">
							The agent offered no decisions {PRODUCT_NAME} can present. Open diagnostics.
						</p>
					) : null}
				</div>
			</div>
		</div>
	);
}

function approvalSubjectKind(activity: ConversationActivity): ActivityKind | undefined {
	if (activity.detail?.subjectKind) return activity.detail.subjectKind;
	if (activity.detail?.method === "item/fileChange/requestApproval") return "file_change";
	if (activity.detail?.method === "item/commandExecution/requestApproval") return "command";
	return undefined;
}

function approvalPrompt(subjectKind?: ActivityKind): string {
	return subjectKind === "file_change"
		? "Do you want to allow these file changes?"
		: "Do you want to run this command?";
}

function approvalDecisionKind(decision: DecisionOption): DecisionOption["kind"] | "unknown" {
	return decision.kind ?? "unknown";
}

function approvalDecisionRank(decision: DecisionOption): number {
	switch (approvalDecisionKind(decision)) {
		case "reject_once":
			return 10;
		case "reject_always":
			return 15;
		case "allow_once":
			return 20;
		case "allow_always":
			return 30;
		default:
			return 40;
	}
}

function approvalDecisionLabel(decision: DecisionOption, subjectKind?: ActivityKind): string {
	switch (approvalDecisionKind(decision)) {
		case "allow_always":
			return subjectKind === "file_change"
				? "Always allow these file changes"
				: "Always allow this command";
		case "allow_once":
			return "Allow once";
		case "reject_once":
			return "Deny";
		case "reject_always":
			return "Always deny";
		default:
			return decision.label;
	}
}

function repeatedDecisionKinds(decisions: DecisionOption[]): Set<string> {
	const seen = new Set<string>();
	const repeated = new Set<string>();
	for (const decision of decisions) {
		const kind = approvalDecisionKind(decision);
		if (!kind) continue;
		if (seen.has(kind)) repeated.add(kind);
		seen.add(kind);
	}
	return repeated;
}

function orderedApprovalDecisions(decisions: DecisionOption[]): DecisionOption[] {
	return decisions
		.map((decision, index) => ({ decision, index }))
		.sort((left, right) => {
			const byRank = approvalDecisionRank(left.decision) - approvalDecisionRank(right.decision);
			return byRank || left.index - right.index;
		})
		.map(({ decision }) => decision);
}

function ResolvedApprovalRow({
	activity,
	command,
}: {
	activity: ConversationActivity;
	command: string;
}) {
	const detail = activity.detail;
	const decision = typeof detail?.decision === "string" ? detail.decision : undefined;
	const decisionKind = activity.decisions?.find((option) => option.id === decision)?.kind;
	const outcome = resolvedApprovalOutcome(activity.status, decision, detail?.resolvedBy, decisionKind);
	const title = detail?.cwd ? `${command}\n${detail.cwd}` : command;
	// An orchestrator's answer is recorded as "orchestrator:<session id>".
	const orchestrator = detail?.resolvedBy?.startsWith("orchestrator:") ? detail.resolvedBy.slice("orchestrator:".length) : "";
	const label = orchestrator ? `${outcome.label} by orchestrator ${orchestrator}` : outcome.label;

	return (
		<div className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)] items-center gap-3 rounded-md border border-border/80 bg-surface/45 px-2.5 py-1.5 text-xs text-muted-foreground">
			<strong className="shrink-0 font-medium text-foreground" title={typeof detail?.resolvedNote === "string" ? detail.resolvedNote : undefined}>
				{label}
			</strong>
			<span className="min-w-0 truncate text-right font-mono" title={title}>
				{command}
			</span>
		</div>
	);
}

function resolvedApprovalOutcome(
	status: ConversationActivity["status"],
	decision?: string,
	resolvedBy?: string,
	decisionKind?: DecisionOption["kind"],
): { label: string; success: boolean } {
	const value = decision?.toLowerCase() ?? "";
	if (status === "failed") return { label: "Approval expired", success: false };
	if (
		status === "cancelled" ||
		decisionKind === "reject_once" ||
		decisionKind === "reject_always" ||
		/(deny|decline|reject|cancel)/.test(value)
	) {
		return { label: "Cancelled", success: false };
	}
	if (decisionKind === "allow_always" || /(remember|always|amendment|policy)/.test(value)) {
		return { label: "Approved and remembered", success: true };
	}
	if (decisionKind === "allow_once" || /(allow|approve|accept)/.test(value)) {
		return { label: "Approved", success: true };
	}
	if (resolvedBy === "provider") return { label: "Resolved elsewhere", success: false };
	return { label: "Resolved", success: false };
}

/* -------------------------------------------------------------------------- */
/* turn diff                                                                   */
/* -------------------------------------------------------------------------- */
