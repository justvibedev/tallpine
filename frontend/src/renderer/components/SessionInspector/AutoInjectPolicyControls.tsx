import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useEffect, useState } from "react";
import { apiErrorMessage } from "../../lib/api-client";
import { clientForSessionHost } from "../../lib/host-clients";
import { sessionUiKey } from "../../lib/hosts";
import { workspaceQueryKeyForHost } from "../../hooks/useWorkspaceQuery";
import type { WorkspaceSession, WorkspaceSummary } from "../../types/workspace";
import { usesPreviewWorkspaceData as usePreviewData } from "../../lib/preview-mode";
import { InspectorPolicyRow } from "./InspectorPolicyRow";

export function AutoInjectCIPolicyControl({ session, hostId }: { session: WorkspaceSession; hostId?: string }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const workspaceKey = workspaceQueryKeyForHost(hostId);
	const [enabled, setEnabled] = useState(session.autoInjectCI ?? true);
	useEffect(() => {
		setEnabled(session.autoInjectCI ?? true);
	}, [hostId, session.id, session.autoInjectCI]);
	const save = useMutation({
		mutationFn: async (autoInjectCI: boolean) => {
			if (usePreviewData) return;
			const { error, response } = await clientForSessionHost(hostId).PATCH("/api/v1/sessions/{sessionId}/auto-inject-ci", {
				params: { path: { sessionId: session.id } },
				body: { autoInjectCI },
			});
			if (error) throw new Error(apiErrorMessage(error, t("inspector.ci.autoInjectError", { status: response.status })));
		},
		onMutate: async (autoInjectCI) => {
			await queryClient.cancelQueries({ queryKey: workspaceKey });
			const previous = queryClient.getQueryData<WorkspaceSummary[]>(workspaceKey);
			queryClient.setQueryData<WorkspaceSummary[]>(workspaceKey, (current) =>
				updateSessionAutoInjectCI(current, session.id, autoInjectCI),
			);
			return { previous };
		},
		onError: (_error, _next, context) => {
			setEnabled(session.autoInjectCI ?? true);
			if (context?.previous) queryClient.setQueryData(workspaceKey, context.previous);
		},
		onSettled: () => {
			void queryClient.invalidateQueries({ queryKey: workspaceKey });
		},
	});
	const error = save.error instanceof Error ? save.error.message : null;

	return (
		<>
			<InspectorPolicyRow
				checked={enabled}
				description={t("inspector.ci.autoInjectDescription")}
				disabled={save.isPending}
				id={`auto-inject-ci-${sessionUiKey(session.id, hostId)}`}
				label={t("inspector.ci.autoInject")}
				onCheckedChange={(next) => {
					setEnabled(next);
					save.mutate(next);
				}}
				tooltipClassName="max-w-64"
			/>
			{error ? (
				<p className="mt-1 text-xs leading-normal text-error" role="status">
					{error}
				</p>
			) : null}
		</>
	);
}

export function AutoInjectReviewPolicyControl({ session, hostId }: { session: WorkspaceSession; hostId?: string }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const workspaceKey = workspaceQueryKeyForHost(hostId);
	const [enabled, setEnabled] = useState(session.autoInjectReview ?? true);
	useEffect(() => {
		setEnabled(session.autoInjectReview ?? true);
	}, [hostId, session.id, session.autoInjectReview]);
	const save = useMutation({
		mutationFn: async (autoInjectReview: boolean) => {
			if (usePreviewData) return;
			const { error } = await clientForSessionHost(hostId).PATCH("/api/v1/sessions/{sessionId}/auto-inject-review", {
				params: { path: { sessionId: session.id } },
				body: { autoInjectReview },
			});
			if (error) throw new Error(apiErrorMessage(error, t("inspector.review.autoInjectError")));
		},
		onSuccess: () => {
			void queryClient.invalidateQueries({ queryKey: workspaceKey });
		},
		onError: () => {
			setEnabled(session.autoInjectReview ?? true);
		},
	});
	const error = save.error instanceof Error ? save.error.message : null;

	return (
		<>
			<InspectorPolicyRow
				checked={enabled}
				description={t("inspector.review.autoInjectDescription")}
				disabled={save.isPending}
				id={`auto-inject-review-${sessionUiKey(session.id, hostId)}`}
				label={t("inspector.review.autoInject")}
				onCheckedChange={(next) => {
					setEnabled(next);
					save.mutate(next);
				}}
				tooltipClassName="max-w-60"
			/>
			{error ? (
				<p className="mt-1 text-xs leading-normal text-error" role="status">
					{error}
				</p>
			) : null}
		</>
	);
}

export function updateSessionAutoInjectCI(
	workspaces: WorkspaceSummary[] | undefined,
	sessionId: string,
	autoInjectCI: boolean,
): WorkspaceSummary[] | undefined {
	return workspaces?.map((workspace) => ({
		...workspace,
		sessions: workspace.sessions.map((candidate) =>
			candidate.id === sessionId ? { ...candidate, autoInjectCI } : candidate,
		),
	}));
}
