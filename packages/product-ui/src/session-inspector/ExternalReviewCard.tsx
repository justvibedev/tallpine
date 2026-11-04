/** GitHub review history and external review cards. */
import { type ReactNode, useState } from "react";
import type { ExternalLinkComponent } from "../external-link";
import { scmUserAvatarUrl } from "../scm-avatar";
import { cn } from "../utils";
import { UserAvatar } from "../UserAvatar";
import { InspectorGithubReview, InspectorReviewLabels, InspectorInlineComment, InspectorReviewSummaryAction } from "./review-types";
import { useRenderedOverflow } from "./review-hooks";
import { reviewerVerdictTone } from "./InspectorReviewsView";
import { ReviewSummaryActions, ReviewMarkdownBody } from "./ReviewSummaryCard";
import { GithubInlineComments, ResolvedInlineComments } from "./InlineComments";

export function GithubReviewHistory({
	entries,
	externalLink,
	labels,
	onOpenInTallpineBrowser,
	onRequestRereview,
	onResolveInlineComment,
	onSendInlineComment,
	onSendReviewSummary,
	onViewInlineCommentInFile,
	renderMarkdown,
}: {
	entries: InspectorGithubReview[];
	externalLink: ExternalLinkComponent;
	labels: InspectorReviewLabels;
	onOpenInTallpineBrowser?: (url: string) => void;
	onRequestRereview?: (review: InspectorGithubReview) => Promise<void> | void;
	onResolveInlineComment?: (comment: InspectorInlineComment & { reviewerId?: string }) => Promise<void> | void;
	onSendInlineComment?: (comment: InspectorInlineComment & { reviewerId?: string }) => Promise<void> | void;
	onSendReviewSummary?: (summary: InspectorReviewSummaryAction) => Promise<void> | void;
	onViewInlineCommentInFile?: (comment: InspectorInlineComment & { reviewerId?: string }) => void;
	renderMarkdown: (body: string) => ReactNode;
}) {
	const sorted = [...entries].sort((a, b) => b.submittedAt.localeCompare(a.submittedAt));
	if (entries.length === 0) return null;
	return (
		<div className="flex min-w-0 flex-col gap-2">
			{sorted.map((entry) => (
				<ExternalReviewCard
					defaultOpen={false}
					entry={entry}
					externalLink={externalLink}
					key={entry.id}
					labels={labels}
					onRequestRereview={onRequestRereview}
					onResolveInlineComment={onResolveInlineComment}
					onOpenInTallpineBrowser={onOpenInTallpineBrowser}
					onSendInlineComment={onSendInlineComment}
					onSendReviewSummary={onSendReviewSummary}
					onViewInlineCommentInFile={onViewInlineCommentInFile}
					renderMarkdown={renderMarkdown}
				/>
			))}
		</div>
	);
}

export function ExternalReviewCard({
	defaultOpen,
	entry,
	externalLink,
	labels,
	onOpenInTallpineBrowser,
	onRequestRereview,
	onResolveInlineComment,
	onSendInlineComment,
	onSendReviewSummary,
	onViewInlineCommentInFile,
	renderMarkdown,
}: {
	defaultOpen: boolean;
	entry: InspectorGithubReview;
	externalLink: ExternalLinkComponent;
	labels: InspectorReviewLabels;
	onOpenInTallpineBrowser?: (url: string) => void;
	onRequestRereview?: (review: InspectorGithubReview) => Promise<void> | void;
	onResolveInlineComment?: (comment: InspectorInlineComment & { reviewerId?: string }) => Promise<void> | void;
	onSendInlineComment?: (comment: InspectorInlineComment & { reviewerId?: string }) => Promise<void> | void;
	onSendReviewSummary?: (summary: InspectorReviewSummaryAction) => Promise<void> | void;
	onViewInlineCommentInFile?: (comment: InspectorInlineComment & { reviewerId?: string }) => void;
	renderMarkdown: (body: string) => ReactNode;
}) {
	const [open, setOpen] = useState(defaultOpen);
	const body = entry.body?.trim();
	const openComments = (entry.inlineComments ?? []).filter((comment) => comment.body?.trim() || comment.file || comment.url);
	const resolvedComments = (entry.resolvedComments ?? []).filter((comment) => comment.body?.trim() || comment.file || comment.url);
	const { ref: bodyRef, isOverflowing: bodyOverflows } = useRenderedOverflow<HTMLDivElement>(body ?? "", "vertical", !open);
	const hasNestedContent = openComments.length > 0 || resolvedComments.length > 0;
	const collapsible = bodyOverflows || hasNestedContent;
	const headerContent = (
		<>
			<UserAvatar
				className="size-6 shrink-0"
				imageUrl={scmUserAvatarUrl("github", entry.pullRequestUrl, entry.reviewerId)}
				name={entry.reviewerId}
			/>
			<span className="flex min-w-0 flex-col gap-0.5">
				<span className="flex min-w-0 items-center gap-1.5 text-xs font-semibold text-foreground">
					<span className="min-w-0 break-words">{entry.reviewerId}</span>
					{entry.isBot ? <span className="shrink-0 font-mono text-micro text-passive">{labels.bot}</span> : null}
				</span>
				{entry.submittedAtLabel ? <span className="font-mono text-micro text-passive">{labels.reviewedAt(entry.submittedAtLabel)}</span> : null}
			</span>
			<span className={cn("flex shrink-0 items-center gap-2 whitespace-nowrap pr-1 text-2xs font-medium @max-[420px]/inspector:col-start-2 @max-[420px]/inspector:row-start-2 @max-[420px]/inspector:justify-self-start", reviewerVerdictTone[entry.verdict.tone])}>
				<span>{entry.verdict.label}</span>
				{openComments.length > 0 ? (
					<span className="rounded-sm px-0.5 text-muted-foreground" title={labels.openInlineComments(openComments.length)}>{openComments.length}</span>
				) : null}
			</span>
		</>
	);
	return (
		<article className="relative min-w-0 border-b border-border/70 py-2 first:pt-0 last:border-b-0 last:pb-0" data-testid="github-review-card">
			<div className="grid w-full min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-1 rounded-md pr-1" data-testid="external-review-header">
				{collapsible ? (
					<button aria-expanded={open} className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 rounded-md px-1.5 py-1.5 text-left transition-colors hover:bg-interactive-hover/30 @max-[420px]/inspector:grid-cols-[auto_minmax(0,1fr)] @max-[420px]/inspector:gap-x-2" onClick={() => setOpen((current) => !current)} type="button">
						{headerContent}
					</button>
				) : (
					<div className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 px-1.5 py-1.5 text-left @max-[420px]/inspector:grid-cols-[auto_minmax(0,1fr)] @max-[420px]/inspector:gap-x-2">
						{headerContent}
					</div>
				)}
				<ReviewSummaryActions body={body ?? ""} externalLink={externalLink} labels={labels} onOpenInTallpineBrowser={onOpenInTallpineBrowser} onRequestRereview={entry.canRequestRereview ? () => onRequestRereview?.(entry) : undefined} onSendReviewSummary={onSendReviewSummary} pullRequestUrl={entry.pullRequestUrl} reviewerId={entry.reviewerId} source="external" url={entry.reviewUrl || entry.pullRequestUrl} />
			</div>
			<div className="flex min-w-0 flex-col gap-3 px-1 pt-2 text-left">
				{body ? (
					<ReviewMarkdownBody body={body} clamped={!open} elementRef={bodyRef} renderMarkdown={renderMarkdown} testId="github-review-summary" />
				) : null}
				{open ? <GithubInlineComments comments={openComments} externalLink={externalLink} labels={labels} onResolveInlineComment={onResolveInlineComment} onSendInlineComment={onSendInlineComment} onViewInlineCommentInFile={onViewInlineCommentInFile} reviewerId={entry.reviewerId} reviewUrl={entry.reviewUrl} /> : null}
				{open && resolvedComments.length > 0 ? <ResolvedInlineComments comments={resolvedComments} externalLink={externalLink} labels={labels} onViewInlineCommentInFile={onViewInlineCommentInFile} reviewerId={entry.reviewerId} reviewUrl={entry.reviewUrl} /> : null}
			</div>
		</article>
	);
}
