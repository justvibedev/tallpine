import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { InspectorPullRequestCardView, type InspectorPullRequest } from "@tallpine/product-ui";
import { ArrowUpRight, GitPullRequest, GitMerge, Loader2 } from "lucide-react";
import { apiErrorMessage } from "../../lib/api-client";
import { clientForSessionHost } from "../../lib/host-clients";
import { workspaceQueryKeyForHost } from "../../hooks/useWorkspaceQuery";
import { ProductExternalLink } from "../ProductExternalLink";
import {
	sessionScmSummaryQueryKey,
	type SessionPRReference,
	type SessionPRSummary,
} from "../../hooks/useSessionScmSummary";
import { prBrowserUrl, prCanMerge, prCardPresentation, prNounKeys, prRepositoryFromUrl } from "../../lib/pr-display";
import { Button } from "../ui/button";
import { usesPreviewWorkspaceData as usePreviewData } from "../../lib/preview-mode";
import { prStateLabelKeys } from "./viewDefs";
import { Spinner } from "../ui/spinner";

export function PRSummaryCard({
	canOpenReviews,
	hostId,
	onOpenReviews,
	pr,
	sessionId,
	showRepository = false,
}: {
	canOpenReviews: boolean;
	hostId?: string;
	onOpenReviews: () => void;
	pr: SessionPRSummary;
	sessionId: string;
	/** Label the card with its repository: the session has PRs on several. */
	showRepository?: boolean;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const workspaceKey = workspaceQueryKeyForHost(hostId);
	const presentation = prCardPresentation(pr);
	const canMerge = prCanMerge(pr) && Boolean(pr.url && pr.headSha);
	const mergePr = useMutation({
		mutationFn: async () => {
			if (usePreviewData) return;
			const { data, error } = await clientForSessionHost(hostId).POST("/api/v1/prs/{id}/merge", {
				params: { path: { id: String(pr.number) } },
				body: { prUrl: pr.url, expectedHeadSha: pr.headSha },
			});
			if (error) throw new Error(apiErrorMessage(error, t("pr.merge.failed", { number: pr.number })));
			return data;
		},
		onSuccess: async (result) => {
			if (result?.state === "merged") {
				queryClient.setQueryData<{ prs: SessionPRSummary[]; linkedPrs: SessionPRReference[] }>(
					sessionScmSummaryQueryKey(sessionId, hostId),
					(current) => current ? {
						...current,
						prs: current.prs.map((cached) => cached.url === pr.url ? {
							...cached,
							state: result.state,
							stateChangedAt: result.stateChangedAt,
							updatedAt: result.stateChangedAt,
						} : cached),
					} : current,
				);
				await queryClient.invalidateQueries({ queryKey: workspaceKey });
				return;
			}
			await Promise.all([
				queryClient.invalidateQueries({ queryKey: sessionScmSummaryQueryKey(sessionId, hostId) }),
				queryClient.invalidateQueries({ queryKey: workspaceKey }),
			]);
		},
	});
	const mergeError = mergePr.error instanceof Error ? mergePr.error.message : null;
	const viewModel: InspectorPullRequest = {
		...pr,
		card: presentation,
		href: prBrowserUrl(pr),
		stateLabel: t(prStateLabelKeys[pr.state]),
		repositoryLabel: showRepository ? pr.repo || prRepositoryFromUrl(pr.htmlUrl || pr.url) : undefined,
		reviewDetailsAction: canOpenReviews && pr.review.decision !== "none" ? (
			<button className="whitespace-nowrap text-settings-muted underline-offset-2 hover:underline" onClick={onOpenReviews} type="button">
				<span className="text-xs">{t("pr.review.viewDetails")} ↗</span>
			</button>
		) : undefined,
	};
	return (
		<InspectorPullRequestCardView
			countNounLabel={(count, noun) => `${count} ${t(prNounKeys[noun], { count })}`}
			externalIcon={<ArrowUpRight aria-hidden="true" className="size-icon-2xs shrink-0" strokeWidth={2} />}
			externalLink={ProductExternalLink}
			mergeAction={
				canMerge ? (
					<Button
						aria-label={t("pr.merge.actionFor", { number: pr.number })}
						className="gap-1 bg-success px-2 text-xs text-on-foreground hover:bg-success/80"
						disabled={mergePr.isPending}
						onClick={() => mergePr.mutate()}
						size="sm"
						type="button"
					>
						{mergePr.isPending ? (
							<Spinner icon={Loader2} className="size-icon-sm" />
						) : (
							<GitMerge className="size-icon-sm" aria-hidden="true" />
						)}
						{mergePr.isPending ? t("pr.merge.merging") : t("pr.merge.action")}
					</Button>
				) : undefined
			}
			mergeError={mergeError}
			openLabel={t("inspector.openPR", { number: pr.number })}
			openedViaLabel={t("pr.openedVia")}
			pr={viewModel}
			pullRequestIcon={<GitPullRequest className="size-icon-sm shrink-0" aria-hidden="true" />}
		/>
	);
}
