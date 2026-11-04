import { useTranslation } from "react-i18next";
import type { BacklogItem, BacklogLinkKind } from "../../lib/backlog-api";
import { resolveBacklogLinks } from "../../lib/backlog-link-status";
import type { WorkspaceSummary } from "../../types/workspace";
import { BacklogLinkAdd } from "./BacklogLinkAdd";
import { BacklogLinkRow } from "./BacklogLinkRow";

type BacklogLinksProps = {
	item: BacklogItem;
	workspaces: readonly WorkspaceSummary[];
	onAdd: (kind: BacklogLinkKind, target: string) => Promise<unknown>;
	onRemove: (kind: BacklogLinkKind, target: string) => void;
	onOpenSession: (sessionId: string) => void;
};

export function BacklogLinks({ item, workspaces, onAdd, onRemove, onOpenSession }: BacklogLinksProps) {
	const { t } = useTranslation();
	const resolved = resolveBacklogLinks(item.links ?? [], workspaces);
	return (
		<div>
			<span className="text-xs font-medium text-muted-foreground">{t("backlog.detail.links")}</span>
			{resolved.length === 0 ? <p className="text-sm text-muted-foreground">{t("backlog.link.none")}</p> : null}
			{resolved.length > 0 ? (
				<ul role="list" className="divide-y divide-border/40">
					{resolved.map((entry) => (
						<BacklogLinkRow
							key={`${entry.link.kind}:${entry.link.ref}`}
							resolved={entry}
							onOpenSession={onOpenSession}
							onRemove={() => onRemove(entry.link.kind as BacklogLinkKind, entry.link.ref)}
						/>
					))}
				</ul>
			) : null}
			<div className="mt-2">
				<BacklogLinkAdd onAdd={onAdd} />
			</div>
		</div>
	);
}
