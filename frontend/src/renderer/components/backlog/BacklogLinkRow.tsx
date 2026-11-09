import type { MessageKey } from "../../i18n/messages";
import { X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { getSessionStatusView } from "../../lib/session-presentation";
import type { ResolvedBacklogLink } from "../../lib/backlog-link-status";
import { openLinkInSystemBrowser } from "../../lib/external-link-policy";
import { cn } from "../../lib/utils";
import { Button } from "../ui/button";

type BacklogLinkRowProps = {
	resolved: ResolvedBacklogLink;
	onOpenSession: (sessionId: string) => void;
	onRemove: () => void;
};

/**
 * One link with the live status the app already knows. Sessions and PRs the
 * app cannot find (deleted, or not reported) fall back to their display text.
 */
export function BacklogLinkRow({ resolved, onOpenSession, onRemove }: BacklogLinkRowProps) {
	const { t } = useTranslation();
	const { link, session, pr } = resolved;
	const status = session ? getSessionStatusView(session.status, t) : undefined;
	const label = session ? session.title : pr ? `#${pr.number}` : link.display;
	return (
		<li className="group/link flex min-h-8 items-center gap-2 text-sm">
			<span className="w-20 shrink-0 text-xs text-muted-foreground">{t(`backlog.link.kind.${link.kind}` as MessageKey)}</span>
			{link.kind === "session" && session ? (
				<button type="button" onClick={() => onOpenSession(session.id)} className="min-w-0 truncate text-left hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60">
					{label}
				</button>
			) : link.kind === "pr" && /^https?:\/\//.test(link.ref) ? (
				<button type="button" onClick={() => void openLinkInSystemBrowser(link.ref)} className="min-w-0 truncate text-left hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60">
					{pr ? t("backlog.pr.label", { number: pr.number }) : link.display}
				</button>
			) : (
				<span className="min-w-0 truncate">{label}</span>
			)}
			{status ? <span className={cn("shrink-0 text-xs", status.className)}>{status.label}</span> : null}
			{pr ? <span className="shrink-0 text-xs text-muted-foreground">{t(`backlog.pr.state.${pr.state}` as MessageKey)}</span> : null}
			<Button
				type="button"
				variant="ghost"
				size="icon-sm"
				aria-label={t("backlog.link.remove", { target: link.display })}
				onClick={onRemove}
				className="ml-auto opacity-0 focus-visible:opacity-100 group-hover/link:opacity-100"
			>
				<X aria-hidden="true" className="size-3.5" />
			</Button>
		</li>
	);
}
