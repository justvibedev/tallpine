import { AppLink } from "../AppLink";
import { useTranslation } from "react-i18next";
import { ArrowUpRight, Bell, LoaderCircle, RotateCcw, X } from "lucide-react";
import { memo } from "react";
import { formatTimeCompact } from "../../lib/format-time";
import { type NotificationDTO } from "../../lib/notifications";
import { cn } from "../../lib/utils";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";

import { notificationCopy, notificationIcon, notificationIconClass, notificationMentions, notificationPRTitleLink } from "./notification-copy";
import { Spinner } from "../ui/spinner";
export function NotificationEmpty({ icon: Icon, message }: { icon: typeof Bell; message: string }) {
	return (
		<div className="flex flex-col items-center gap-2.5 px-4 py-5 text-center">
			<div className="grid size-control-xl place-items-center rounded-full border border-border bg-surface text-passive">
				{Icon === LoaderCircle ? <Spinner icon={Icon} className="size-icon-base" /> : <Icon className="size-icon-base" aria-hidden="true" />}
			</div>
			<p className="text-control text-muted-foreground">{message}</p>
		</div>
	);
}

/**
 * The whole row is the click target for live sessions. A terminated session
 * behind a `needs_input` notification is not navigable — restore is the only
 * action, since there is a paused agent to resume. PR-outcome notifications
 * (`offerRestore` false) describe finished work, so a terminated session stays
 * viewable there instead of being gated behind restore. PR titles stay a real
 * link so a PR row can open the PR without a separate icon button.
 */
export const NotificationItem = memo(function NotificationItem({
	highlighted,
	notification,
	offerRestore,
	onOpenSession,
	onRestore,
	onClear,
	hostLabel,
	projectName,
	clearing,
	clearDisabled,
	restoring,
	restoreDisabled,
	sessionName,
	sessionsReady,
	terminated,
}: {
	highlighted: boolean;
	notification: NotificationDTO;
	offerRestore: boolean;
	onOpenSession: (notification: NotificationDTO) => void;
	onRestore: (notification: NotificationDTO) => void;
	onClear: (notification: NotificationDTO) => void;
	hostLabel?: string;
	projectName?: string;
	clearing: boolean;
	clearDisabled: boolean;
	restoring: boolean;
	restoreDisabled: boolean;
	sessionName?: string;
	sessionsReady: boolean;
	terminated: boolean;
}) {
	const { t } = useTranslation();
	const Icon = notificationIcon(notification.type);
	const sessionId = notification.target.sessionId || notification.sessionId;
	const canOpenSession = Boolean(sessionId) && sessionsReady && (!terminated || !offerRestore);
	const copy = notificationCopy(notification, sessionName);
	const titleLink = notificationPRTitleLink(notification, copy.title);
	const showSessionMeta = Boolean(sessionName) && !notificationMentions(copy, sessionName ?? "");
	const openRow = () => {
		if (canOpenSession) onOpenSession(notification);
	};
	return (
		<div role="listitem">
			<div
				className={cn(
					"group grid grid-cols-notification items-start gap-3 px-4 py-3 text-left transition-[background-color,opacity,transform] duration-fast will-change-transform",
					highlighted && "notification-row-enter",
					canOpenSession
						? "cursor-pointer hover:bg-interactive-hover active:scale-[0.99] active:bg-interactive-active"
						: "cursor-default",
					!highlighted && "opacity-55 hover:opacity-80",
				)}
				onClick={openRow}
				onKeyDown={(event) => {
					if (!canOpenSession || event.target !== event.currentTarget) return;
					if (event.key !== "Enter" && event.key !== " ") return;
					event.preventDefault();
					openRow();
				}}
				role={canOpenSession ? "button" : undefined}
				tabIndex={canOpenSession ? 0 : undefined}
				// Row actions set title="" so this native tooltip does not show over them.
				title={canOpenSession ? t("notify.openSessionTitle") : undefined}
			>
				<div
					className={cn(
						"grid size-notification-icon shrink-0 place-items-center transition-transform duration-fast group-hover:brightness-110 group-active:scale-90",
						notificationIconClass(notification.type),
					)}
				>
					<Icon className="size-5" strokeWidth={2} aria-hidden="true" />
				</div>
				<div className="min-w-0">
					{/* Match the 26px icon band so the title centers with the left glyph. */}
					<div className="flex min-h-notification-icon items-center">
						<span
							aria-label={copy.title}
							className={cn(
								"min-w-0 break-words text-control leading-snug text-foreground",
								highlighted && "font-medium",
							)}
						>
							{titleLink ? (
								<>
									{titleLink.before}
									<AppLink
										aria-label={t("inspector.openPR", { number: titleLink.number })}
										className="inline-flex items-center gap-0.5 underline-offset-2 hover:underline focus-visible:rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
										href={titleLink.url}
										onClick={(event) => {
											event.stopPropagation();
										}}
										rel="noopener noreferrer"
										target="_blank"
									>
										{titleLink.label}
										<ArrowUpRight aria-hidden="true" className="size-icon-2xs shrink-0" strokeWidth={2} />
									</AppLink>
									{titleLink.after}
								</>
							) : (
								copy.title
							)}
						</span>
					</div>
					{copy.body ? (
						<p className="mt-0.5 whitespace-pre-wrap break-words text-caption leading-snug text-muted-foreground">
							{copy.body}
						</p>
					) : null}
					{hostLabel || projectName || showSessionMeta ? (
						<p className="mt-1 flex min-w-0 items-center gap-1.5 text-caption leading-none text-passive">
							{hostLabel ? <span className="shrink-0 font-medium text-muted-foreground">{hostLabel}</span> : null}
							{hostLabel && (projectName || showSessionMeta) ? <span aria-hidden="true">·</span> : null}
							{projectName ? (
								<span className="truncate font-medium text-muted-foreground">{projectName}</span>
							) : null}
							{projectName && showSessionMeta ? <span aria-hidden="true">·</span> : null}
							{showSessionMeta ? <span className="truncate">{sessionName}</span> : null}
						</p>
					) : null}
				</div>
				{/* Time and row actions share the same icon-height band. */}
				<div className="flex h-notification-icon shrink-0 items-center gap-1">
					<time className="shrink-0 font-mono text-[9px] leading-none text-passive" dateTime={notification.createdAt}>
						{formatTimeCompact(notification.createdAt)}
					</time>
					{offerRestore && sessionId ? (
						<Tooltip delayDuration={0}>
							<TooltipTrigger asChild>
								<button
									aria-label={t("shell.restoreSession")}
									title=""
									className="grid size-notification-icon place-items-center rounded-md text-passive transition-colors hover:bg-interactive-active hover:text-foreground disabled:pointer-events-none disabled:opacity-40"
									disabled={restoreDisabled}
									onClick={(event) => {
										event.stopPropagation();
										onRestore(notification);
									}}
									type="button"
								>
									{restoring ? <Spinner icon={RotateCcw} className="size-icon-md" /> : <RotateCcw className="size-icon-md" aria-hidden="true" />}
								</button>
							</TooltipTrigger>
							<TooltipContent side="top">
								{restoring ? t("shell.restoringSession") : t("shell.restoreSession")}
							</TooltipContent>
						</Tooltip>
					) : null}
					<Tooltip delayDuration={0}>
						<TooltipTrigger asChild>
							<button
								aria-label={t("notify.clearOne", { title: hostLabel ? `${hostLabel}: ${copy.title}` : copy.title })}
								title=""
								className="grid size-notification-icon place-items-center rounded-md text-passive transition-colors hover:bg-interactive-active hover:text-foreground disabled:pointer-events-none disabled:opacity-40"
								disabled={clearDisabled}
								onClick={(event) => {
									event.stopPropagation();
									onClear(notification);
								}}
								onKeyDown={(event) => event.stopPropagation()}
								type="button"
							>
								{clearing ? (
									<Spinner icon={LoaderCircle} className="size-icon-sm" />
								) : (
									<X className="size-icon-sm" aria-hidden="true" />
								)}
							</button>
						</TooltipTrigger>
						<TooltipContent side="top">{t("notify.clearOneShort")}</TooltipContent>
					</Tooltip>
				</div>
			</div>
		</div>
	);
});
