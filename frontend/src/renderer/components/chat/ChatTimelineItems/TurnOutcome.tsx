import { cn } from "../../../lib/utils";
import { UsageLimitNotice } from "../UsageLimitNotice";
import { type ConversationTurnAutoRetry, type ConversationUsageLimit } from "../../../types/conversation";
import { linkifiedProviderErrorText } from "./ErrorActivityRow";
import { formatDuration } from "./format";
import { TwoRowTimelineMarker } from "./markers";

export interface TurnOutcomeRetryControl {
	onRetry: () => void;
	pending?: boolean;
	error?: string;
	disabled?: boolean;
}

/** Must match ports.ErrChatAppRestart in the backend. */
export const APP_RESTART_ERROR = "the app restarted while the agent was working";

export function TurnOutcome({
	state,
	error,
	retry,
	usageLimit,
	autoRetry,
	retriedTransient,
}: {
	state: "recovered" | "interrupted" | "failed";
	error?: string;
	retry?: TurnOutcomeRetryControl;
	usageLimit?: ConversationUsageLimit;
	/** The daemon is retrying this network failure on its own. */
	autoRetry?: ConversationTurnAutoRetry;
	/** A network failure a later attempt already retried. */
	retriedTransient?: boolean;
}) {
	// A dropped connection the daemon is handling is not a problem to act on, so
	// it reads as a quiet status line rather than the red failure block.
	if (state === "failed" && autoRetry) {
		const message = autoRetry.offline
			? "Waiting for the network to reconnect…"
			: `Reconnecting… (attempt ${autoRetry.attempt} of ${autoRetry.maxAttempts})`;
		return <TwoRowTimelineMarker message={message} detail={error} />;
	}
	if (state === "failed" && retriedTransient) {
		return <TwoRowTimelineMarker message="Connection dropped; the turn was sent again" detail={error} />;
	}
	if (state === "failed" && usageLimit) {
		return (
			<div className="flex min-w-0 flex-col gap-2 py-3">
				<UsageLimitNotice
					compact={!retry}
					usageLimit={usageLimit}
					onResume={retry?.onRetry}
					pending={retry?.pending}
					disabled={retry?.disabled}
					error={retry?.error}
				/>
				{error ? <div className="whitespace-pre-wrap wrap-anywhere text-sm leading-relaxed text-foreground">{linkifiedProviderErrorText(error)}</div> : null}
			</div>
		);
	}
	// The backend marks a turn cut off by the app going down (not by the person
	// pressing stop) with this error text; it replaces the user-interrupt label.
	const appRestart = state === "interrupted" && error === APP_RESTART_ERROR;
	const copy = {
		recovered: {
			label: "This turn was recovered from an earlier session",
			tone: "text-muted-foreground/70",
		},
		interrupted: {
			label: appRestart ? "The agent was interrupted by an app restart" : "The agent was interrupted by you",
			tone: "text-muted-foreground/70",
		},
		failed: { label: "The agent ran into a problem", tone: "text-destructive" },
	}[state];

	const action = retry ? (
		<>
			{retry.error ? (
				<span role="alert" className="max-w-[50%] text-pretty text-right text-xs leading-tight text-destructive">
					{retry.error}
				</span>
			) : null}
			<button
				type="button"
				onClick={retry.onRetry}
				disabled={retry.pending || retry.disabled}
				aria-label="Retry this turn"
				title={retry.error ?? (retry.disabled ? "Wait for the current turn to finish" : "Send this prompt again as a new turn")}
				data-testid="retry-turn"
				className="shrink-0 rounded px-1.5 py-0.5 text-xs text-muted-foreground/70 transition-colors hover:text-foreground focus-visible:outline focus-visible:outline-2 focus-visible:outline-ring disabled:pointer-events-none disabled:opacity-50"
			>
				{retry.pending ? "Retrying…" : "Retry"}
			</button>
		</>
	) : undefined;
	if (state === "failed") {
		return (
			<div className="flex min-w-0 flex-col gap-2 py-3">
				<div className="flex items-baseline justify-between gap-3 text-sm font-medium text-destructive">
					<span>{copy.label}</span>
					{action}
				</div>
				{error ? <div className="whitespace-pre-wrap wrap-anywhere text-sm leading-relaxed text-foreground">{linkifiedProviderErrorText(error)}</div> : null}
			</div>
		);
	}
	return <TwoRowTimelineMarker message={copy.label} detail={appRestart ? undefined : error} tone={copy.tone} action={action} />;
}

/** Turn wall-clock duration; lives on the action row next to rollback, not the Done divider. */
export function TurnDuration({ durationMs, inline = false }: { durationMs: number; inline?: boolean }) {
	if (durationMs <= 0) return null;
	return (
		<span
			className={cn(
				"shrink-0 font-sans text-sm leading-none tabular-nums text-muted-foreground",
				inline && "group-hover/row:text-foreground",
				!inline && "px-1",
			)}
		>
			{formatDuration(durationMs)}
		</span>
	);
}
