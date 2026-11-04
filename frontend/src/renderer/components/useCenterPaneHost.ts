import { useCallback, useMemo } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useHostConnection } from "../hooks/useHostConnection";
import { workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { createTerminalMux, muxUrlFromApiBase } from "../lib/terminal-mux";

// Workspace refresh and, for a remote host, its terminal mux connection.
export function useCenterPaneHost(hostId: string | undefined) {
	const queryClient = useQueryClient();
	const refreshWorkspaces = useCallback(
		() => queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) }),
		[hostId, queryClient],
	);
	const { baseUrl: remoteBase } = useHostConnection(hostId);
	const remoteCreateMux = useMemo(
		() => remoteBase ? () => createTerminalMux(muxUrlFromApiBase(remoteBase)) : undefined,
		[remoteBase],
	);
	return { refreshWorkspaces, remoteBase, remoteCreateMux };
}
