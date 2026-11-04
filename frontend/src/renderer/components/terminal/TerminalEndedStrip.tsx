/** Replay cover and the strip shown when a terminal session has ended. */
import { RotateCcw } from "lucide-react";
import { useTranslation } from "react-i18next";
import { type WorkspaceSession } from "../../types/workspace";
import { ResumeAgentControl } from "../ResumeAgentControl";
import { Spinner } from "../ui/spinner";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";

export function ReplayCover() {
	return (
		// xterm remains live underneath; replay stays deliberately silent to avoid
		// covering settled output.
		<div
			aria-hidden="true"
			className="bg-terminal-opaque pointer-events-none absolute inset-0 grid place-items-center"
			data-testid="terminal-replay-cover"
		/>
	);
}

export type TerminalEndedStripProps = {
	canRestore: boolean;
	error?: string;
	isRestoring: boolean;
	onRestore: () => void;
	reviewStatus?: "running" | "complete" | "delivered" | "failed" | "cancelled";
	session?: WorkspaceSession;
	variant: "reviewer" | "session" | "shell";
};

export function TerminalEndedStrip({ canRestore, error, isRestoring, onRestore, reviewStatus, session, variant }: TerminalEndedStripProps) {
	const { t } = useTranslation();
	const reviewDelivered = variant === "reviewer" && reviewStatus === "delivered";
	const message = reviewDelivered
		? t("terminal.reviewerCompleted")
		: canRestore
		? t("terminal.restoreToContinue")
		: variant === "reviewer"
			? t("terminal.reviewerEnded")
			: variant === "shell"
				? t("terminal.shellExited")
				: t("terminal.sessionEndedNotTerminated");

	return (
		<div className="shrink-0 border-b border-border bg-surface/80 px-4 py-2">
			<div className="flex min-h-control-board items-center gap-3">
				<div className="min-w-0 flex-1">
					<div className="font-mono text-caption font-medium uppercase tracking-wide-md text-muted-foreground">
						{reviewDelivered ? t("terminal.reviewCompleted") : t("terminal.ended")}
					</div>
					<div className="mt-0.5 truncate text-xs text-muted-foreground">{message}</div>
				</div>
				{error && <div className="max-w-content-max truncate text-xs text-destructive">{error}</div>}
				{variant === "session" && session ? <ResumeAgentControl session={session} /> : null}
				{canRestore && (
					<Tooltip>
						<TooltipTrigger asChild>
							<span className="inline-flex">
								<button
									type="button"
									aria-label={t("terminal.restoreSession")}
									className="inline-flex size-control-form shrink-0 items-center justify-center rounded-md border border-border bg-raised text-foreground transition hover:bg-interactive-hover disabled:cursor-not-allowed disabled:opacity-50"
									disabled={isRestoring}
									onClick={onRestore}
								>
									{isRestoring ? <Spinner icon={RotateCcw} className="size-icon-base" /> : <RotateCcw className="size-icon-base" aria-hidden="true" />}
								</button>
							</span>
						</TooltipTrigger>
						<TooltipContent side="bottom">{t("terminal.restoreSession")}</TooltipContent>
					</Tooltip>
				)}
			</div>
		</div>
	);
}
