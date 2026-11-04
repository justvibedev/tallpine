import type { UseQueryResult } from "@tanstack/react-query";
import type { DaemonStatus } from "../lib/daemon-status";
import type { QueryClient } from "@tanstack/react-query";
import { isCancelledError } from "@tanstack/react-query";
import { useEffect } from "react";
import { workspaceQueryKey, workspaceQueryOptions } from "../hooks/useWorkspaceQuery";
import { usesPreviewWorkspaceData } from "../lib/preview-mode";
import { type WorkspaceSummary } from "../types/workspace";

export function useWorkspaceStartup({ queryClient, workspaceQuery, daemonStatus, workspaceStartupState, setWorkspaceStartupState, workspaceStartupBaselineRef }: {
	queryClient: QueryClient;
	workspaceQuery: UseQueryResult<NoInfer<WorkspaceSummary[]>, Error>;
	daemonStatus: DaemonStatus;
	workspaceStartupState: "error" | "ready" | "loading";
	setWorkspaceStartupState: React.Dispatch<React.SetStateAction<"error" | "ready" | "loading">>;
	workspaceStartupBaselineRef: React.RefObject<number>;
}) {
	// A daemon port is not enough to render a trustworthy empty state: the
	// route loader may have cached [] before Electron reported the port. Fetch
	// against each ready daemon before the board decides between projects and the
	// first-run import flow. Per-session recovery renders on the cards themselves.
	useEffect(() => {
		let active = true;
		if (usesPreviewWorkspaceData) {
			workspaceStartupBaselineRef.current = 0;
			setWorkspaceStartupState("ready");
			return () => {
				active = false;
			};
		}
		if (daemonStatus.state !== "ready" || !daemonStatus.port) {
			workspaceStartupBaselineRef.current = 0;
			setWorkspaceStartupState("loading");
			return () => {
				active = false;
			};
		}

		workspaceStartupBaselineRef.current =
			queryClient.getQueryState(workspaceQueryKey)?.dataUpdatedAt ?? 0;
		setWorkspaceStartupState("loading");
		void queryClient
			.fetchQuery({ ...workspaceQueryOptions, staleTime: 0 })
			.then(() => {
				if (active) setWorkspaceStartupState("ready");
			})
			.catch((error) => {
				if (active && !isCancelledError(error)) setWorkspaceStartupState("error");
			});

		return () => {
			active = false;
		};
	}, [daemonStatus.port, daemonStatus.state, queryClient]);

	// The first confirmed fetch may fail transiently even though the daemon is
	// ready. React Query keeps polling and the event transport may invalidate
	// the workspace query later, so let a newer successful result recover the
	// shell without requiring a daemon restart or port change.
	useEffect(() => {
		if (
			usesPreviewWorkspaceData ||
			daemonStatus.state !== "ready" ||
			workspaceStartupState === "ready" ||
			!workspaceQuery.isSuccess ||
			workspaceQuery.dataUpdatedAt <= workspaceStartupBaselineRef.current
		) {
			return;
		}
		setWorkspaceStartupState("ready");
	}, [
		daemonStatus.state,
		workspaceQuery.dataUpdatedAt,
		workspaceQuery.isSuccess,
		workspaceStartupState,
	]);
}
