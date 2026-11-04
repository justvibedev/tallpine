import { GitMerge, GitPullRequest, Loader2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { BacklogItem } from "../../lib/backlog-api";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import { Spinner } from "../ui/spinner";

/**
 * One status glyph in the row's status slot (DESIGN section 5): a running
 * session wins, then an open PR, then a landed one. The glyph carries a text
 * alternative; it is never the only signal.
 */
export function BacklogBadges({ badges }: { badges: BacklogItem["badges"] }) {
	const { t } = useTranslation();
	const glyph = badges.active
		? { label: t("backlog.badge.active"), icon: <Spinner icon={Loader2} className="size-3.5 text-status-working" /> }
		: badges.inReview
			? { label: t("backlog.badge.inReview"), icon: <GitPullRequest aria-hidden="true" className="size-3.5 text-status-in-review" /> }
			: badges.landed
				? { label: t("backlog.badge.landed"), icon: <GitMerge aria-hidden="true" className="size-3.5 text-status-merged" /> }
				: null;
	if (!glyph) return null;
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<span role="img" aria-label={glyph.label} className="inline-flex shrink-0 items-center">
					{glyph.icon}
				</span>
			</TooltipTrigger>
			<TooltipContent>{glyph.label}</TooltipContent>
		</Tooltip>
	);
}
