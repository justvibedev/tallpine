import { useCallback } from "react";
import { delegateTask, killSession, launchOrchestrator as apiLaunchOrchestrator, mergePR as apiMergePR, pinSession as apiPinSession, renameSession as apiRenameSession, restoreSession, resumeSessionAgent, sendMessage, spawnSession, unpinSession as apiUnpinSession, type DashboardPR, type ProjectInfo, type SessionMode } from "../api";
import { LaneStoppedError, resolveSpawnTarget } from "../laneSelection";
import { selectedLaneId } from "../laneSelectionStore";
import { type ServerConfig } from "../config";
import { ALL_PROJECTS, STANDALONE_PROJECT } from "../projectFilter";
import { type HostSnapshot } from "../otherHosts";
import { SpawnOptions } from "../store";

export function useHostActions({
	activeProjectId,
	projects,
	requireConfig,
	refreshHost,
	hostStates,
	selectedHostId,
	fetchAll,
}: {
	activeProjectId: string;
	projects: ProjectInfo[];
	requireConfig: (hostId?: string | undefined) => ServerConfig;
	refreshHost: (hostId: string) => Promise<void>;
	hostStates: HostSnapshot[];
	selectedHostId: string | null;
	fetchAll: (afterCurrent?: boolean) => Promise<boolean>;
}) {

	// Pick a sensible project for actions that need one (spawn / conductor).
	const targetProject = useCallback((): string | null => {
		if (activeProjectId !== ALL_PROJECTS) return activeProjectId;
		if (projects.length === 1) return projects[0].id;
		return null;
	}, [activeProjectId, projects]);

	const spawn = useCallback(
		async ({ hostId, projectId, prompt, harness, model, mode, attachments, clientRequestId, useDefaultLane, laneParentId }: SpawnOptions) => {
			const resolvedMode = mode ?? "chat";
			return (async () => {
				const c = requireConfig(hostId);
				if (projectId === STANDALONE_PROJECT) {
					const session = await spawnSession(c, { prompt, harness, model, mode: resolvedMode, attachments, clientRequestId });
					await refreshHost(hostId);
					return session;
				}
				const hostProjects = hostStates.find((host) => host.hostId === hostId)?.projects ?? [];
				const proj = projectId ?? (hostId === selectedHostId ? targetProject() : hostProjects.length === 1 ? hostProjects[0].id : null);
				if (!proj) throw new Error("Pick a project first");
				const laneTarget = resolveSpawnTarget(hostStates.find((host) => host.hostId === hostId)?.orchestrators ?? [], proj, selectedLaneId(hostId, proj), { useDefaultLane, laneParentId });
				if (laneTarget.kind === "stopped") throw new LaneStoppedError(hostId, proj, laneTarget.lane);
				if (laneTarget.kind === "lane") {
					// delegate always goes to the default orchestrator; a plain spawn
					// parented to the lane orchestrator makes the worker inherit that lane.
					const session = await spawnSession(c, { projectId: proj, prompt, harness, model, mode: resolvedMode, attachments, clientRequestId, parentSessionId: laneTarget.id });
					await refreshHost(hostId);
					return session;
				}
				const session = await delegateTask(c, {
					projectId: proj,
					brief: prompt ?? "",
					agent: harness,
					model,
					mode: resolvedMode,
					attachments,
					clientRequestId,
				});
				await refreshHost(hostId);
				return session;
			})();
		},
		[targetProject, requireConfig, hostStates, selectedHostId, refreshHost],
	);

	const launchConductor = useCallback(
		async (projectId: string, clean = false, mode: SessionMode = "chat", hostId?: string, lane?: string) =>
			(async () => {
				const c = requireConfig(hostId);
				const link = await apiLaunchOrchestrator(c, projectId, clean, mode, lane);
				await refreshHost(c.hostId ?? "");
				return link;
			})(),
		[requireConfig, refreshHost],
	);

	const merge = useCallback(
		async (pr: DashboardPR, hostId?: string) =>
			(async () => {
				const c = requireConfig(hostId);
				await apiMergePR(c, pr);
				await refreshHost(c.hostId ?? "");
			})(),
		[requireConfig, refreshHost],
	);

	const kill = useCallback(
		async (id: string, hostId?: string) =>
			(async () => {
				const c = requireConfig(hostId);
				await killSession(c, id);
				await refreshHost(c.hostId ?? "");
			})(),
		[requireConfig, refreshHost],
	);

	const renameWorker = useCallback(
		async (id: string, displayName: string, hostId?: string) => {
			const c = requireConfig(hostId);
			await apiRenameSession(c, id, displayName);
			await refreshHost(c.hostId ?? "");
		},
		[requireConfig, refreshHost],
	);

	const setWorkerPinned = useCallback(
		async (id: string, pinned: boolean, hostId?: string) => {
			const c = requireConfig(hostId);
			await (pinned ? apiPinSession(c, id) : apiUnpinSession(c, id));
			await refreshHost(c.hostId ?? "");
		},
		[requireConfig, refreshHost],
	);

	const restore = useCallback(
		async (id: string, hostId?: string) =>
			(async () => {
				const c = requireConfig(hostId);
				await restoreSession(c, id);
				await refreshHost(c.hostId ?? "");
			})(),
		[requireConfig, refreshHost],
	);

	// Distinct from restore, and the chat screen already relies on the
	// difference: a terminated Tallpine session is restored, a merely stopped
	// agent/controller is resumed without resurrecting the session around it.
	const resumeAgent = useCallback(
		async (id: string, hostId?: string) =>
			(async () => {
				const c = requireConfig(hostId);
				await resumeSessionAgent(c, id);
				await refreshHost(c.hostId ?? "");
			})(),
		[requireConfig, refreshHost],
	);

	const send = useCallback(async (id: string, message: string, hostId?: string) => {
		await sendMessage(requireConfig(hostId), id, message);
	}, [requireConfig]);
	const refresh = useCallback(async () => {
		await fetchAll(true);
	}, [fetchAll]);

	return { spawn, launchConductor, merge, kill, renameWorker, setWorkerPinned, restore, resumeAgent, send, refresh };
}
