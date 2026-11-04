/** Inspector reviews view and verdict badge. */
import { type ReactNode } from "react";
import type { ExternalLinkComponent } from "../external-link";
import { BotIcon, GitPullRequestIcon } from "../icons";
import { cn } from "../utils";
import { InspectorReviewGroup, InspectorReviewLabels, InspectorGithubReview, InspectorInlineComment, InspectorReviewSummaryAction, InspectorVerdict } from "./review-types";
import { InspectorSection } from "./InspectorShellView";
import { inspectorEmptyClass } from "./inspector-types";
import { ReviewDisclosure, ReviewSourceLabel } from "./ReviewDisclosure";
import { ReviewRuns } from "./ReviewRunHistory";
import { GithubReviewHistory } from "./ExternalReviewCard";

export function InspectorReviewsView({
	externalLink,
	groups,
	isLoading,
	labels,
	onRequestRereview,
	onResolveInlineComment,
	onOpenInTallpineBrowser,
	onSendInlineComment,
	onSendReviewSummary,
	onViewInlineCommentInFile,
	renderAvatar,
	renderMarkdown,
}: {
	externalLink: ExternalLinkComponent;
	groups: InspectorReviewGroup[];
	isLoading: boolean;
	labels: InspectorReviewLabels;
	onRequestRereview?: (review: InspectorGithubReview) => Promise<void> | void;
	onResolveInlineComment?: (comment: InspectorInlineComment & { reviewerId?: string }) => Promise<void> | void;
	onOpenInTallpineBrowser?: (url: string) => void;
	onSendInlineComment?: (comment: InspectorInlineComment & { reviewerId?: string }) => Promise<void> | void;
	onSendReviewSummary?: (summary: InspectorReviewSummaryAction) => Promise<void> | void;
	onViewInlineCommentInFile?: (comment: InspectorInlineComment & { reviewerId?: string }) => void;
	renderAvatar: (harness: string) => ReactNode;
	renderMarkdown: (body: string) => ReactNode;
}) {
	if (isLoading && groups.length === 0) {
		return (
			<InspectorSection surface={false} title={labels.reviews}>
				<p className={inspectorEmptyClass}>{labels.loadingReviews}</p>
			</InspectorSection>
		);
	}
	if (groups.length === 0) return null;
	return (
		<InspectorSection surface={false} title={labels.reviews}>
			<div className="flex flex-col gap-2">
				{groups.map((group) => (
					<ReviewDisclosure
						defaultOpen={false}
						key={group.key ?? group.number}
						meta={group.meta}
						title={group.title}
						verdict={group.verdict}
					>
						{group.tallpineState ? (
							<div className="flex min-w-0 flex-col gap-2">
								<ReviewSourceLabel
									icon={<BotIcon />}
								>
									{labels.tallpineSource}
								</ReviewSourceLabel>
								<ReviewRuns
									dimmed={group.tallpineState.dimmed}
									historical={group.tallpineState.historical}
									labels={labels}
									externalLink={externalLink}
									onOpenInTallpineBrowser={onOpenInTallpineBrowser}
									onResolveInlineComment={onResolveInlineComment}
									onSendInlineComment={onSendInlineComment}
									renderAvatar={renderAvatar}
									renderMarkdown={renderMarkdown}
									runs={group.tallpineState.runs}
									onSendReviewSummary={onSendReviewSummary}
									onViewInlineCommentInFile={onViewInlineCommentInFile}
								/>
							</div>
						) : null}
						{group.github && (group.github.entries.length > 0 || group.github.unresolved > 0) ? (
							<div className="flex min-w-0 flex-col gap-2">
								<ReviewSourceLabel
									icon={<GitPullRequestIcon />}
								>
									{labels.githubSource}
								</ReviewSourceLabel>
								<GithubReviewHistory
									entries={group.github.entries}
									externalLink={externalLink}
									labels={labels}
									onOpenInTallpineBrowser={onOpenInTallpineBrowser}
									onRequestRereview={onRequestRereview}
									onResolveInlineComment={onResolveInlineComment}
									onSendInlineComment={onSendInlineComment}
									onSendReviewSummary={onSendReviewSummary}
									onViewInlineCommentInFile={onViewInlineCommentInFile}
									renderMarkdown={renderMarkdown}
								/>
							</div>
						) : null}
					</ReviewDisclosure>
				))}
			</div>
		</InspectorSection>
	);
}

export const reviewerVerdictTone: Record<InspectorVerdict["tone"], string> = {
	neutral: "text-muted-foreground",
	running: "text-working",
	success: "text-success",
	danger: "text-error",
};

export function VerdictBadge({ className, verdict }: { className?: string; verdict: InspectorVerdict }) {
	return (
		<span className={cn("inline-flex shrink-0 items-center gap-1.5 whitespace-nowrap text-2xs font-medium", reviewerVerdictTone[verdict.tone], className)}>
			<span className="size-1.5 shrink-0 rounded-full bg-current" />
			{verdict.label}
		</span>
	);
}
