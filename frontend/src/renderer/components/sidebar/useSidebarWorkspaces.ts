import { replaceEqualDeep } from "@tanstack/react-query";
import { useMemo, useRef } from "react";
import { type WorkspaceSummary, withoutSidebarChurn } from "../../types/workspace";

export const NO_WORKSPACES: WorkspaceSummary[] = [];

/** `withoutSidebarChurn`, sharing every unchanged project and session with the previous render. */
export function useSidebarWorkspaces(workspaces: WorkspaceSummary[]): WorkspaceSummary[] {
	const previous = useRef<WorkspaceSummary[] | undefined>(undefined);
	return useMemo(() => {
		const next = replaceEqualDeep(previous.current, withoutSidebarChurn(workspaces));
		previous.current = next;
		return next;
	}, [workspaces]);
}
