import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { restartProjectOrchestrator } from "../lib/restart-orchestrator";
import { useUiStore } from "../stores/ui-store";
import { Button } from "./ui/button";

/** Replaces the project's orchestrator with a fresh session (the board's restart path), so it reads newly committed files. */
export function RestartOrchestratorButton({ projectId }: { projectId: string }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const navigate = useNavigate();
	const setProjectRestarting = useUiStore((s) => s.setProjectRestarting);
	const setOrchestratorReplacementError = useUiStore((s) => s.setOrchestratorReplacementError);
	const restarting = useUiStore((s) => s.restartingProjectIds.has(projectId));
	return <Button size="sm" type="button" disabled={restarting} onClick={() => void restartProjectOrchestrator({ projectId, queryClient, navigate, setProjectRestarting, setOrchestratorReplacementError })}>
		{t(restarting ? "skillsCommit.restarting" : "skillsCommit.restart")}
	</Button>;
}
