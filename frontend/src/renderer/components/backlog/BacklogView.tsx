import { useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useBacklogItemById } from "../../hooks/useBacklogItemById";
import { useBacklogItems } from "../../hooks/useBacklogItems";
import { useReorderBacklogItem } from "../../hooks/useReorderBacklogItem";
import { useWorkspaceQuery } from "../../hooks/useWorkspaceQuery";
import { BACKLOG_PAGE_LIMIT, type BacklogItem } from "../../lib/backlog-api";
import { groupBacklog } from "../../lib/backlog-groups";
import { findProjectOrchestrator, STANDALONE_PROJECT_KIND } from "../../types/workspace";
import { Button } from "../ui/button";
import { BacklogDetail } from "./BacklogDetail";
import { BacklogEmptyState } from "./BacklogEmptyState";
import { BacklogList } from "./BacklogList";
import { BacklogRunButton } from "./BacklogRunButton";
import { BacklogRunCard } from "./BacklogRunCard";
import { BacklogToolbar, type BacklogFilter } from "./BacklogToolbar";

export type BacklogViewProps = {
	/** undefined lists every project. */
	projectId: string | undefined;
	/** Id of the selected item. */
	itemId: string | undefined;
	onProjectChange: (projectId: string | undefined) => void;
	onItemChange: (itemId: string | undefined) => void;
	onOpenSession: (projectId: string, sessionId: string) => void;
	/** Single column for the side panel: the list, or the selected item with a Back button. */
	compact?: boolean;
	/** Extra toolbar actions after the Run button (the panel's expand link). */
	toolbarActions?: ReactNode;
};

/** The Backlog page body: list on the left, the selected item on the right. */
export function BacklogView({ projectId, itemId, onProjectChange, onItemChange, onOpenSession, compact = false, toolbarActions }: BacklogViewProps) {
	const { t } = useTranslation();
	const workspaces = useWorkspaceQuery().data ?? [];
	const projects = useMemo(() => workspaces.filter((workspace) => workspace.kind !== STANDALONE_PROJECT_KIND), [workspaces]);
	const scope = useMemo(() => (projectId ? [projectId] : projects.map((project) => project.id)), [projectId, projects]);
	const { items, error, isLoading, truncated, refetch } = useBacklogItems(scope);
	const reorder = useReorderBacklogItem(projectId ?? "");
	const [filter, setFilter] = useState<BacklogFilter>("all");
	const [search, setSearch] = useState("");

	const visibleItems = useMemo(() => {
		const query = search.trim().toLowerCase();
		return query ? items.filter((item) => `${item.ref} ${item.title}`.toLowerCase().includes(query)) : items;
	}, [items, search]);
	const groups = useMemo(() => groupBacklog(visibleItems), [visibleItems]);
	const projectNames = useMemo(() => new Map(projects.map((project) => [project.id, project.name])), [projects]);
	const inLists = items.find((item) => item.id === itemId);
	// An item the lists do not hold (past the page cap, or another project's) is read by id.
	const fetched = useBacklogItemById(scope, itemId, Boolean(itemId) && !inLists && !isLoading);
	const selected: BacklogItem | undefined = inLists ?? fetched;

	return (
		<div className="flex min-h-0 flex-1 bg-background">
			<section aria-label={t("backlog.title")} className={compact ? `@container flex min-h-0 min-w-0 flex-1 flex-col ${selected ? "hidden" : ""}` : `@container flex min-h-0 min-w-0 flex-1 flex-col md:max-w-[560px] md:flex-none md:basis-[45%] md:border-r md:border-border-strong ${selected ? "max-md:hidden" : ""}`}>
				<BacklogToolbar
					projects={projects}
					projectId={projectId}
					filter={filter}
					search={search}
					onProjectChange={onProjectChange}
					onFilterChange={setFilter}
					onSearchChange={setSearch}
					hideScope={compact}
					actions={<>{projectId ? <BacklogRunButton projectId={projectId} selection={{ kind: "all" }} hasOrchestrator={Boolean(findProjectOrchestrator([...workspaces], projectId))} /> : null}{toolbarActions}</>}
				/>
				<div className="min-h-0 flex-1 overflow-y-auto">
					{projectId ? <BacklogRunCard projectId={projectId} onSelectItem={(id) => onItemChange(id)} /> : null}
					{error ? (
						<p role="alert" className="flex items-center gap-2 px-3 py-2 text-sm text-destructive">
							{t("backlog.error")}
							<Button type="button" size="sm" variant="ghost" onClick={() => void refetch()}>{t("backlog.retry")}</Button>
						</p>
					) : null}
					{isLoading && items.length === 0 ? <p role="status" className="px-3 py-8 text-sm text-muted-foreground">{t("backlog.loading")}</p> : null}
					{!isLoading && !error && items.length === 0 ? <BacklogEmptyState kind="none" /> : null}
					{items.length > 0 ? (
						<BacklogList
							groups={groups}
							filter={filter}
							selectedId={selected?.id}
							sortable={Boolean(projectId)}
							projectNames={projectId ? undefined : projectNames}
							onSelect={(item) => onItemChange(item?.id)}
							onReorder={(item, target) => reorder.mutate({ ref: item.ref, target })}
						/>
					) : null}
					{truncated ? <p className="px-3 py-3 text-xs text-muted-foreground">{t("backlog.truncated", { limit: BACKLOG_PAGE_LIMIT })}</p> : null}
				</div>
			</section>
			<div className={`min-h-0 min-w-0 flex-1 flex-col ${selected ? "flex" : compact ? "hidden" : "hidden md:flex"}`}>
				{selected ? (
					<BacklogDetail
						key={selected.id}
						summary={selected}
						workspaces={workspaces}
						compact={compact}
						onBack={() => onItemChange(undefined)}
						onOpenSession={(sessionId) => onOpenSession(selected.projectId, sessionId)}
					/>
				) : null}
			</div>
		</div>
	);
}
