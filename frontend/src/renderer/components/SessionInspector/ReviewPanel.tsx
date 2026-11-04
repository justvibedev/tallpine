import { useTranslation } from "react-i18next";
import { useEffect, useState } from "react";
import { InspectorSection as Section, inspectorEmptyClass } from "@tallpine/product-ui";
import { Archive, Info, Play, Loader2, X } from "lucide-react";
import type { components } from "../../../api/schema";
import { apiErrorMessage } from "../../lib/api-client";
import { sessionUiKey } from "../../lib/hosts";
import { AgentAvatar } from "../AgentAvatar";
import type { WorkspaceSession } from "../../types/workspace";
import { sortedPRs } from "../../types/workspace";
import { Button } from "../ui/button";
import { ReviewerSelect } from "../ReviewerSelect";
import { agentLabel } from "../../lib/agent-options";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import {
	openReviewStatesFor,
	reviewHasLiveActivity,
	reviewIsRunning,
	reviewRunDisabled,
	reviewSessionRunAction,
	type PRReviewState,
	type ReviewRunFacts,
} from "../../lib/session-reviews";
import { ProjectConfig, ReviewerSurface } from "./types";
import { InspectorPolicyRow } from "./InspectorPolicyRow";
import { ReviewerHarness, AgentCatalog, resolveDefaultReviewerHarness } from "./ReviewsSection";
import { Spinner } from "../ui/spinner";

export function ReviewPanel({
	notice,
	autoReviewEnabled,
	session,
	hostId,
	config,
	reviewStates,
	reviewerHandleId,
	reviewerSurface,
	reviewerActivityState,
	activeReviewers,
	onOpenReviewer,
	isLoading,
	isTriggering,
	isCancelling,
	isAutoReviewSaving,
	isKilling,
	isSwitchingReviewer,
	error,
	agentCatalog,
	reviewerOverride,
	reviewerModel,
	reviewerMode,
	onReviewerOverrideChange,
	onReviewerHarnessPreviewChange,
	onTrigger,
	onCancel,
	onAutoReviewChange,
	onKill,
}: {
	autoReviewEnabled: boolean;
	session: WorkspaceSession;
	hostId?: string;
	config?: ProjectConfig;
	reviewStates: PRReviewState[];
	reviewerHandleId: string;
	reviewerSurface?: components["schemas"]["ListReviewsResponse"]["reviewerSurface"];
	reviewerActivityState?: components["schemas"]["ListReviewsResponse"]["reviewerActivityState"];
	activeReviewers: ReviewerSurface[];
	onOpenReviewer: (surface: ReviewerSurface) => void;
	isLoading: boolean;
	isTriggering: boolean;
	isCancelling: boolean;
	isKilling: boolean;
	isSwitchingReviewer: boolean;
	error: unknown;
	agentCatalog?: AgentCatalog;
	reviewerOverride: ReviewerHarness | "";
	reviewerModel: string;
	reviewerMode: string;
	onReviewerOverrideChange: (next: ReviewerHarness | "", config: { model?: string; mode?: string }) => void;
	onReviewerHarnessPreviewChange: (next: ReviewerHarness | "") => void;
	onTrigger: () => void;
	onCancel: () => void;
	onAutoReviewChange: (enabled: boolean) => void;
	isAutoReviewSaving: boolean;
	onKill: () => void;
	/** The trigger declined to run and says why. */
	notice?: string | null;
}) {
	const { t } = useTranslation();
	const latestAutoFailure = reviewStates
		.map((review) => review.latestRun)
		.filter(
			(run): run is ReviewRunFacts =>
				Boolean(
					autoReviewEnabled &&
						run?.triggerSource === "auto" &&
						run.status === "failed" &&
						run.body?.trim(),
				),
		)
		.sort((a, b) => b.createdAt.localeCompare(a.createdAt))[0];
	const [dismissedAutoFailureId, setDismissedAutoFailureId] = useState<string | null>(null);
	useEffect(() => {
		if (!latestAutoFailure || latestAutoFailure.id === dismissedAutoFailureId) return;
		const timer = window.setTimeout(() => setDismissedAutoFailureId(latestAutoFailure.id), 10_000);
		return () => window.clearTimeout(timer);
	}, [dismissedAutoFailureId, latestAutoFailure]);
	if (sortedPRs(session).length === 0) {
		return <Section surface={false} title={t("inspector.review.controls")}><p className={inspectorEmptyClass}>{t("inspector.noPROpened")}</p></Section>;
	}
	if (isLoading) {
		return <Section surface={false} title={t("inspector.review.controls")}><p className={inspectorEmptyClass}>{t("inspector.loadingReviews")}</p></Section>;
	}

	const openReviewStates = openReviewStatesFor(session, reviewStates);
	// Whichever PR happens to come first is not the reviewer to name. With one PR
	// reviewed earlier by claude-code and another running under codex, taking the
	// first run reported the wrong agent as the one working. Prefer the run
	// actually in flight, then the newest recorded one.
	const runningRun = openReviewStates.find((review) => review.status === "running")?.latestRun;
	const newestRun = openReviewStates
		.map((review) => review.latestRun)
		.filter((run): run is NonNullable<typeof run> => Boolean(run))
		.sort((a, b) => b.createdAt.localeCompare(a.createdAt))[0];
	const latest = runningRun ?? newestRun;
	const resolvedDefaultHarness = resolveDefaultReviewerHarness(config, session.provider);
	const effectiveReviewerHarness = reviewerOverride || resolvedDefaultHarness;
	const activeReviewerHarness = latest?.harness || effectiveReviewerHarness;
	const autoReviewFailure =
		latestAutoFailure && latestAutoFailure.id !== dismissedAutoFailureId ? latestAutoFailure.body.trim() : null;
	const hasReviewerSession = reviewerHandleId.trim() !== "" ||
		Boolean(reviewerSurface?.mode === "chat" && reviewerSurface.reviewId);
	const reviewRunning = reviewIsRunning(openReviewStates);
	const reviewLive = reviewHasLiveActivity(openReviewStates, reviewerActivityState, hasReviewerSession);
	const reviewHasRun = reviewRunning || Boolean(latest);
	const runAction = reviewSessionRunAction(openReviewStates, isTriggering);
	const runDisabled = isKilling || isSwitchingReviewer || reviewRunDisabled(openReviewStates, isTriggering);
	const primaryReviewActionLabel = reviewRunning
		? isCancelling
			? t("inspector.review.cancelling")
			: t("inspector.review.cancel")
		: runAction;
	const archiveActionLabel = isKilling ? t("inspector.review.killingSession") : t("inspector.review.killSession");
	const killDisabled = isKilling || isCancelling || isTriggering || isSwitchingReviewer || !hasReviewerSession;

	return (
		<Section surface={false} title={t("inspector.review.controls")}>
				<div className="flex min-w-0 flex-col gap-3">
					{error ? (
						<p className="m-0 rounded-md border border-error/28 bg-error/8 px-2.5 py-2 text-xs leading-normal text-error">
							{apiErrorMessage(error, t("inspector.reviewRequestFailed"))}
					</p>
				) : null}
				{autoReviewFailure ? (
					<p className="m-0 rounded-md border border-error/28 bg-error/8 px-2.5 py-2 text-xs leading-normal text-error" role="status">
						<span className="font-semibold">
							{t("inspector.autoReview")} {t("inspector.review.failed")}:
						</span>{" "}
						{autoReviewFailure}
					</p>
				) : null}
				{notice ? (
					<Tooltip>
						<TooltipTrigger asChild>
							<button
								aria-label={notice}
								className="flex max-w-full shrink-0 items-start gap-1 self-start rounded-sm text-left text-2xs font-medium leading-normal text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
								type="button"
							>
								<Info aria-hidden="true" className="mt-px size-icon-2xs shrink-0" />
								<span className="min-w-0 text-xs">{t("inspector.reviewAlreadyRanShort")}</span>
							</button>
						</TooltipTrigger>
						<TooltipContent className="max-w-56 leading-normal">{notice}</TooltipContent>
					</Tooltip>
				) : null}
				<div className="review-run-controls-container flex min-w-0 flex-col text-xs">
					<div className="flex min-h-8 min-w-0 items-center justify-between gap-3 py-1">
						<span className="min-w-0 text-sm text-settings-label">
							{t("inspector.selectReviewerAgent")}
						</span>
						<ReviewerSelect
							ariaLabel={t("inspector.selectReviewerAgent")}
							agents={agentCatalog?.agents}
							contentAlign="end"
							defaultHarness={resolvedDefaultHarness}
							disabled={reviewRunning || autoReviewEnabled || isKilling || isSwitchingReviewer || isTriggering || isCancelling}
							onChange={(next) => onReviewerHarnessPreviewChange(next as ReviewerHarness | "")}
							onConfigChange={(harness, config) => onReviewerOverrideChange(harness as ReviewerHarness | "", config)}
							model={reviewerModel}
							mode={reviewerMode}
							hostId={hostId}
							projectId={session.workspaceId}
							triggerClassName="review-run-agent-select ml-auto h-control-md w-auto min-w-0 shrink-0 justify-end px-2 text-right text-xs"
							value={reviewerOverride}
						/>
					</div>
					{/* Several reviewers can work on one worker at once (an agent may
					    ask a reviewer other than the selected one). The reviewer tab
					    shows one at a time, so each live reviewer gets a row to open it
					    whenever any of them is not the selected reviewer. */}
					{activeReviewers.some((surface) => surface.reviewId !== reviewerSurface?.reviewId)
						? activeReviewers.map((surface) => (
								<div className="flex min-h-8 min-w-0 items-center justify-between gap-3 py-1" key={surface.reviewId}>
									<span className="flex min-w-0 items-center gap-1.5 text-sm text-foreground">
										<AgentAvatar className="size-4" decorative provider={surface.harness} />
										<span className="truncate">{agentLabel(surface.harness)}</span>
									</span>
									<Button
										className="shrink-0 px-1.5 text-xs"
										disabled={surface.mode !== "chat" && !surface.handleId}
										onClick={() => onOpenReviewer(surface)}
										size="sm"
										type="button"
										variant="ghost"
									>
										{t("inspector.review.openReviewer")}
									</Button>
								</div>
							))
						: null}
					<InspectorPolicyRow
						checked={autoReviewEnabled}
						description={t("inspector.autoReviewDescription")}
						disabled={isAutoReviewSaving}
						id={`auto-review-${sessionUiKey(session.id, hostId)}`}
						label={t("inspector.autoReview")}
						onCheckedChange={onAutoReviewChange}
						tooltipClassName="max-w-64"
					/>
					<div className="flex min-h-8 min-w-0 items-center justify-between gap-3 py-1">
						<span className="text-sm text-settings-label">{t("inspector.review.session")}</span>
						<div className="flex min-w-0 items-center justify-end gap-1.5">
							<Button
								aria-label={primaryReviewActionLabel}
								className="shrink-0 gap-1 px-1.5 text-xs [&_svg]:size-icon-sm"
								disabled={reviewRunning ? isCancelling || isKilling || isSwitchingReviewer : runDisabled || autoReviewEnabled}
								onClick={reviewRunning ? onCancel : onTrigger}
								size="sm"
								type="button"
								variant={reviewRunning ? "ghost" : reviewHasRun ? "secondary" : "primary"}
							>
								{reviewRunning ? <X aria-hidden="true" /> : <Play aria-hidden="true" />}
								<span className="review-run-action-label">{primaryReviewActionLabel}</span>
							</Button>
							<Tooltip>
								<TooltipTrigger asChild>
									<span className="inline-flex">
										<Button
											aria-label={archiveActionLabel}
											className="shrink-0 [&_svg]:size-icon-sm"
											disabled={killDisabled}
											onClick={onKill}
											size="icon-sm"
											type="button"
											variant="ghost"
										>
											<Archive aria-hidden="true" />
										</Button>
									</span>
								</TooltipTrigger>
								<TooltipContent>{archiveActionLabel}</TooltipContent>
							</Tooltip>
						</div>
					</div>
				</div>
				{reviewLive ? (
					<div className="flex items-center gap-2 border-t border-border pt-3">
						<Spinner icon={Loader2} className="size-icon-sm shrink-0 text-muted-foreground" />
						<span className="min-w-0 flex-1 truncate text-xs font-medium text-muted-foreground">
							{isCancelling
								? t("inspector.review.cancelling")
								: `Review in progress · ${agentLabel(activeReviewerHarness)}`}
						</span>
					</div>
				) : null}
			</div>
		</Section>
	);
}
