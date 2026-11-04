import { useTranslation } from "react-i18next";
import type { BacklogItem } from "../../lib/backlog-api";
import { activeLinkedSessions } from "../../lib/backlog-link-status";
import type { WorkspaceSummary } from "../../types/workspace";
import { BacklogBadges } from "./BacklogBadges";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "../ui/dropdown-menu";

type BacklogDetailBadgeProps = {
	item: BacklogItem;
	workspaces: readonly WorkspaceSummary[];
	onOpenSession: (sessionId: string) => void;
};

/**
 * The status glyph in the detail header. When a linked session is running it opens
 * that session's chat; with several, a small menu picks one. Rows keep the plain
 * glyph, since a row is already a button.
 */
export function BacklogDetailBadge({ item, workspaces, onOpenSession }: BacklogDetailBadgeProps) {
	const { t } = useTranslation();
	const running = item.badges.active ? activeLinkedSessions(item.links ?? [], workspaces) : [];
	if (running.length === 0) return <BacklogBadges badges={item.badges} />;
	const trigger = (
		<button
			type="button"
			aria-label={running.length === 1 ? t("backlog.openSession", { title: running[0].title }) : t("backlog.openSessionMenu")}
			onClick={running.length === 1 ? () => onOpenSession(running[0].id) : undefined}
			className="rounded p-1 hover:bg-interactive-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
		>
			<BacklogBadges badges={item.badges} />
		</button>
	);
	if (running.length === 1) return trigger;
	return (
		<DropdownMenu>
			<DropdownMenuTrigger asChild>{trigger}</DropdownMenuTrigger>
			<DropdownMenuContent align="end">
				{running.map((session) => <DropdownMenuItem key={session.id} onSelect={() => onOpenSession(session.id)}>{session.title}</DropdownMenuItem>)}
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
