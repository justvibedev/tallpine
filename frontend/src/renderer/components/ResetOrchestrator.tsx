import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { askOrchestratorToHandOff, handoffOrchestratorWithLatestNote } from "../lib/handoff-orchestrator";
import { useUiStore } from "../stores/ui-store";
import { ConfirmDialog } from "./ConfirmDialog";

/**
 * "Reset orchestrator" for an orchestrator's tab menus: confirm, then replace it with a fresh
 * orchestrator through the handoff path (never native /clear), so the new one loads Tallpine's
 * system prompt again and picks up the state note, intents and workers. With no state note yet,
 * the running orchestrator is asked to write one and hand off itself.
 *
 * `dialog` must be rendered outside the menu content, which unmounts when the menu closes.
 */
export function useResetOrchestrator(sessionId: string, hostId?: string): { request: () => void; dialog: ReactNode } {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const navigate = useNavigate();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const [open, setOpen] = useState(false);
	const [busy, setBusy] = useState(false);
	const reset = async () => {
		setBusy(true);
		try {
			const outcome = await handoffOrchestratorWithLatestNote(sessionId, hostId);
			if (outcome.kind === "noNote") {
				await askOrchestratorToHandOff(sessionId, hostId);
				showGlobalToast(t("orchestratorHandoff.asked"), t("orchestratorHandoff.askedDetail"), "info");
			} else {
				await queryClient.invalidateQueries({ queryKey: workspaceQueryKey }).catch(() => {});
				void navigate({
					to: "/projects/$projectId/sessions/$sessionId",
					params: { projectId: outcome.projectId, sessionId: outcome.sessionId },
				});
			}
			setOpen(false);
		} catch (error) {
			setOpen(false);
			showGlobalToast(t("orchestratorHandoff.resetFailed"), error instanceof Error ? error.message : String(error), "error");
		} finally {
			setBusy(false);
		}
	};
	const dialog = (
		<ConfirmDialog
			open={open}
			title={t("orchestratorHandoff.resetTitle")}
			description={t("orchestratorHandoff.resetDescription")}
			confirmLabel={t("orchestratorHandoff.reset")}
			busy={busy}
			onConfirm={() => void reset()}
			onOpenChange={(next) => !busy && setOpen(next)}
		/>
	);
	return { request: () => setOpen(true), dialog };
}
