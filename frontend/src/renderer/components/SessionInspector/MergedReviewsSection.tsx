import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
	InspectorReviewsView,
	type InspectorInlineComment,
	type InspectorGithubReview,
	type InspectorReviewGroup,
	type InspectorReviewSummaryAction,
} from "@tallpine/product-ui";
import { apiErrorMessage } from "../../lib/api-client";
import { clientForSessionHost } from "../../lib/host-clients";
import { workspaceQueryKeyForHost } from "../../hooks/useWorkspaceQuery";
import { formatTimeCompact } from "../../lib/format-time";
import { AgentAvatar } from "../AgentAvatar";
import { ProductExternalLink } from "../ProductExternalLink";
import { sessionScmSummaryQueryKey, type SessionPRSummary } from "../../hooks/useSessionScmSummary";
import { useSessionBrowserLink } from "../../hooks/useSessionBrowserLink";
import { prRepositoryFromUrl } from "../../lib/pr-display";
import type { WorkspaceSession } from "../../types/workspace";
import { openReviewStatesFor, type PRReviewState, type ReviewRunFacts } from "../../lib/session-reviews";
import { externalReviewActorMatchesPRAuthor } from "./reviewActors";
import {
	canRequestPRRereview,
	reviewLabels,
	renderReviewMarkdown,
	formatInlineReviewCommentMessage,
	formatReviewSummaryMessage,
} from "./reviewHelpers";
import {
	githubVerdict,
	runsByPRFrom,
	triggeredReviewStatesFrom,
	tallpineReviewMeta,
	tallpineReviewCommentUrl,
	reviewVerdict,
} from "./reviewState";

/**
 * Tallpine's own reviewer passes and the reviews humans and bots left on GitHub, in
 * one list keyed by PR. They were two sections, which made the same PR appear
 * twice and left the reader joining them up by number; a review is a review,
 * and what matters is who wrote it. Each group inside a PR names its source —
 * "Tallpine codex" against the agent that ran, "On GitHub" for everyone else.
 */
export function MergedReviewsSection({
	githubPRs,
	hostId,
	isLoading,
	onOpenReviewFile,
	onWorkerMessageSent,
	reviewStates,
	runs,
	session,
}: {
	githubPRs: SessionPRSummary[];
	hostId?: string;
	isLoading: boolean;
	onOpenReviewFile?: (target: { line?: number; path: string }) => void;
	onWorkerMessageSent?: () => void;
	reviewStates: PRReviewState[];
	runs: ReviewRunFacts[];
	session: WorkspaceSession;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const openInTallpineBrowser = useSessionBrowserLink(session);
	const workspaceKey = workspaceQueryKeyForHost(hostId);
	const openReviewStates = openReviewStatesFor(session, reviewStates);
	const runsByPR = runsByPRFrom(openReviewStates, runs);
	const tallpineStates = triggeredReviewStatesFrom(openReviewStates, runs);

	// Union by PR, newest PR first. A PR can appear on either side alone. A
	// multi-repository session can have the same number in two repositories, so
	// there a PR is its repository and number; a single-repository session
	// matches by number alone, as before.
	const prRepositories = new Set([
		...tallpineStates.map((state) => prRepositoryFromUrl(state.prUrl)),
		...githubPRs.map((pr) => pr.repo || prRepositoryFromUrl(pr.htmlUrl || pr.url)),
	].filter((repository): repository is string => Boolean(repository)).map((repository) => repository.toLowerCase()));
	const keyByRepository = prRepositories.size > 1;
	const prKey = (url: string | undefined, number: number, repo?: string) =>
		keyByRepository ? `${(repo || prRepositoryFromUrl(url) || "").toLowerCase()}#${number}` : `#${number}`;
	const byPR = new Map<string, { number: number; repository?: string; tallpineState?: PRReviewState; github?: SessionPRSummary }>();
	for (const state of tallpineStates) {
		const key = prKey(state.prUrl, state.prNumber);
		byPR.set(key, { number: state.prNumber, repository: prRepositoryFromUrl(state.prUrl), ...byPR.get(key), tallpineState: state });
	}
	for (const pr of githubPRs) {
		const key = prKey(pr.htmlUrl || pr.url, pr.number, pr.repo);
		byPR.set(key, { number: pr.number, repository: pr.repo || prRepositoryFromUrl(pr.htmlUrl || pr.url), ...byPR.get(key), github: pr });
	}
	const severalRepositories = keyByRepository;
	const rows = [...byPR.entries()].sort(([, a], [, b]) => b.number - a.number);
	const labels = reviewLabels(t);
	const requestRereview = async (review: InspectorGithubReview) => {
		const { error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/reviews/rerequest", {
			params: { path: { sessionId: session.id } },
			body: { pullRequestUrl: review.pullRequestUrl, reviewerId: review.reviewerId },
		});
		if (error) throw new Error(apiErrorMessage(error, "Unable to request re-review"));
	};
	const resolveInlineComment = async (comment: InspectorInlineComment) => {
		const { error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/reviews/comments/resolve", {
			params: { path: { sessionId: session.id } },
			body: { pullRequestUrl: comment.pullRequestUrl, commentUrl: comment.url ?? "" },
		});
		if (error) throw new Error(apiErrorMessage(error, "Unable to resolve review comment"));
		void queryClient.invalidateQueries({ queryKey: sessionScmSummaryQueryKey(session.id, hostId) });
		void queryClient.invalidateQueries({ queryKey: workspaceKey });
	};
	const sendMessageToWorker = async (message: string, fallbackError: string) => {
		if (session.mode === "chat") {
			const { error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/conversation/messages", {
				params: { path: { sessionId: session.id } },
				body: { text: message, clientMessageId: crypto.randomUUID() },
			});
			if (error) throw new Error(apiErrorMessage(error, fallbackError));
			return;
		}
		const { error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/send", {
			params: { path: { sessionId: session.id } },
			body: { message, userAuthored: true },
		});
		if (error) throw new Error(apiErrorMessage(error, fallbackError));
	};
	const sendInlineCommentToWorker = async (comment: InspectorInlineComment & { reviewerId?: string }) => {
		await sendMessageToWorker(formatInlineReviewCommentMessage(comment), "Unable to send review comment to worker agent");
		onWorkerMessageSent?.();
	};
	const sendReviewSummaryToWorker = async (summary: InspectorReviewSummaryAction) => {
		await sendMessageToWorker(formatReviewSummaryMessage(summary), "Unable to send review summary to worker agent");
		onWorkerMessageSent?.();
	};
	const groups: InspectorReviewGroup[] = rows.map(([key, { number, repository, tallpineState, github }]) => {
		const prLabel = severalRepositories && repository ? `${repository} #${number}` : `#${number}`;
		const tallpineRuns = tallpineState ? [...(runsByPR.get(tallpineState.prUrl) ?? [])].sort((a, b) => b.createdAt.localeCompare(a.createdAt)) : [];
		const entries = (github?.review?.reviews ?? []).filter(
			(entry) => !externalReviewActorMatchesPRAuthor(entry.reviewerId, github?.author),
		);
		const allUnresolvedReviewers = github?.review?.unresolvedBy ?? [];
		const allResolvedReviewers = github?.review?.resolvedBy ?? [];
		const agentReviewIds = new Set(tallpineRuns.map((run) => run.githubReviewId).filter(Boolean));
		const agentComments = new Map<string, { inlineComments: InspectorInlineComment[]; resolvedComments: InspectorInlineComment[] }>();
		const partitionReviewerComments = (
			reviewers: typeof allUnresolvedReviewers,
			resolved: boolean,
		) => reviewers
			.map((reviewer) => {
				const externalLinks = [] as typeof reviewer.links;
				for (const link of reviewer.links) {
					const reviewId = link.reviewId?.trim();
					if (!reviewId || !agentReviewIds.has(reviewId)) {
						externalLinks.push(link);
						continue;
					}
					const comments = agentComments.get(reviewId) ?? { inlineComments: [], resolvedComments: [] };
					const comment = {
						autoInjectReview: link.autoInjectReview,
						body: link.body,
						file: link.file,
						line: link.line,
						pullRequestUrl: github?.url,
						resolved,
						reviewerId: reviewer.reviewerId,
						url: link.url || reviewer.reviewUrl,
					};
					if (resolved) comments.resolvedComments.push(comment);
					else comments.inlineComments.push(comment);
					agentComments.set(reviewId, comments);
				}
				return { ...reviewer, count: externalLinks.length, links: externalLinks };
			})
			.filter((reviewer) => reviewer.links.length > 0);
		const unresolvedReviewers = partitionReviewerComments(allUnresolvedReviewers, false).filter(
			(reviewer) => !externalReviewActorMatchesPRAuthor(reviewer.reviewerId, github?.author),
		);
		const resolvedReviewers = partitionReviewerComments(allResolvedReviewers, true).filter(
			(reviewer) => !externalReviewActorMatchesPRAuthor(reviewer.reviewerId, github?.author),
		);
		const unresolved = unresolvedReviewers.reduce((count, reviewer) => count + reviewer.count, 0);
		const reviewRuns = tallpineRuns.map((run) => {
			const reviewUrl = tallpineReviewCommentUrl(run);
			return {
				autoInjectReview: run.autoInjectReview,
				body: run.body,
				createdAtLabel:
					run.triggerSource === "agent"
						? `${formatTimeCompact(run.createdAt)} · ${t("inspector.review.requestedByAgent")}`
						: formatTimeCompact(run.createdAt),
				harness: run.harness || "reviewer",
				id: run.id,
				inlineComments: agentComments.get(run.githubReviewId)?.inlineComments ?? [],
				resolvedComments: agentComments.get(run.githubReviewId)?.resolvedComments ?? [],
				status: run.status,
				url: reviewUrl ?? (tallpineState?.prUrl || null),
				verdict: githubVerdict(run.verdict, t),
			};
		});
		const unresolvedByReviewer = new Map(
			unresolvedReviewers.map((reviewer) => [reviewer.reviewerId, reviewer]),
		);
		const resolvedByReviewer = new Map(
			resolvedReviewers.map((reviewer) => [reviewer.reviewerId, reviewer]),
		);
		const externalEntries = entries.map((entry) => {
			const reviewer = unresolvedByReviewer.get(entry.reviewerId);
			const resolvedReviewer = resolvedByReviewer.get(entry.reviewerId);
			unresolvedByReviewer.delete(entry.reviewerId);
			resolvedByReviewer.delete(entry.reviewerId);
			return {
				body: entry.body,
				canRequestRereview: canRequestPRRereview(entry.verdict, github?.url),
				id: entry.reviewUrl || `${entry.reviewerId}:${entry.submittedAt}`,
				pullRequestUrl: github?.url,
				inlineComments: (reviewer?.links ?? []).map((link) => ({
					autoInjectReview: link.autoInjectReview,
					body: link.body,
					file: link.file,
					line: link.line,
					pullRequestUrl: github?.url,
					url: link.url || reviewer?.reviewUrl,
				})),
				resolvedComments: (resolvedReviewer?.links ?? []).map((link) => ({
					autoInjectReview: link.autoInjectReview,
					body: link.body,
					file: link.file,
					line: link.line,
					pullRequestUrl: github?.url,
					resolved: true,
					url: link.url || resolvedReviewer?.reviewUrl,
				})),
				isBot: entry.isBot,
				reviewerId: entry.reviewerId,
				reviewUrl: entry.reviewUrl,
				submittedAt: entry.submittedAt,
				submittedAtLabel: formatTimeCompact(entry.submittedAt),
				verdict: githubVerdict(entry.verdict, t),
			};
		});
		for (const reviewer of unresolvedByReviewer.values()) {
			const resolvedReviewer = resolvedByReviewer.get(reviewer.reviewerId);
			resolvedByReviewer.delete(reviewer.reviewerId);
			externalEntries.push({
				body: undefined,
				canRequestRereview: canRequestPRRereview("changes_requested", github?.url),
				id: `unresolved:${reviewer.reviewerId}:${key}`,
				pullRequestUrl: github?.url,
				inlineComments: reviewer.links.map((link) => ({
					autoInjectReview: link.autoInjectReview,
					body: link.body,
					file: link.file,
					line: link.line,
					pullRequestUrl: github?.url,
					url: link.url || reviewer.reviewUrl,
				})),
				resolvedComments: (resolvedReviewer?.links ?? []).map((link) => ({
					autoInjectReview: link.autoInjectReview,
					body: link.body,
					file: link.file,
					line: link.line,
					pullRequestUrl: github?.url,
					resolved: true,
					url: link.url || resolvedReviewer?.reviewUrl,
				})),
				isBot: reviewer.isBot,
				reviewerId: reviewer.reviewerId,
				reviewUrl: reviewer.reviewUrl,
				submittedAt: "",
				submittedAtLabel: "",
				verdict: githubVerdict("none", t),
			});
		}
		for (const reviewer of resolvedByReviewer.values()) {
			externalEntries.push({
				body: undefined,
				canRequestRereview: false,
				id: `resolved:${reviewer.reviewerId}:${key}`,
				pullRequestUrl: github?.url,
				inlineComments: [],
				resolvedComments: reviewer.links.map((link) => ({
					autoInjectReview: link.autoInjectReview,
					body: link.body,
					file: link.file,
					line: link.line,
					pullRequestUrl: github?.url,
					resolved: true,
					url: link.url || reviewer.reviewUrl,
				})),
				isBot: reviewer.isBot,
				reviewerId: reviewer.reviewerId,
				reviewUrl: reviewer.reviewUrl,
				submittedAt: "",
				submittedAtLabel: "",
				verdict: githubVerdict("none", t),
			});
		}
		return {
			tallpineState: tallpineState
				? {
						dimmed: tallpineState.status === "ineligible",
						historical:
							tallpineState.status === "needs_review" &&
							Boolean(tallpineState.previousRun) &&
							(!tallpineState.latestRun || tallpineState.latestRun.status === "failed" || tallpineState.latestRun.status === "cancelled"),
						notInjected: tallpineRuns.some((run) => run.autoInjectReview === false),
						runs: reviewRuns,
					}
				: undefined,
			github: github
				? {
						entries: externalEntries,
						notInjected:
							entries.some((review) => review.autoInjectReview === false) ||
							unresolvedReviewers.some((reviewer) =>
								reviewer.links.some((link) => link.autoInjectReview === false),
							),
						unresolved,
						unresolvedBy: unresolvedReviewers.map((reviewer) => ({
							count: reviewer.count,
							isBot: reviewer.isBot,
							links: reviewer.links.map((link) => ({
								autoInjectReview: link.autoInjectReview,
								body: link.body,
								file: link.file,
								line: link.line,
								pullRequestUrl: github?.url,
								url: link.url,
							})),
							reviewerId: reviewer.reviewerId,
							reviewUrl: reviewer.reviewUrl,
						})),
					}
				: undefined,
			meta: [
				tallpineState ? tallpineReviewMeta(tallpineState) : prLabel,
				unresolved > 0 ? t("inspector.unresolvedCount", { count: unresolved }) : null,
			]
				.filter(Boolean)
				.join(" · "),
			number,
			key,
			title: (tallpineState?.title ?? github?.title)?.trim() || `PR ${prLabel}`,
			verdict: tallpineState ? reviewVerdict(tallpineState) : undefined,
		};
	}).filter((group) =>
		Boolean(group.tallpineState || (group.github && (group.github.entries.length > 0 || group.github.unresolved > 0))),
	);
	return (
		<InspectorReviewsView
			externalLink={ProductExternalLink}
			groups={groups}
			isLoading={isLoading}
			labels={labels}
			onRequestRereview={requestRereview}
			onResolveInlineComment={resolveInlineComment}
			onOpenInTallpineBrowser={openInTallpineBrowser}
			onSendInlineComment={sendInlineCommentToWorker}
			onSendReviewSummary={sendReviewSummaryToWorker}
			onViewInlineCommentInFile={(comment) => {
				if (comment.file) onOpenReviewFile?.({ line: comment.line, path: comment.file });
			}}
			renderAvatar={(harness) => (
				<AgentAvatar className="size-5 shrink-0" decorative provider={harness} />
			)}
			renderMarkdown={renderReviewMarkdown}
		/>
	);
}
