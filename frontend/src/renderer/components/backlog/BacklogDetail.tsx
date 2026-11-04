import type { MessageKey } from "../../i18n/messages";
import { ArrowLeft } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { BacklogItem, BacklogLinkKind } from "../../lib/backlog-api";
import { useAddBacklogLink } from "../../hooks/useAddBacklogLink";
import { useBacklogItem } from "../../hooks/useBacklogItem";
import { useClearBacklogField } from "../../hooks/useClearBacklogField";
import { useRemoveBacklogLink } from "../../hooks/useRemoveBacklogLink";
import { useSetBacklogField } from "../../hooks/useSetBacklogField";
import { useUpdateBacklogItem } from "../../hooks/useUpdateBacklogItem";
import { findProjectOrchestrator, type WorkspaceSummary } from "../../types/workspace";
import { Button } from "../ui/button";
import { BacklogActions } from "./BacklogActions";
import { BacklogAgentNotes } from "./BacklogAgentNotes";
import { BacklogDetailBadge } from "./BacklogDetailBadge";
import { BacklogEditableText } from "./BacklogEditableText";
import { BacklogFields } from "./BacklogFields";
import { BacklogLinks } from "./BacklogLinks";
import { BacklogItemUsage } from "./BacklogItemUsage";
import { BacklogOrigin } from "./BacklogOrigin";
import { BacklogPlanSection } from "./BacklogPlanSection";
import { BacklogRunButton } from "./BacklogRunButton";
import { BacklogScopeSelect } from "./BacklogScopeSelect";

type BacklogDetailProps = {
	/** The list row; shown at once while the full item (with links) loads. */
	summary: BacklogItem;
	workspaces: readonly WorkspaceSummary[];
	onBack: () => void;
	onOpenSession: (sessionId: string) => void;
	/** Side-panel layout: the Back button stays at every width. */
	compact?: boolean;
};

/** The right pane: everything about one item, editable in place. */
export function BacklogDetail({ summary, workspaces, onBack, onOpenSession, compact = false }: BacklogDetailProps) {
	const { t } = useTranslation();
	const full = useBacklogItem(summary.projectId, summary.ref);
	const item = full.data ?? summary;
	const update = useUpdateBacklogItem(item.projectId);
	const addLink = useAddBacklogLink(item.projectId);
	const removeLink = useRemoveBacklogLink(item.projectId);
	const setField = useSetBacklogField(item.projectId);
	const clearField = useClearBacklogField(item.projectId);
	const patch = (body: Parameters<typeof update.mutateAsync>[0]["body"]) => update.mutateAsync({ ref: item.ref, body });
	const blockers = (item.links ?? []).filter((link) => link.kind === "blocked_by");

	return (
		<article aria-label={`${item.ref} ${item.title}`} className="flex min-h-0 flex-1 flex-col">
			<header className="flex items-center gap-2 border-b border-border-strong px-4 py-2">
				<Button type="button" variant="ghost" size="icon-sm" aria-label={t("backlog.back")} onClick={onBack} className={compact ? undefined : "md:hidden"}>
					<ArrowLeft aria-hidden="true" className="size-4" />
				</Button>
				<span className="text-xs tabular-nums text-muted-foreground">{item.ref}</span>
				<span className="text-xs text-muted-foreground">· {t(`backlog.state.${item.state}` as MessageKey)}</span>
				{item.parentRef ? <span className="text-xs text-muted-foreground">· {t("backlog.parent", { ref: item.parentRef })}</span> : null}
				<span className="ml-auto"><BacklogDetailBadge item={item} workspaces={workspaces} onOpenSession={onOpenSession} /></span>
			</header>
			<div className="min-h-0 flex-1 space-y-5 overflow-y-auto px-4 py-4">
				<BacklogEditableText heading label={t("backlog.detail.title")} value={item.title} maxLength={200} onSave={(title) => patch({ title })} />
				{item.hold !== "" ? <p className="text-sm text-status-in-review">{t("backlog.holdLabel", { reason: item.hold })}</p> : null}
				{blockers.length > 0 ? <p className="text-sm text-muted-foreground">{t("backlog.blockedBy", { refs: blockers.map((link) => link.display).join(", ") })}</p> : null}
				<BacklogActions item={item} workspaces={workspaces} onOpenSession={onOpenSession}>
					{item.state === "open" && item.childCount > 0 ? (
						<BacklogRunButton projectId={item.projectId} variant="ghost" selection={{ kind: "parent", ref: item.ref }} hasOrchestrator={Boolean(findProjectOrchestrator([...workspaces], item.projectId))} />
					) : null}
				</BacklogActions>
				<BacklogItemUsage projectId={item.projectId} itemRef={item.ref} />
				<BacklogPlanSection item={item} />
				<BacklogEditableText label={t("backlog.detail.note")} value={item.note} multiline markdown maxLength={20000} emptyLabel={t("backlog.detail.noteEmpty")} onSave={(note) => patch({ note })} />
				<BacklogEditableText label={t("backlog.detail.doneWhen")} value={item.doneWhen} multiline maxLength={2000} emptyLabel={t("backlog.detail.doneWhenEmpty")} onSave={(doneWhen) => patch({ doneWhen })} />
				<BacklogScopeSelect value={item.scope ?? ""} onChange={(scope) => void patch({ scope })} />
				<BacklogLinks
					item={item}
					workspaces={workspaces}
					onAdd={(kind: BacklogLinkKind, target) => addLink.mutateAsync({ ref: item.ref, kind, target })}
					onRemove={(kind, target) => void removeLink.mutateAsync({ ref: item.ref, kind, target })}
					onOpenSession={onOpenSession}
				/>
				<BacklogOrigin item={item} />
				<BacklogAgentNotes notes={item.agentNotes} onSave={(agentNotes) => patch({ agentNotes })} />
				<BacklogFields
					fields={item.fields ?? {}}
					onSet={(key, value) => setField.mutateAsync({ ref: item.ref, key, value })}
					onClear={(key) => clearField.mutateAsync({ ref: item.ref, key })}
				/>
			</div>
		</article>
	);
}
