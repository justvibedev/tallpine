import type { MessageKey } from "../../i18n/messages";
import { useTranslation } from "react-i18next";
import type { BacklogItem } from "../../lib/backlog-api";
import { formatBacklogAge } from "../../lib/backlog-age";

/** Who filed the item and when, with the text it came from as a quote. */
export function BacklogOrigin({ item }: { item: BacklogItem }) {
	const { t, i18n } = useTranslation();
	const who = t(`backlog.origin.${item.origin.kind}` as MessageKey);
	return (
		<div>
			<span className="text-xs font-medium text-muted-foreground">{t("backlog.detail.origin")}</span>
			<p className="text-sm">
				{who}
				{item.origin.sessionId ? <span className="text-muted-foreground"> · {item.origin.sessionId}</span> : null}
				<span className="text-muted-foreground"> · {formatBacklogAge(item.createdAt, i18n.language)}</span>
			</p>
			{item.origin.excerpt ? (
				<blockquote className="mt-1 border-l-2 border-border-strong pl-3 text-sm text-muted-foreground">{item.origin.excerpt}</blockquote>
			) : null}
		</div>
	);
}
