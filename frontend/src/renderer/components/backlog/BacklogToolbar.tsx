import type { MessageKey } from "../../i18n/messages";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import { Input } from "../ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../ui/select";

export const BACKLOG_FILTERS = ["all", "inbox", "open", "held", "archive"] as const;
export type BacklogFilter = (typeof BACKLOG_FILTERS)[number];

type BacklogToolbarProps = {
	projects: ReadonlyArray<{ id: string; name: string }>;
	/** undefined = every project. */
	projectId: string | undefined;
	filter: BacklogFilter;
	search: string;
	onProjectChange: (projectId: string | undefined) => void;
	onFilterChange: (filter: BacklogFilter) => void;
	onSearchChange: (search: string) => void;
	/** Page actions, at the trailing edge. */
	actions?: ReactNode;
	/** Hides the project select when the host fixes the scope (the side panel). */
	hideScope?: boolean;
};

const ALL = "__all__";

export function BacklogToolbar({ projects, projectId, filter, search, onProjectChange, onFilterChange, onSearchChange, actions, hideScope }: BacklogToolbarProps) {
	const { t } = useTranslation();
	return (
		<div className="flex flex-wrap items-center gap-2 px-3 py-2">
			{hideScope ? null : <Select value={projectId ?? ALL} onValueChange={(value) => onProjectChange(value === ALL ? undefined : value)}>
				<SelectTrigger size="sm" aria-label={t("backlog.scope.label")} className="max-w-48">
					<SelectValue />
				</SelectTrigger>
				<SelectContent>
					<SelectItem value={ALL}>{t("backlog.scope.all")}</SelectItem>
					{projects.map((project) => <SelectItem key={project.id} value={project.id}>{project.name}</SelectItem>)}
				</SelectContent>
			</Select>}
			<div className="flex flex-wrap items-center gap-1" role="group" aria-label={t("backlog.filter.label")}>
				{BACKLOG_FILTERS.map((value) => (
					<button
						key={value}
						type="button"
						aria-pressed={filter === value}
						onClick={() => onFilterChange(value)}
						className={cn(
							"inline-flex h-7 items-center rounded-md px-2 text-xs @md:px-2.5 transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60",
							filter === value ? "bg-muted text-foreground" : "text-muted-foreground hover:bg-muted/50 hover:text-foreground",
						)}
					>
						{t(`backlog.filter.${value}` as MessageKey)}
					</button>
				))}
			</div>
			<Input
				type="search"
				value={search}
				onChange={(event) => onSearchChange(event.target.value)}
				aria-label={t("backlog.search.label")}
				placeholder={t("backlog.search.placeholder")}
				className="h-7 min-w-32 flex-1 text-xs"
			/>
			{actions}
		</div>
	);
}
