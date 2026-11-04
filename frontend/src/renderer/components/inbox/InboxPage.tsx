import { parseChangeRequestUrl } from "@tallpine/shared/format/change-request-url";
import { getRouteApi } from "@tanstack/react-router";
import { ArrowUpRight, Inbox } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useNotificationsQuery } from "../../hooks/useNotificationsQuery";
import { formatTimeCompact } from "../../lib/format-time";
import { useNavigateToSession } from "../../lib/navigate-to-session";
import { getCachedNotifications } from "../../lib/notifications";
import { hidesShellTopbar } from "../../lib/platform";
import { cn } from "../../lib/utils";
import { notificationCopy } from "../NotificationCenter";
import { topbarProjectLabelClass } from "../TopbarButton";
import { Button } from "../ui/button";

const route = getRouteApi("/_shell/inbox");

/** The /inbox route: the notification picked in the left list, with its target one click away. */
export function InboxPage() {
	const { t } = useTranslation();
	const { item } = route.useSearch();
	const navigateToSession = useNavigateToSession();
	const all = useNotificationsQuery("all");
	const notification = item ? getCachedNotifications(all.data).find((entry) => entry.id === item) : undefined;
	const showPageTitle = hidesShellTopbar();
	const sessionId = notification ? notification.target.sessionId || notification.sessionId : undefined;
	const prUrl = notification?.target.kind === "pr" ? notification.target.prUrl : undefined;
	const copy = notification ? notificationCopy(notification) : undefined;
	return (
		<div className="flex min-h-0 flex-1 flex-col bg-background" data-testid="inbox-page">
			<header className="workspace-topbar-container center-panel-titlebar flex h-toolbar shrink-0 items-center gap-3 border-b border-border-strong pr-2">
				<h1 className={showPageTitle ? cn(topbarProjectLabelClass, "inline-flex items-center gap-1.5") : "sr-only"}>
					{showPageTitle ? <Inbox className="size-icon-md" aria-hidden="true" /> : null}
					{t("shellNav.inbox")}
				</h1>
			</header>
			{notification && copy ? (
				<article className="mx-auto flex w-full max-w-[640px] flex-col gap-3 px-6 py-8" data-testid="inbox-detail">
					<time className="font-mono text-caption text-passive" dateTime={notification.createdAt}>{formatTimeCompact(notification.createdAt)}</time>
					<h2 className="text-lg font-medium text-foreground">{copy.title}</h2>
					{copy.body ? <p className="whitespace-pre-wrap text-control text-muted-foreground">{copy.body}</p> : null}
					<div className="flex gap-2 pt-2">
						{sessionId ? <Button onClick={() => navigateToSession(notification.projectId, sessionId)} size="sm">{t("notify.openSessionTitle")}</Button> : null}
						{prUrl ? (
							<Button onClick={() => window.open(prUrl, "_blank", "noopener,noreferrer")} size="sm" variant="outline">
								{t("inspector.openPR", { number: parseChangeRequestUrl(prUrl)?.number ?? "" })}
								<ArrowUpRight aria-hidden="true" className="size-icon-sm" />
							</Button>
						) : null}
					</div>
				</article>
			) : (
				<p className="m-auto text-control text-muted-foreground" data-testid="inbox-empty">{t("inbox.select")}</p>
			)}
		</div>
	);
}
