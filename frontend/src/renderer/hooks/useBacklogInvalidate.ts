import { useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import { backlogInboxCountsKey, backlogProjectKey } from "../lib/backlog-api";

/** Refetches a project's backlog views and the sidebar's inbox count after a write. */
export function useBacklogInvalidate(projectId: string) {
	const client = useQueryClient();
	return useCallback(async () => {
		await Promise.all([
			client.invalidateQueries({ queryKey: backlogProjectKey(projectId) }),
			client.invalidateQueries({ queryKey: backlogInboxCountsKey }),
		]);
	}, [client, projectId]);
}
