import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useState } from "react";
import { hasConfiguredOrchestratorAgent, newestActiveOrchestrator, type WorkspaceSummary } from "../../types/workspace";
import { useCanResumeAgent } from "../../hooks/useCanResumeAgent";
import { workspaceQueryKey } from "../../hooks/useWorkspaceQuery";
import { resumeOrchestrator, spawnOrchestrator } from "../../lib/spawn-orchestrator";
import { useUiStore } from "../../stores/ui-store";
import { type Selection } from "./types";

export function useProjectOrchestrator({ workspace, selection, expanded, toggleDisclosure, onOpenOrchestrator, isProjectProvisioning, isProjectRestarting }: { workspace: WorkspaceSummary; selection: Selection; expanded: boolean; toggleDisclosure: () => void; onOpenOrchestrator?: () => void; isProjectProvisioning: boolean; isProjectRestarting: boolean }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const [isSpawning, setIsSpawning] = useState(false);
	// The project's live orchestrator (if any) backs the hover Orchestrator
	// button: navigate to it when present, otherwise spawn one first.
	const orchestrator = newestActiveOrchestrator(workspace.sessions);
	const canResumeOrchestrator = useCanResumeAgent(orchestrator, workspace.hostId);

	// Mirrors ShellTopbar's launcher: attach to the running orchestrator, or
	// spawn one via the daemon and follow it once the workspace refetches.
	// Expand a collapsed project so opening the orchestrator also reveals its
	// session list — otherwise the tree stays shut while you're inside it.
	const openOrchestrator = async () => {
		if (isSpawning || isProjectProvisioning || isProjectRestarting) return;
		if (!expanded) toggleDisclosure();
		if (onOpenOrchestrator) {
			onOpenOrchestrator();
			return;
		}
		if (orchestrator) {
			// Mirrors useProjectOrchestratorAction; both launchers must stay in step.
			if (canResumeOrchestrator) {
				setIsSpawning(true);
				try {
					// Start the resume, then open the chat while it runs; its
					// composer shows the startup shimmer in the meantime.
					const resume = resumeOrchestrator(orchestrator.id);
					selection.goSession(workspace.id, orchestrator.id);
					await resume;
					await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
					return;
				} catch (err) {
					console.error("Failed to resume orchestrator:", err);
					showGlobalToast(
						t("inspector.resumeAgent"),
						err instanceof Error ? err.message : t("shell.couldNotSpawn"),
						"error",
					);
					return;
				} finally {
					setIsSpawning(false);
				}
			}
			selection.goSession(workspace.id, orchestrator.id);
			return;
		}
		if (!hasConfiguredOrchestratorAgent(workspace)) {
			selection.goSettings(workspace.id);
			return;
		}
		setIsSpawning(true);
		try {
			const sessionId = await spawnOrchestrator(workspace.id, "sidebar");
			await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
			selection.goSession(workspace.id, sessionId);
		} catch (err) {
			console.error("Failed to spawn orchestrator:", err);
		} finally {
			setIsSpawning(false);
		}
	};

	return { orchestrator, isSpawning, openOrchestrator };
}
