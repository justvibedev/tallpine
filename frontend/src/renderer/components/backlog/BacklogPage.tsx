import { getRouteApi, useNavigate } from "@tanstack/react-router";
import { ListTodo } from "lucide-react";
import { useTranslation } from "react-i18next";
import { hidesShellTopbar } from "../../lib/platform";
import { cn } from "../../lib/utils";
import { topbarProjectLabelClass } from "../TopbarButton";
import { BacklogView } from "./BacklogView";

const route = getRouteApi("/_shell/backlog");

/** The /backlog route: the page header plus the view, with project and selection kept in the URL. */
export function BacklogPage() {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const { project, item } = route.useSearch();
	const showPageTitle = hidesShellTopbar();
	return (
		<div className="flex min-h-0 flex-1 flex-col bg-background">
			<header className="workspace-topbar-container center-panel-titlebar flex h-toolbar shrink-0 items-center gap-3 border-b border-border-strong pr-2">
				<h1 className={showPageTitle ? cn(topbarProjectLabelClass, "inline-flex items-center gap-1.5") : "sr-only"}>
					{showPageTitle ? <ListTodo className="size-icon-md" aria-hidden="true" /> : null}
					{t("backlog.title")}
				</h1>
			</header>
			<BacklogView
				projectId={project}
				itemId={item}
				onProjectChange={(next) => void navigate({ to: "/backlog", search: { project: next }, replace: true })}
				onItemChange={(next) => void navigate({ to: "/backlog", search: (previous: { project?: string }) => ({ project: previous.project, item: next }), replace: true })}
				onOpenSession={(projectId, sessionId) => void navigate({ to: "/projects/$projectId/sessions/$sessionId", params: { projectId, sessionId } })}
			/>
		</div>
	);
}
