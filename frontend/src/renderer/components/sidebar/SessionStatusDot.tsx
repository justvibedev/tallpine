import { useTranslation } from "react-i18next";
import { Moon, Timer, UserRound } from "lucide-react";
import { type WorkspaceSession } from "../../types/workspace";
import { getSessionStatusDotView } from "../../lib/session-presentation";
import { cn } from "../../lib/utils";

// Colour tracks the session's board section, preserving SCM state while the
// agent runs; motion stays on raw agent activity. A no-PR idle session turns
// blue when it starts working. See getSessionStatusDotView for the lane mapping.
export function SessionStatusDot({ session }: { session: WorkspaceSession }) {
	const { t } = useTranslation();
	const dot = getSessionStatusDotView(session);
	if (dot.idleReason) {
		// Quiet but not done: name who or what it waits on, in the idle slot.
		const owner = dot.idleReason === "owner";
		const label = t(owner ? "status.idleWaitingOnYou" : "status.idleWaitingOnTimer");
		const Icon = owner ? UserRound : Timer;
		return (
			<span
				className="relative z-[1] inline-flex shrink-0 items-center justify-center px-1"
				data-session-status={session.status}
				data-session-idle-reason={dot.idleReason}
				title={label}
			>
				<Icon aria-hidden="true" className={cn("size-2.5", owner ? "text-status-needs-you-dot" : "text-passive")} />
				<span className="sr-only">{label}</span>
			</span>
		);
	}
	if (dot.sleeping) {
		// A sleeping agent takes the idle slot: same place, same quiet tone.
		return (
			<span
				className="relative z-[1] inline-flex shrink-0 items-center justify-center px-1"
				data-session-status={session.status}
				data-session-sleeping=""
				title={t("status.sleeping")}
			>
				<Moon aria-hidden="true" className="size-2.5 text-passive" />
				<span className="sr-only">{t("status.sleeping")}</span>
			</span>
		);
	}
	return (
		<span
			aria-hidden={dot.title ? undefined : "true"}
			className="relative z-[1] inline-flex shrink-0 items-center justify-center px-1.5"
			title={dot.title}
		>
			<span
				className={cn(
					"size-2 rounded-full",
					dot.className,
					dot.breathe && "animate-status-pulse",
				)}
				data-session-status={session.status}
			/>
		</span>
	);
}
