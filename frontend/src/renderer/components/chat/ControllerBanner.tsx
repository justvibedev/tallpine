import { CircleCheck, Loader2, TriangleAlert } from "lucide-react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import { type WorkspaceSession } from "../../types/workspace";
import { Button } from "../ui/button";
import { type ControllerState } from "../../types/conversation";
import { Spinner } from "../ui/spinner";


/**
 * Controller health and hibernation. A silent surface is indistinguishable from
 * an agent that is simply thinking.
 */
export function ControllerBanner({
	controller,
	provisionState,
	transitioning,
	automaticWakePending,
	reconnecting,
	onResume,
	resuming,
	resumeError,
	onOpenShell,
	openingShell,
	shellError,
	finishedLabel,
}: {
	controller: { state: ControllerState; error?: string };
	provisionState?: WorkspaceSession["provisionState"];
	transitioning?: boolean;
	automaticWakePending?: boolean;
	/** The daemon is still reconnecting this session after a restart. */
	reconnecting?: boolean;
	onResume?: () => void | Promise<unknown>;
	resuming?: boolean;
	resumeError?: string;
	onOpenShell?: () => void;
	openingShell?: boolean;
	shellError?: string;
	/** The session was ended on purpose (killed, or landed with its worker); a stopped controller is expected, not a crash. */
	finishedLabel?: string;
}) {
	const { t } = useTranslation();
	// Startup failures use the checklist's Retry action.
	if (provisionState === "provisioning" || provisionState === "failed") return null;
	if (finishedLabel && controller.state === "stopped") {
		return (
			<div role="status" aria-atomic="true" className="flex shrink-0 items-center gap-2.5 border-b border-border bg-surface px-4 py-2.5">
				<CircleCheck aria-hidden="true" className="size-3.5 shrink-0 text-muted-foreground" />
				<strong className="text-xs font-medium text-muted-foreground">{finishedLabel}</strong>
			</div>
		);
	}
	const waking = Boolean(resuming && controller.state === "stopped");
	// Nothing to act on yet: offering Resume here invites a click the daemon is
	// about to make unnecessary.
	const restarting = Boolean(reconnecting && controller.state === "stopped" && !waking);
	const resumeClick = () => {
		void Promise.resolve().then(() => onResume?.()).catch(() => {});
	};

	// The transition coordinator intentionally stops one controller before it
	// starts the other. The top-bar handoff state already explains that interval;
	// presenting its intermediate snapshot as a crash produces a red false alarm.
	if (controller.state === "ready" || controller.state === "busy" || controller.state === "hibernated") return null;
	if (controller.state === "stopped" && (transitioning || automaticWakePending)) return null;

	const copy: Partial<Record<ControllerState, { title: string; tone: string }>> = {
		connecting: {
			title: "Connecting to the agent…",
			tone: "text-muted-foreground",
		},
		recovering: {
			title: "Reconnecting to the agent",
			tone: "text-warning",
		},
		stopped: {
			title: waking ? "Waking agent…" : restarting ? "Reconnecting to the agent…" : "The agent controller stopped",
			tone: waking || restarting ? "text-muted-foreground" : "text-destructive",
		},
	};
	const shown = copy[controller.state];
	if (!shown) return null;
	const loading = controller.state === "connecting" || waking || restarting;
	const actionable = controller.state === "stopped" && !waking && !restarting;

	return (
		<div
			role={actionable ? "alert" : "status"}
			aria-atomic="true"
			className="flex shrink-0 items-start gap-2.5 border-b border-border bg-surface px-4 py-2.5"
		>
			{loading ? (
				<Spinner icon={Loader2}
					
				 className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />
			) : (
				<TriangleAlert aria-hidden="true" className={cn("mt-0.5 size-3.5 shrink-0", shown.tone)} />
			)}
			<div className="flex min-w-0 flex-1 flex-col gap-0.5">
				<strong className={cn("text-xs font-medium", shown.tone)}>{shown.title}</strong>
				{controller.state === "stopped" && waking ? (
					<span className="text-xs leading-snug text-muted-foreground">{t("chat.controller.wakingHint")}</span>
				) : restarting ? (
					<span className="text-xs leading-snug text-muted-foreground">{t("chat.controller.restartedHint")}</span>
				) : controller.error ? (
					<span className="text-xs leading-snug text-muted-foreground">{controller.error}</span>
				) : null}
				{actionable ? (
					<>
						<span className="text-xs leading-snug text-muted-foreground">
							{t("chat.controller.stoppedHint")}
						</span>
						{resumeError || shellError ? (
							<span className="text-xs leading-snug text-destructive">
								{resumeError ?? shellError}
							</span>
						) : null}
						<div className="mt-1.5 flex flex-wrap gap-2">
							{onResume ? (
								<Button
									type="button"
									size="sm"
									variant="outline"
									onClick={resumeClick}
									disabled={resuming}
								>
									{resuming ? "Resuming…" : "Resume agent"}
								</Button>
							) : null}
							{onOpenShell ? (
								<Button
									type="button"
									size="sm"
									variant="ghost"
									onClick={onOpenShell}
									disabled={openingShell}
								>
									{openingShell ? "Opening shell…" : "Open shell"}
								</Button>
							) : null}
						</div>
					</>
				) : null}
			</div>
		</div>
	);
}
