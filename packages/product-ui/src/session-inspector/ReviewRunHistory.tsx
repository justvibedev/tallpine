/** Review runs, run history and pager. */
import { type ReactNode, useEffect, useState } from "react";
import type { ExternalLinkComponent } from "../external-link";
import { ChevronIcon } from "../icons";
import { cn } from "../utils";
import { InspectorReviewLabels, InspectorInlineComment, InspectorReviewSummaryAction, InspectorReviewRun } from "./review-types";
import { inspectorEmptyClass } from "./inspector-types";
import { ReviewSummaryCard } from "./ReviewSummaryCard";

export function ReviewRuns({
	dimmed,
	externalLink,
	historical,
	labels,
	onOpenInTallpineBrowser,
	onResolveInlineComment,
	onSendInlineComment,
	onSendReviewSummary,
	onViewInlineCommentInFile,
	renderAvatar,
	renderMarkdown,
	runs,
}: {
	dimmed?: boolean;
	externalLink: ExternalLinkComponent;
	historical?: boolean;
	labels: InspectorReviewLabels;
	onOpenInTallpineBrowser?: (url: string) => void;
	onResolveInlineComment?: (comment: InspectorInlineComment & { reviewerId?: string }) => Promise<void> | void;
	onSendInlineComment?: (comment: InspectorInlineComment & { reviewerId?: string }) => Promise<void> | void;
	onSendReviewSummary?: (summary: InspectorReviewSummaryAction) => Promise<void> | void;
	onViewInlineCommentInFile?: (comment: InspectorInlineComment & { reviewerId?: string }) => void;
	renderAvatar: (harness: string) => ReactNode;
	renderMarkdown: (body: string) => ReactNode;
	runs: InspectorReviewRun[];
}) {
	if (runs.length === 0) {
		return <p className={cn(inspectorEmptyClass, "m-0")}>{labels.noPastReviewSummaries}</p>;
	}
	return (
		<ReviewRunHistory
			dimmed={dimmed}
			externalLink={externalLink}
			historical={historical}
			labels={labels}
			onOpenInTallpineBrowser={onOpenInTallpineBrowser}
			onResolveInlineComment={onResolveInlineComment}
			onSendInlineComment={onSendInlineComment}
			onSendReviewSummary={onSendReviewSummary}
			onViewInlineCommentInFile={onViewInlineCommentInFile}
			renderAvatar={renderAvatar}
			renderMarkdown={renderMarkdown}
			runs={runs}
		/>
	);
}

export function ReviewRunHistory({
	dimmed,
	externalLink,
	historical,
	labels,
	onOpenInTallpineBrowser,
	onResolveInlineComment,
	onSendInlineComment,
	onSendReviewSummary,
	onViewInlineCommentInFile,
	renderAvatar,
	renderMarkdown,
	runs,
}: {
	dimmed?: boolean;
	externalLink: ExternalLinkComponent;
	historical?: boolean;
	labels: InspectorReviewLabels;
	onOpenInTallpineBrowser?: (url: string) => void;
	onResolveInlineComment?: (comment: InspectorInlineComment & { reviewerId?: string }) => Promise<void> | void;
	onSendInlineComment?: (comment: InspectorInlineComment & { reviewerId?: string }) => Promise<void> | void;
	onSendReviewSummary?: (summary: InspectorReviewSummaryAction) => Promise<void> | void;
	onViewInlineCommentInFile?: (comment: InspectorInlineComment & { reviewerId?: string }) => void;
	renderAvatar: (harness: string) => ReactNode;
	renderMarkdown: (body: string) => ReactNode;
	runs: InspectorReviewRun[];
}) {
	const latestKey = runs[0]?.id ?? "";
	const [visibleCount, setVisibleCount] = useState(1);
	useEffect(() => setVisibleCount(1), [latestKey]);
	const visible = runs.slice(0, visibleCount);
	const remaining = Math.max(0, runs.length - visible.length);
	return (
		<div className={cn("flex min-w-0 flex-col gap-2", dimmed && "opacity-70")}>
			{visible.map((run, index) => (
				<ReviewSummaryCard
					actor={run.harness || "reviewer"}
					body={run.status === "cancelled" || run.status === "failed" ? "" : run.body}
					externalLink={externalLink}
					isEarlier={historical || index > 0}
					key={run.id}
					labels={labels}
					renderAvatar={renderAvatar}
					renderMarkdown={renderMarkdown}
					onOpenInTallpineBrowser={onOpenInTallpineBrowser}
					onResolveInlineComment={onResolveInlineComment}
					onSendInlineComment={onSendInlineComment}
					onSendReviewSummary={onSendReviewSummary}
					onViewInlineCommentInFile={onViewInlineCommentInFile}
					inlineComments={run.inlineComments}
					resolvedComments={run.resolvedComments}
					source="agent"
					testId="review-run-summary"
					timestamp={run.createdAtLabel}
					url={run.url}
					verdict={run.verdict}
				/>
			))}
			<ReviewHistoryPager
				labels={labels}
				onCollapse={visibleCount > 1 ? () => setVisibleCount(1) : undefined}
				onLoadMore={
					remaining > 0
						? () => setVisibleCount((count) => Math.min(runs.length, count + REVIEW_HISTORY_PAGE_SIZE))
						: undefined
				}
				remaining={remaining}
			/>
		</div>
	);
}

export const REVIEW_HISTORY_PAGE_SIZE = 3;

export function ReviewHistoryPager({
	labels,
	onCollapse,
	onLoadMore,
	remaining,
}: {
	labels: InspectorReviewLabels;
	onCollapse?: () => void;
	onLoadMore?: () => void;
	remaining: number;
}) {
	if (!onCollapse && (!onLoadMore || remaining === 0)) return null;
	return (
		<div className="flex min-w-0 gap-1.5">
			{onCollapse ? (
				<button
					className="flex min-h-8 min-w-0 flex-1 items-center justify-center gap-1.5 rounded-md border border-dashed border-border px-2 py-1.5 text-xs font-medium leading-none text-muted-foreground transition-colors hover:border-border-strong hover:bg-interactive-hover/30 hover:text-foreground"
					onClick={onCollapse}
					type="button"
				>
					<ChevronIcon className="size-icon-2xs shrink-0" direction="up" />
					<span className="truncate">{labels.showLatestReviewOnly}</span>
				</button>
			) : null}
			{remaining > 0 && onLoadMore ? (
				<button
					className="flex min-h-8 min-w-0 flex-1 items-center justify-center gap-1.5 rounded-md border border-dashed border-border px-2 py-1.5 text-xs font-medium leading-none text-muted-foreground transition-colors hover:border-border-strong hover:bg-interactive-hover/30 hover:text-foreground"
					onClick={onLoadMore}
					type="button"
				>
					<ChevronIcon className="size-icon-2xs shrink-0" direction="down" />
					<span className="truncate">{labels.loadMoreReviews(remaining)}</span>
				</button>
			) : null}
		</div>
	);
}
