import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useEffect, useState } from "react";
import { reviewerConversationQueryKey } from "../../hooks/useReviewerConversation";
import { apiErrorMessage } from "../../lib/api-client";
import { clientForSessionHost } from "../../lib/host-clients";
import { sessionUiKey } from "../../lib/hosts";
import { workspaceQueryKeyForHost } from "../../hooks/useWorkspaceQuery";
import { useSessionScmSummary } from "../../hooks/useSessionScmSummary";
import { sessionPRDisplaySummaries } from "../../lib/pr-display";
import type { WorkspaceSession } from "../../types/workspace";
import { sortedPRs } from "../../types/workspace";
import { ConfirmDialog } from "../ConfirmDialog";
import { useAgentReadinessQuery, useEnsureAgentReadiness } from "../../hooks/useAgentReadinessQuery";
import { usesPreviewWorkspaceData as usePreviewData } from "../../lib/preview-mode";
import { openReviewStatesFor, sessionReviewsQueryOptions, sessionReviewsQueryKey } from "../../lib/session-reviews";
import { OpenReviewerTerminal } from "./types";
import { ReviewerHarness, resolveDefaultReviewerHarness } from "./ReviewsSection";
import { MergedReviewsSection } from "./MergedReviewsSection";
import { buildReviewerAgentConfig, projectConfig, mockProjectConfig } from "./reviewHelpers";
import { ReviewPanel } from "./ReviewPanel";

export function LocalReviewsSection({
	session,
	hostId,
	onOpenReviewFile,
	onOpenReviewerTerminal,
	onOpenReviewerChat,
	onWorkerMessageSent,
}: {
	session: WorkspaceSession;
	hostId?: string;
	onOpenReviewFile?: (target: { line?: number; path: string }) => void;
	onOpenReviewerTerminal?: OpenReviewerTerminal;
	onOpenReviewerChat?: (reviewId: string) => void;
	onWorkerMessageSent?: () => void;
}) {
	const { t } = useTranslation();
	const hasPr = sortedPRs(session).length > 0;
	const queryClient = useQueryClient();
	const workspaceKey = workspaceQueryKeyForHost(hostId);
	const reviewsKey = sessionReviewsQueryKey(session.id, hostId);
	const [rerunConfirmation, setRerunConfirmation] = useState<{ ownerKey: string; heads: string } | null>(null);
	const reviewsQuery = useQuery({
		...sessionReviewsQueryOptions(session, hasPr, undefined, hostId),
		refetchInterval: (query) => {
			const reviews = query.state.data?.reviews ?? [];
			if (reviews.some((review) => review.status === "running")) return 2500;
			return session.autoReviewEnabled === true ? 10_000 : false;
		},
	});
	const agentsQuery = useAgentReadinessQuery(true, hostId);
	useEnsureAgentReadiness({ hostId });
	const projectConfigQuery = useQuery({
		queryKey: hostId ? ["project-config", hostId, session.workspaceId] : ["project-config", session.workspaceId],
		enabled: hasPr,
		queryFn: async () => {
			if (usePreviewData) return mockProjectConfig();
			const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/projects/{id}", {
				params: { path: { id: session.workspaceId } },
			});
			if (error) return undefined;
			return projectConfig(data?.project);
		},
	});
	// The reviewer preference belongs to the worker session, not this component
	// or the whole project. Keep local state responsive while the daemon persists
	// it, and resync when the inspector moves to another session.
	const currentDefaultReviewerHarness = resolveDefaultReviewerHarness(projectConfigQuery.data, session.provider);
	const [reviewerOverride, setReviewerOverride] = useState<ReviewerHarness | "">(
		session.reviewerHarness ?? "",
	);
	const [reviewerModel, setReviewerModel] = useState(session.reviewerConfig?.model ?? "");
	const [reviewerMode, setReviewerMode] = useState(session.reviewerConfig?.mode ?? "");
	const reviewerOwnerKey = sessionUiKey(session.id, hostId);
	const openReviewStates = openReviewStatesFor(session, reviewsQuery.data?.reviews ?? []);
	const reviewHeads = openReviewStates.map((review) => `${review.prUrl}@${review.targetSha}`).sort().join("\n");
	const currentHeadReviewed = openReviewStates.some((review) => review.latestRun?.targetSha === review.targetSha && (review.latestRun.status === "complete" || review.latestRun.status === "delivered"));
	useEffect(() => {
		setRerunConfirmation((current) => current && (current.ownerKey !== reviewerOwnerKey || current.heads !== reviewHeads) ? null : current);
	}, [reviewerOwnerKey, reviewHeads]);
	useEnsureAgentReadiness({
		agentIds: reviewerOverride ? [reviewerOverride] : [],
		enabled: reviewerOverride !== "",
		hostId,
	});
	useEffect(() => {
		setReviewerOverride(session.reviewerHarness ?? "");
		setReviewerModel(session.reviewerConfig?.model ?? "");
		setReviewerMode(session.reviewerConfig?.mode ?? "");
	}, [hostId, session.id, session.reviewerConfig?.mode, session.reviewerConfig?.model, session.reviewerHarness]);
	const saveReviewer = useMutation({
		mutationKey: [...reviewsKey, "switch-reviewer"],
		mutationFn: async ({ harness, model, mode }: { harness: ReviewerHarness | ""; model: string; mode: string }) => {
			const clearingToProjectDefault = harness === "" && model === "" && mode === "";
			const currentEffectiveReviewerHarness = (session.reviewerHarness ?? "") || currentDefaultReviewerHarness;
			const nextEffectiveReviewerHarness = harness || currentDefaultReviewerHarness;
			const existingReviewerConfig =
				!clearingToProjectDefault && currentEffectiveReviewerHarness === nextEffectiveReviewerHarness
					? session.reviewerConfig
					: undefined;
			const nextReviewerConfig = buildReviewerAgentConfig(existingReviewerConfig, model, mode);
			const { data, error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/reviews/switch", {
				params: { path: { sessionId: session.id } },
				body: {
					harness: harness || undefined,
					agentConfig: nextReviewerConfig,
				},
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to save reviewer"));
			if (data) queryClient.setQueryData(reviewsKey, data);
		},
		onSuccess: () => {
			void queryClient.invalidateQueries({ queryKey: reviewsKey });
			void queryClient.invalidateQueries({ queryKey: workspaceKey });
		},
	});
	const saveAutoReview = useMutation({
		mutationFn: async (enabled: boolean) => {
			// Intent, not effect: emitted before the PUT, so a failed save still
			// counts as the user reaching for the switch.
			const { error } = await clientForSessionHost(hostId).PUT("/api/v1/sessions/{sessionId}/auto-review", {
				params: { path: { sessionId: session.id } },
				body: { enabled },
			});
			if (error) throw new Error(apiErrorMessage(error, t("inspector.reviewRequestFailed")));
		},
		onSuccess: () => {
			void queryClient.invalidateQueries({ queryKey: workspaceKey });
		},
	});
	const triggerReview = useMutation({
		mutationFn: async ({ ownerKey, rerun = false }: { ownerKey: string; rerun?: boolean }) => {
			// Keep agent/model overrides scoped to this pass; the daemon selects the surface.
			const reviewerConfig = reviewerModel || reviewerMode
				? { ...(reviewerModel ? { model: reviewerModel } : {}), ...(reviewerMode ? { mode: reviewerMode } : {}) }
				: undefined;
			const selectedHarness = reviewerOverride || undefined;
			const { data, error, response } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/reviews/trigger", {
				params: { path: { sessionId: session.id } },
				body: { ...(selectedHarness ? { harness: selectedHarness } : {}), ...(reviewerConfig ? { agentConfig: reviewerConfig } : {}), ...(rerun ? { rerun: true } : {}) },
			});
			if (error) throw new Error(apiErrorMessage(error, t("inspector.unableStartReview")));
			return { data, reused: response?.status === 200, rerun, ownerKey, reviewsKey, workspaceKey };
		},
		onMutate: async () => {
			await queryClient.cancelQueries({ queryKey: reviewsKey });
			return { reviewsKey };
		},
		onSuccess: ({ data, reused, rerun, ownerKey, reviewsKey: requestReviewsKey, workspaceKey: requestWorkspaceKey }) => {
			if (data) queryClient.setQueryData(requestReviewsKey, data);
			void queryClient.invalidateQueries({ queryKey: requestReviewsKey });
			void queryClient.invalidateQueries({ queryKey: requestWorkspaceKey });
			if (ownerKey !== reviewerOwnerKey) return;
			const started = data?.reviews?.find((review) => review.status === "running" && review.latestRun);
			if (!started?.latestRun) {
				if (reused && !rerun) setRerunConfirmation({ ownerKey, heads: reviewHeads });
				return;
			}
			if (data?.reviewerSurface?.mode === "chat" && data.reviewerSurface.reviewId) {
				onOpenReviewerChat?.(data.reviewerSurface.reviewId);
			} else if (data?.reviewerHandleId) {
				const harness = started.latestRun.harness || "reviewer";
				onOpenReviewerTerminal?.({ handleId: data.reviewerHandleId, harness });
			}
		},
		onError: (_error, _variables, context) => {
			void queryClient.invalidateQueries({ queryKey: context?.reviewsKey ?? reviewsKey });
		},
	});
	const cancelReview = useMutation({
		mutationFn: async () => {
			const { error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/reviews/cancel", {
				params: { path: { sessionId: session.id } },
			});
			if (error) throw new Error(apiErrorMessage(error, t("inspector.unableCancelReview")));
		},
		onSuccess: () => {
			void queryClient.invalidateQueries({ queryKey: reviewsKey });
			void queryClient.invalidateQueries({ queryKey: workspaceKey });
		},
	});
	const killReview = useMutation({
		mutationFn: async () => {
			const { data, error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/reviews/kill", {
				params: { path: { sessionId: session.id } },
			});
			if (error) throw new Error(apiErrorMessage(error, t("inspector.unableKillReviewSession")));
			return {
				data,
				requestReviewsKey: reviewsKey,
				requestWorkspaceKey: workspaceKey,
				reviewId: reviewsQuery.data?.reviewerSurface?.reviewId,
				requestHostId: hostId,
			};
		},
		onSuccess: ({ data, requestReviewsKey, requestWorkspaceKey, reviewId, requestHostId }) => {
			if (data) queryClient.setQueryData(requestReviewsKey, data);
			void queryClient.invalidateQueries({ queryKey: requestReviewsKey });
			void queryClient.invalidateQueries({ queryKey: requestWorkspaceKey });
			if (reviewId) {
				void queryClient.invalidateQueries({ queryKey: reviewerConversationQueryKey(reviewId, requestHostId) });
			}
		},
	});
	const reviewStates = reviewsQuery.data?.reviews ?? [];
	const autoReviewEnabled = session.autoReviewEnabled === true;
	const scmSummary = useSessionScmSummary(session.id, true, hostId);
	const prSummaries = sessionPRDisplaySummaries(session, scmSummary.data?.prs);
	const githubReviews = prSummaries.filter(
		(pr) =>
			(pr.state === "open" || pr.state === "draft") &&
			((pr.review?.reviews?.length ?? 0) > 0 ||
				(pr.review?.unresolvedBy ?? []).some((reviewer) => reviewer.count > 0) ||
				(pr.review?.resolvedBy ?? []).some((reviewer) => reviewer.count > 0)),
	);
	return (
		<>
			{/* Running a review is an action; reading them is a list. The action stays
			    on top, then one list carrying both sources keyed by PR. */}
			<ReviewPanel
				autoReviewEnabled={autoReviewEnabled}
				config={projectConfigQuery.data}
				error={
					reviewsQuery.error ??
					triggerReview.error ??
					cancelReview.error ??
					killReview.error ??
					saveReviewer.error ??
					saveAutoReview.error
				}
				isLoading={reviewsQuery.isLoading}
				isCancelling={cancelReview.isPending}
				isAutoReviewSaving={saveAutoReview.isPending}
				isKilling={killReview.isPending}
				isSwitchingReviewer={saveReviewer.isPending}
				isTriggering={triggerReview.isPending}
				hostId={hostId}
				onCancel={() => cancelReview.mutate()}
				onAutoReviewChange={(enabled) => saveAutoReview.mutate(enabled)}
				onKill={() => killReview.mutate()}
				onTrigger={() => {
					if (currentHeadReviewed) setRerunConfirmation({ ownerKey: reviewerOwnerKey, heads: reviewHeads });
					else triggerReview.mutate({ ownerKey: reviewerOwnerKey });
				}}
				reviewerHandleId={reviewsQuery.data?.reviewerHandleId ?? ""}
				reviewerSurface={reviewsQuery.data?.reviewerSurface}
				reviewerActivityState={reviewsQuery.data?.reviewerActivityState}
				activeReviewers={reviewsQuery.data?.activeReviewers ?? []}
				onOpenReviewer={(surface) => {
					if (surface.mode === "chat") onOpenReviewerChat?.(surface.reviewId);
					else if (surface.handleId) onOpenReviewerTerminal?.({ handleId: surface.handleId, harness: surface.harness });
				}}
				reviewStates={reviewStates}
				agentCatalog={agentsQuery.data}
				reviewerOverride={reviewerOverride}
				reviewerModel={reviewerModel}
				reviewerMode={reviewerMode}
				onReviewerOverrideChange={(next, config) => {
					setReviewerOverride(next);
					setReviewerModel(config.model ?? "");
					setReviewerMode(config.mode ?? "");
					saveReviewer.mutate({ harness: next, model: config.model ?? "", mode: config.mode ?? "" });
				}}
				onReviewerHarnessPreviewChange={(next) => {
					setReviewerOverride(next);
					setReviewerModel("");
					setReviewerMode("");
				}}
				session={session}
			/>
			<ConfirmDialog
				open={rerunConfirmation?.ownerKey === reviewerOwnerKey && rerunConfirmation.heads === reviewHeads}
				title={t("inspector.rerunReviewTitle")}
				description={t("inspector.rerunReviewWarning")}
				confirmLabel={t("inspector.rerunReviewConfirm")}
				onOpenChange={(open) => { if (!open) setRerunConfirmation(null); }}
				onConfirm={() => {
					if (rerunConfirmation?.ownerKey !== reviewerOwnerKey || rerunConfirmation.heads !== reviewHeads) return;
					setRerunConfirmation(null);
					triggerReview.mutate({ ownerKey: reviewerOwnerKey, rerun: true });
				}}
			/>

			<MergedReviewsSection
				hostId={hostId}
				githubPRs={githubReviews}
				isLoading={scmSummary.isLoading}
				onOpenReviewFile={onOpenReviewFile}
				onWorkerMessageSent={onWorkerMessageSent}
				reviewStates={reviewStates}
				runs={reviewsQuery.data?.runs ?? []}
				session={session}
			/>
		</>
	);
}
