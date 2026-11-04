import { useTranslation } from "react-i18next";
import { Archive, Pin, PinOff } from "lucide-react";
import { memo, useRef, useState } from "react";
import { type WorkspaceSession } from "../../types/workspace";
import { usePinSession, useUnpinSession } from "../../hooks/usePinSession";
import { useTerminateSession } from "../../hooks/useTerminateSession";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import { cn } from "../../lib/utils";
import { SessionArchiveDialog, type SessionArchiveChoice } from "../SessionArchiveDialog";
import { SessionMessageAge } from "./SessionMessageAge";
import { ROW_ACTIONS_CLASS, SESSION_ACTION_CLASS } from "./constants";

export const SessionActions = memo(function SessionActions({
	session,
	isDragging,
	onKilled,
}: {
	session: WorkspaceSession;
	isDragging: boolean;
	onKilled?: (session: WorkspaceSession) => void;
}) {
	const { t } = useTranslation();
	const { mutate: pinSession } = usePinSession();
	const { mutate: unpinSession } = useUnpinSession();
	const [confirmOpen, setConfirmOpen] = useState(false);
	// Optimistic: navigate + drop the row as soon as kill starts (onMutate),
	// not after the daemon round-trip.
	const onKilledRef = useRef(onKilled);
	onKilledRef.current = onKilled;
	const { mutate: terminateSession, isPending: isKilling } = useTerminateSession({
		onOptimistic: (killed) => {
			onKilledRef.current?.(killed);
		},
	});

	// The row used to archive on the bare click while the session page asked
	// first; both surfaces now open the same confirm before anything moves.
	const handleArchive = (event: React.MouseEvent) => {
		event.stopPropagation();
		setConfirmOpen(true);
	};

	const confirmArchive = ({ deleteArtifacts }: SessionArchiveChoice) => {
		setConfirmOpen(false);
		terminateSession({ ...session, deleteArtifacts });
	};

	return (
		<div
			className="pointer-events-none absolute inset-y-0 right-0 z-chrome"
			data-session-actions=""
			onPointerDown={(event) => event.stopPropagation()}
		>
			<div
				className={cn(
					/* 1.3 — pin/kill: scale 0.8↔1 from center (not origin-right — that reads as a slide) */
					ROW_ACTIONS_CLASS,
					"origin-center scale-[0.8] opacity-0",
					"transition-[scale] duration-normal ease-[var(--ease-out)]",
					"motion-reduce:transition-none",
					!isDragging &&
						"group-has-[:focus-visible]/session-row:pointer-events-auto group-has-[:focus-visible]/session-row:scale-100 group-has-[:focus-visible]/session-row:opacity-100",
				)}
				data-session-action-buttons=""
			>
				<Tooltip>
					<TooltipTrigger asChild>
						<button
							aria-label={session.isPinned ? t("shell.unpinSession") : t("shell.pinSession")}
							className={cn(
								SESSION_ACTION_CLASS,
								"focus-visible:text-foreground",
								session.isPinned && "text-foreground",
							)}
							onClick={(event) => {
								event.stopPropagation();
								session.isPinned ? unpinSession(session) : pinSession(session);
							}}
							type="button"
						>
							{session.isPinned ? <PinOff aria-hidden="true" /> : <Pin aria-hidden="true" />}
						</button>
					</TooltipTrigger>
					<TooltipContent side="top">
						{session.isPinned ? t("shell.unpinSession") : t("shell.pinSession")}
					</TooltipContent>
				</Tooltip>
				<Tooltip>
					<TooltipTrigger asChild>
						<span className="inline-flex">
							<SessionArchiveDialog
								onConfirm={confirmArchive}
								onOpenChange={setConfirmOpen}
								open={confirmOpen}
								session={session}
								trigger={
									<button
										aria-label={t("shell.archiveSession")}
										className={cn(SESSION_ACTION_CLASS, "focus-visible:text-foreground")}
										disabled={isKilling}
										onClick={handleArchive}
										type="button"
									>
										<Archive aria-hidden="true" />
									</button>
								}
							/>
						</span>
					</TooltipTrigger>
					<TooltipContent side="top">{t("shell.archiveSession")}</TooltipContent>
				</Tooltip>
			</div>
			<SessionMessageAge session={session} />
		</div>
	);
});
