import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { Repeat2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { askOrchestratorToHandOff, handoffOrchestratorWithLatestNote } from "../lib/handoff-orchestrator";
import { useUiStore } from "../stores/ui-store";
import { DropdownMenuItem } from "./ui/dropdown-menu";

/**
 * Orchestrator tab menu entries that replace the orchestrator with a fresh one.
 * The first asks the orchestrator to write its state note and hand off itself;
 * the second hands off now from its newest note. Workers keep running either way.
 */
export function OrchestratorHandoffMenuItems({ sessionId, hostId, disabled }: { sessionId: string; hostId?: string; disabled?: boolean }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const navigate = useNavigate();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const fail = (error: unknown) => showGlobalToast(t("orchestratorHandoff.failed"), error instanceof Error ? error.message : String(error), "error");
	const ask = () => {
		void askOrchestratorToHandOff(sessionId, hostId)
			.then(() => showGlobalToast(t("orchestratorHandoff.asked"), t("orchestratorHandoff.askedDetail"), "info"))
			.catch(fail);
	};
	const handOffNow = () => {
		void handoffOrchestratorWithLatestNote(sessionId, hostId)
			.then(async (outcome) => {
				if (outcome.kind === "noNote") {
					showGlobalToast(t("orchestratorHandoff.noNote"), t("orchestratorHandoff.noNoteDetail"), "info");
					return;
				}
				await queryClient.invalidateQueries({ queryKey: workspaceQueryKey }).catch(() => {});
				void navigate({
					to: "/projects/$projectId/sessions/$sessionId",
					params: { projectId: outcome.projectId, sessionId: outcome.sessionId },
				});
			})
			.catch(fail);
	};
	return (
		<>
			<DropdownMenuItem disabled={disabled} onSelect={ask}>
				<Repeat2 aria-hidden="true" className="size-icon-lg" />
				{t("orchestratorHandoff.menu")}
			</DropdownMenuItem>
			<DropdownMenuItem disabled={disabled} onSelect={handOffNow}>
				<Repeat2 aria-hidden="true" className="size-icon-lg" />
				{t("orchestratorHandoff.menuLatestNote")}
			</DropdownMenuItem>
		</>
	);
}
