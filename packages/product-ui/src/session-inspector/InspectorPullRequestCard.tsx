/** Pull request card for the inspector summary. */
import { type ReactNode } from "react";
import type { ExternalLinkComponent } from "../external-link";
import { ArrowUpRightIcon, GitPullRequestIcon } from "../icons";
import { PRCardStatusSummary, PRSummaryMeta, type CountNounLabel } from "../PRSummaryDisplay";
import type { PRCardPresentation, PRSummaryMetadata } from "../pull-request-models";
import { cn } from "../utils";

export type InspectorPullRequestState = "open" | "draft" | "merged" | "closed";

export type InspectorPullRequest = PRSummaryMetadata & {
	card: PRCardPresentation;
	href: string;
	number: number;
	state: InspectorPullRequestState;
	stateLabel: string;
	title?: string;
	/** owner/name, shown before the number when a session's PRs span several repositories. */
	repositoryLabel?: string;
	reviewDetailsAction?: ReactNode;
};

export const prStateTone: Record<InspectorPullRequestState, string> = {
	open: "border-border-strong bg-overlay text-muted-foreground",
	draft: "border-status-in-review/35 bg-status-in-review/10 text-status-in-review",
	merged: "border-border-strong bg-overlay text-success",
	closed: "border-error/40 bg-error/10 text-error",
};

export function InspectorPullRequestCardView({
	countNounLabel,
	externalIcon,
	externalLink: ExternalLink,
	mergeAction,
	mergeError,
	openLabel,
	openedViaLabel,
	pr,
	pullRequestIcon,
	statusNotice,
}: {
	countNounLabel: CountNounLabel;
	externalIcon?: ReactNode;
	externalLink: ExternalLinkComponent;
	mergeAction?: ReactNode;
	mergeError?: string | null;
	openLabel: string;
	openedViaLabel?: string;
	pr: InspectorPullRequest;
	pullRequestIcon?: ReactNode;
	statusNotice?: ReactNode;
}) {
	return (
		<article className="min-w-0 w-full rounded-lg border border-(--color-border-settings-input) bg-(--color-bg-settings-input) px-3 py-2.5">
			{pr.title ? (
				<ExternalLink
					className="inline text-sm font-semibold leading-snug tracking-tight text-settings-label underline-offset-2 hover:underline focus-visible:rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
					href={pr.href}
				>
					{pr.title}
				</ExternalLink>
			) : null}
			<div className={cn("flex min-w-0 items-center gap-2", pr.title && "mt-1.5")}>
				<ExternalLink
					ariaLabel={openLabel}
					className="inline-flex min-w-0 items-center gap-1 font-mono text-xs font-medium text-settings-label decoration-muted-foreground underline-offset-2 hover:text-settings-label hover:underline focus-visible:rounded-sm focus-visible:text-settings-label focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
					href={pr.href}
				>
					{pullRequestIcon ?? <GitPullRequestIcon className="size-icon-sm shrink-0" />}
					<span className="truncate">{pr.repositoryLabel ? `${pr.repositoryLabel} ` : ""}PR #{pr.number}</span>
					{externalIcon ?? <ArrowUpRightIcon className="size-icon-2xs shrink-0" />}
				</ExternalLink>
				<span
					className={cn(
						"inline-flex h-5 shrink-0 items-center justify-center gap-1 overflow-hidden whitespace-nowrap rounded-full border border-transparent px-2 py-0.5 text-xs font-medium transition-[background-color,border-color,color,box-shadow] focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 [&>svg]:pointer-events-none [&>svg]:size-3",
						"border-border text-foreground hover:bg-muted",
						"h-5 px-1.5 text-[9px] leading-none font-medium",
						prStateTone[pr.state],
					)}
					data-slot="badge"
				>
					{pr.stateLabel}
				</span>
			</div>
			<PRSummaryMeta
				className="mt-1.5"
				countNounLabel={countNounLabel}
				externalLink={ExternalLink}
				openedViaLabel={openedViaLabel}
				pr={pr}
			/>
			{pr.state !== "merged" ? (
				<>
					<PRCardStatusSummary
						action={mergeAction}
						className="mt-2"
						externalLink={ExternalLink}
						presentation={pr.card}
						reviewDetailsAction={pr.reviewDetailsAction}
					/>
					{statusNotice}
					{mergeError ? (
						<p className="mt-2 text-xs leading-normal text-error" role="status">
							{mergeError}
						</p>
					) : null}
				</>
			) : null}
		</article>
	);
}
