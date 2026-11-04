import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { InspectorSection as Section } from "@tallpine/product-ui";
import { Archive } from "lucide-react";
import { apiErrorMessage } from "../../lib/api-client";
import { clientForSessionHost } from "../../lib/host-clients";
import { sessionUiKey } from "../../lib/hosts";
import { projectBoardNavigateTarget, sessionNavigateTarget } from "../../lib/navigate-to-session";
import { workspaceQueryKeyForHost } from "../../hooks/useWorkspaceQuery";
import { clearTerminateSessionState, useTerminateSession } from "../../hooks/useTerminateSession";
import type { WorkspaceSession, WorkspaceSummary } from "../../types/workspace";
import { resolveNextNavigationAfterSessionKill, STANDALONE_WORKSPACE_ID } from "../../types/workspace";
import { SessionArchiveDialog, type SessionArchiveChoice } from "../SessionArchiveDialog";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import { usesPreviewWorkspaceData as usePreviewData } from "../../lib/preview-mode";
import { InspectorPolicyRow } from "./InspectorPolicyRow";
import { SessionFenceGapLine } from "./SessionFenceGapLine";
import { AutoInjectCIPolicyControl, AutoInjectReviewPolicyControl } from "./AutoInjectPolicyControls";

export function SessionControls({ session, hostId }: { session: WorkspaceSession; hostId?: string }) {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const workspaceKey = workspaceQueryKeyForHost(hostId);
	const [confirmOpen, setConfirmOpen] = useState(false);
	const terminate = useTerminateSession();
	const policy = useMutation({
		mutationFn: async (terminateOnPrMerge: boolean) => {
			if (usePreviewData) return;
			const { error, response } = await clientForSessionHost(hostId).PATCH("/api/v1/sessions/{sessionId}/merge-policy", {
				params: { path: { sessionId: session.id } },
				body: { terminateOnPrMerge },
			});
			if (error) throw new Error(apiErrorMessage(error, `Failed to update merge policy (${response.status})`));
		},
		onMutate: async (terminateOnPrMerge) => {
			await queryClient.cancelQueries({ queryKey: workspaceKey });
			const previous = queryClient.getQueryData<WorkspaceSummary[]>(workspaceKey);
			queryClient.setQueryData<WorkspaceSummary[]>(workspaceKey, (current) =>
				updateSessionMergePolicy(current, session.id, terminateOnPrMerge),
			);
			return { previous };
		},
		onError: (_error, _next, context) => {
			if (context?.previous) queryClient.setQueryData(workspaceKey, context.previous);
		},
		onSettled: () => {
			void queryClient.invalidateQueries({ queryKey: workspaceKey });
		},
	});
	const policyError = policy.error instanceof Error ? policy.error.message : null;
	const canTerminateNow = session.status === "merged";
	const isStandaloneSession = session.workspaceId === STANDALONE_WORKSPACE_ID;

	const confirmTermination = ({ deleteArtifacts }: SessionArchiveChoice) => {
		const workspaces = queryClient.getQueryData<WorkspaceSummary[]>(workspaceKey) ?? [];
		const workspace = workspaces.find((w) => w.id === session.workspaceId);
		const nextNav = resolveNextNavigationAfterSessionKill(workspace, session.id);
		
		setConfirmOpen(false);
		terminate.mutate(hostId ? { ...session, hostId, deleteArtifacts } : { ...session, deleteArtifacts });
		
		if (nextNav.target === "session") {
			void navigate(sessionNavigateTarget(session.workspaceId, nextNav.sessionId, hostId));
		} else {
			if (session.workspaceId === STANDALONE_WORKSPACE_ID) {
				void navigate({ to: "/" });
				return;
			}
			void navigate(projectBoardNavigateTarget(session.workspaceId, hostId));
		}
	};

	if (session.isTerminated === true) return null;

	const terminateAction = (
		<div className="flex items-center justify-between gap-3 py-1">
			<span className="min-w-0 text-sm text-settings-label">{t("inspector.archiveShort")}</span>
			<Tooltip>
				<TooltipTrigger asChild>
					<span className="inline-flex">
						<SessionArchiveDialog
							onConfirm={confirmTermination}
							onOpenChange={setConfirmOpen}
							open={confirmOpen}
							session={session}
							trigger={
								<button
									aria-label={t("inspector.archive")}
									className="inline-flex size-control-md items-center justify-center rounded-sm text-passive transition-colors hover:bg-interactive-hover hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
									onClick={() => {
										clearTerminateSessionState(queryClient, session.id, hostId);
										// Always open the confirm; the modal owns its own dismissal.
										setConfirmOpen(true);
									}}
									type="button"
								>
									<Archive className="size-icon-sm" aria-hidden="true" />
								</button>
							}
						/>
					</span>
				</TooltipTrigger>
				<TooltipContent side="bottom">{t("inspector.archive")}</TooltipContent>
			</Tooltip>
		</div>
	);

	if (isStandaloneSession) {
		return (
			<Section title={t("inspector.sessionControls")}>
				{terminateAction}
			</Section>
		);
	}

	return (
		<Section title={t("inspector.sessionControls")}>
			<AutoInjectCIPolicyControl hostId={hostId} session={session} />
			<AutoInjectReviewPolicyControl hostId={hostId} session={session} />
			{hostId ? null : <SessionFenceGapLine sessionId={session.id} projectId={session.workspaceId} />}
			{session.kind === "orchestrator" ? null : canTerminateNow ? (
				terminateAction
			) : (
				<>
					<InspectorPolicyRow
						ariaLabel={t("inspector.terminateOnMerge")}
						checked={Boolean(session.terminateOnPrMerge)}
						description={t("inspector.terminateOnMergeDescription")}
						disabled={policy.isPending}
						id={`merge-policy-${sessionUiKey(session.id, hostId)}`}
						label={t("inspector.terminateOnMergeShort")}
						onCheckedChange={(checked) => policy.mutate(checked)}
						tooltipClassName="max-w-60"
					/>
					{policyError ? (
						<p className="mt-1 text-xs leading-normal text-error" role="status">
							{policyError}
						</p>
					) : null}
				</>
			)}
		</Section>
	);
}

export function updateSessionMergePolicy(
	workspaces: WorkspaceSummary[] | undefined,
	sessionId: string,
	terminateOnPrMerge: boolean,
): WorkspaceSummary[] | undefined {
	return workspaces?.map((workspace) => ({
		...workspace,
		sessions: workspace.sessions.map((candidate) =>
			candidate.id === sessionId ? { ...candidate, terminateOnPrMerge } : candidate,
		),
	}));
}
