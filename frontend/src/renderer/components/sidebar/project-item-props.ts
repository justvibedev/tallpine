import { type DragEvent as ReactDragEvent } from "react";
import { type WorkspaceSummary } from "../../types/workspace";
import { type Selection } from "./types";

export type ProjectItemProps = {
	workspace: WorkspaceSummary;
	hostLabel?: string;
	onOpenOrchestrator?: () => void;
	expanded: boolean;
	selection: Selection;
	isDragged: boolean;
	projectDragInProgress: boolean;
	consumeDragClick: (id: string) => boolean;
	onToggle: (projectId: string) => void;
	onRemoveProject: (projectId: string) => Promise<void>;
	suppressInitialExpandAnimation: boolean;
	onProjectDragStart: (event: ReactDragEvent<HTMLElement>, projectId: string) => void;
	onProjectDragEnd: () => void;
	onProjectDragOver: (event: ReactDragEvent<HTMLElement>, overId: string) => void;
	onProjectDrop: (event: ReactDragEvent<HTMLElement>) => void;
};

// The part of the route selection one project row renders from. A switch
// between two other projects' sessions leaves it unchanged. The active project
// keys on the raw session id: a just-opened session may not be listed yet.
function projectSelectionKey(workspace: WorkspaceSummary, selection: Selection): string {
	const sessionId = selection.activeSessionId ?? "";
	if (selection.activeProjectId === workspace.id) return `active\u0000${sessionId}`;
	return workspace.sessions.some((session) => session.id === sessionId) ? sessionId : "";
}

// Session switches replace the shared selection object. Re-render a project
// only when its own slice of the selection (or any other prop) changed, so a
// switch does not re-render every project's rows, menus, and dialogs.
export function projectItemPropsEqual(previous: ProjectItemProps, next: ProjectItemProps): boolean {
	const keys = Object.keys(next) as (keyof ProjectItemProps)[];
	if (keys.length !== Object.keys(previous).length) return false;
	for (const key of keys) {
		if (key !== "selection" && !Object.is(previous[key], next[key])) return false;
	}
	const a = previous.selection;
	const b = next.selection;
	return a.goSession === b.goSession && a.goProject === b.goProject && a.goHome === b.goHome &&
		a.goSettings === b.goSettings && projectSelectionKey(previous.workspace, a) === projectSelectionKey(next.workspace, b);
}
