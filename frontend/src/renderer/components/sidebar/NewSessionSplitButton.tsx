import { ChevronDown, Plus } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useUiStore } from "../../stores/ui-store";
import { STANDALONE_WORKSPACE_ID } from "../../types/workspace";
import { Button } from "../ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "../ui/dropdown-menu";
import type { Selection } from "./types";

/**
 * "+ New" in the Sessions panel header. The main button starts a session in the
 * project in scope (same routing as the new-session shortcut); the caret offers
 * a standalone agent or a new project.
 */
export function NewSessionSplitButton({ selection }: { selection: Selection }) {
	const { t } = useTranslation();
	const requestNewTask = useUiStore((state) => state.requestNewTask);
	const requestCreateProject = useUiStore((state) => state.requestCreateProject);
	const startSession = () => {
		if (selection.activeRemoteHostId) {
			requestNewTask(selection.activeRemoteProjectId ?? STANDALONE_WORKSPACE_ID, selection.activeRemoteHostId);
			return;
		}
		requestNewTask(selection.activeProjectId ?? STANDALONE_WORKSPACE_ID);
	};
	return (
		<div className="flex shrink-0 items-center" data-testid="sessions-panel-new">
			<Button className="gap-0.5 rounded-r-none px-1.5" data-testid="sessions-panel-new-button" onClick={startSession} size="sm" type="button">
				<Plus aria-hidden="true" className="size-icon-md" />
				{t("sessionsPanel.new")}
			</Button>
			<DropdownMenu>
				<DropdownMenuTrigger asChild>
					<Button
						aria-label={t("sessionsPanel.newOptions")}
						className="rounded-l-none border-l border-l-primary-foreground/25 px-0.5"
						data-testid="sessions-panel-new-menu"
						size="sm"
						type="button"
					>
						<ChevronDown aria-hidden="true" className="size-icon-md" />
					</Button>
				</DropdownMenuTrigger>
				<DropdownMenuContent align="end" className="min-w-44">
					<DropdownMenuItem onSelect={() => requestNewTask(STANDALONE_WORKSPACE_ID)}>{t("sessionsPanel.newStandalone")}</DropdownMenuItem>
					<DropdownMenuItem onSelect={() => requestCreateProject()}>{t("sessionsPanel.newProject")}</DropdownMenuItem>
				</DropdownMenuContent>
			</DropdownMenu>
		</div>
	);
}
