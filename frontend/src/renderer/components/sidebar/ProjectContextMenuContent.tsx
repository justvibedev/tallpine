import { useTranslation } from "react-i18next";
import { GitBranch, Pin, PinOff, Plus, Settings, Trash2 } from "lucide-react";
import { type WorkspaceSummary } from "../../types/workspace";
import { ContextMenuContent, ContextMenuItem } from "../ui/context-menu";
import { useUiStore } from "../../stores/ui-store";
import { type Selection } from "./types";
import { useProjectPin } from "./useProjectPin";

export function ProjectContextMenuContent({ workspace, selection, isStandalone, isProjectRestarting, isRemoving, removeProject }: { workspace: WorkspaceSummary; selection: Selection; isStandalone: boolean; isProjectRestarting: boolean; isRemoving: boolean; removeProject: () => void }) {
	const { t } = useTranslation();
	const requestNewTask = useUiStore((state) => state.requestNewTask);
	const requestLane = useUiStore((state) => state.requestLaneOrchestrator);
	const { canPin, isPinned, setPinned } = useProjectPin(workspace, isStandalone);
	return (
		<ContextMenuContent className="min-w-44">
			<ContextMenuItem disabled={isProjectRestarting} onSelect={() => requestNewTask(workspace.id, workspace.hostId)}>
				<Plus aria-hidden="true" />
				{t("shell.newTask")}
			</ContextMenuItem>
			{!isStandalone && <ContextMenuItem disabled={isProjectRestarting} onSelect={() => requestLane(workspace.id)}>
				<GitBranch aria-hidden="true" />
				{t("shell.newLane")}
			</ContextMenuItem>}
			{canPin ? (
				<ContextMenuItem onSelect={() => setPinned(!isPinned)}>
					{isPinned ? <PinOff aria-hidden="true" /> : <Pin aria-hidden="true" />}
					{isPinned ? t("shell.unpinProject") : t("shell.pinProject")}
				</ContextMenuItem>
			) : null}
			{!isStandalone && <ContextMenuItem onSelect={() => selection.goSettings(workspace.id)}>
				<Settings aria-hidden="true" />
				{t("shell.projectSettings")}
			</ContextMenuItem>}
			{!isStandalone && <ContextMenuItem
				className="text-destructive focus:text-destructive [&_svg]:text-destructive focus:[&_svg]:text-destructive"
				disabled={isRemoving}
				onSelect={() => void removeProject()}
			>
				<Trash2 aria-hidden="true" />
				{t("shell.removeProjectTitle")}
			</ContextMenuItem>}
		</ContextMenuContent>
	);
}
