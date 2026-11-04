import { useAgentAccountLabel } from "../../../hooks/useAgentAccounts";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { ChevronDown, ChevronRight, CircleAlert, KeyRound, SquareTerminal } from "lucide-react";
import { cn } from "../../../lib/utils";
import { stripAnsi } from "../../../lib/ansi";
import { type ConversationActivity } from "../../../types/conversation";
import { setupDuration } from "./format";
import { TwoRowTimelineMarker } from "./markers";

/**
 * How the project's Setup commands (Project settings > Scripts) ended, before
 * the agent started. Compact like the other system rows; the failed step's
 * output (the tail the worker kept, already redacted) opens below it in
 * monospace.
 */
export function ProjectSetupRow({ activity }: { activity: ConversationActivity }) {
	const { t } = useTranslation();
	const [expanded, setExpanded] = useState(false);
	const detail = activity.detail;
	const status = detail?.setupStatus ?? (activity.status === "completed" ? "succeeded" : "failed");
	const step = detail?.setupStep ?? 1;
	const duration = typeof detail?.durationMs === "number" ? setupDuration(detail.durationMs) : undefined;
	const durationText = duration
		? duration.minutes > 0
			? t("chat.projectSetup.durationMinutes", { minutes: duration.minutes, seconds: duration.seconds })
			: t("chat.projectSetup.durationSeconds", { seconds: duration.seconds })
		: undefined;
	const headline = status === "succeeded"
		? durationText ? t("chat.projectSetup.succeededIn", { duration: durationText }) : t("chat.projectSetup.succeeded")
		: status === "timed_out"
			? t("chat.projectSetup.timedOut", { step })
			: typeof detail?.exitCode === "number" && detail.exitCode >= 0
				? t("chat.projectSetup.failedWithCode", { step, code: detail.exitCode })
				: t("chat.projectSetup.failed", { step });
	const failed = status !== "succeeded";
	const stepOutput = detail?.output?.trim() ? stripAnsi(detail.output) : undefined;
	// The failed step's command, who and where it ran, and its full log, one
	// fact per line above its output.
	const setupFailure = failed ? detail?.setupFailure : undefined;
	const contextLines: string[] = [];
	if (setupFailure?.command) contextLines.push(t("chat.projectSetup.stepCommand", { step, command: setupFailure.command }));
	if (setupFailure?.user) {
		contextLines.push(setupFailure.groups?.length
			? t("chat.projectSetup.ranAsWithGroups", { user: setupFailure.user, groups: setupFailure.groups.join(" ") })
			: t("chat.projectSetup.ranAs", { user: setupFailure.user }));
	}
	if (setupFailure?.dir) contextLines.push(t("chat.projectSetup.ranIn", { dir: setupFailure.dir }));
	if (setupFailure?.logPath) contextLines.push(t("chat.projectSetup.fullOutput", { path: setupFailure.logPath }));
	const context = contextLines.join("\n");
	const output = context ? (stepOutput ? `${context}\n\n${stepOutput}` : context) : stepOutput;
	const hint = failed ? detail?.setupHint?.trim() : undefined;
	const Icon = failed ? CircleAlert : SquareTerminal;
	return (
		<div className={cn("flex flex-col gap-1.5 rounded-md border bg-surface/60 px-3 py-2", failed ? "border-destructive/40" : "border-border")} data-testid="project-setup-row">
			<div className="flex items-center gap-2.5">
				<Icon aria-hidden="true" className={cn("size-3.5 shrink-0", failed ? "text-destructive" : "text-muted-foreground")} />
				<span className={cn("min-w-0 flex-1 text-xs", failed ? "text-destructive" : "text-foreground")}>{headline}</span>
				{output ? (
					<button
						type="button"
						aria-expanded={expanded}
						className="inline-flex shrink-0 items-center gap-1 rounded text-xs text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
						onClick={() => setExpanded((value) => !value)}
					>
						{expanded ? <ChevronDown aria-hidden="true" className="size-3.5" /> : <ChevronRight aria-hidden="true" className="size-3.5" />}
						{expanded ? t("chat.projectSetup.hideOutput") : t("chat.projectSetup.showOutput")}
					</button>
				) : null}
			</div>
			{hint ? <p className="pl-6 text-xs text-muted-foreground">{hint}</p> : null}
			{output && expanded ? (
				<pre className="max-h-64 overflow-auto whitespace-pre-wrap break-words rounded bg-background/60 px-2.5 py-2 font-mono text-[12px] leading-5 text-muted-foreground select-text">{output}</pre>
			) : null}
		</div>
	);
}

/** Where an account switch stopped the turn: not a failure, the conversation
 * continues on the new account with the next message. */
export function AccountSwitchedRow({ accountId }: { accountId?: string }) {
	const { t } = useTranslation();
	const label = useAgentAccountLabel();
	return <TwoRowTimelineMarker message={t("chat.accountSwitched", { email: label(accountId) })} detail={t("chat.accountSwitchedDetail")} wrapDetail />;
}

export function ReauthRow({ activity }: { activity: ConversationActivity }) {
	return (
		<div className="flex items-start gap-2.5 rounded-md border border-destructive/40 bg-surface px-3 py-2">
			<KeyRound aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 text-destructive" />
			<div className="flex min-w-0 flex-col gap-0.5">
				<strong className="text-xs font-medium text-destructive">
					The provider asked you to sign in again
				</strong>
				{activity.detail?.reason ? (
					<span className="text-xs leading-snug text-muted-foreground">
						{activity.detail.reason}
					</span>
				) : null}
			</div>
		</div>
	);
}
