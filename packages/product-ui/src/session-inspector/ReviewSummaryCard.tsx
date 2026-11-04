/** Review summary card, markdown body and actions. */
import { type ReactNode, type RefObject, useEffect, useState } from "react";
import type { ExternalLinkComponent } from "../external-link";
import { MoreHorizontalIcon } from "../icons";
import { cn } from "../utils";
import { InspectorReviewLabels, InspectorInlineComment, InspectorReviewSummaryAction, InspectorVerdict } from "./review-types";
import { useRenderedOverflow, ReviewLinks, useClickAway } from "./review-hooks";
import { VerdictBadge } from "./InspectorReviewsView";
import { GithubInlineComments, ResolvedInlineComments } from "./InlineComments";

export function ReviewSummaryCard({
	actor,
	body: rawBody,
	externalLink,
	isBot = false,
	isEarlier = false,
	labels,
	inlineComments = [],
	onOpenInTallpineBrowser,
	onResolveInlineComment,
	onSendInlineComment,
	onSendReviewSummary,
	onViewInlineCommentInFile,
	renderAvatar,
	renderMarkdown,
	resolvedComments = [],
	source,
	testId,
	timestamp,
	url,
	verdict,
}: {
	actor: string;
	body?: string;
	externalLink: ExternalLinkComponent;
	isBot?: boolean;
	isEarlier?: boolean;
	labels: InspectorReviewLabels;
	inlineComments?: InspectorInlineComment[];
	onOpenInTallpineBrowser?: (url: string) => void;
	onResolveInlineComment?: (comment: InspectorInlineComment & { reviewerId?: string }) => Promise<void> | void;
	onSendInlineComment?: (comment: InspectorInlineComment & { reviewerId?: string }) => Promise<void> | void;
	onSendReviewSummary?: (summary: InspectorReviewSummaryAction) => Promise<void> | void;
	onViewInlineCommentInFile?: (comment: InspectorInlineComment & { reviewerId?: string }) => void;
	renderAvatar: (harness: string) => ReactNode;
	renderMarkdown: (body: string) => ReactNode;
	resolvedComments?: InspectorInlineComment[];
	source: InspectorReviewSummaryAction["source"];
	testId: string;
	timestamp: string;
	url?: string | null;
	verdict: InspectorVerdict;
}) {
	const [expanded, setExpanded] = useState(false);
	const trimmed = rawBody?.trim();
	const body = trimmed ? trimmed.replace(/\n{3,}/g, "\n\n") : trimmed;
	const { ref: bodyRef, isOverflowing } = useRenderedOverflow<HTMLDivElement>(body ?? "", "vertical", !expanded);
	useEffect(() => setExpanded(false), [body]);
	return (
		<article className="flex min-w-0 flex-col gap-1 rounded-md bg-overlay/50 px-2.5 py-2.5">
			<span className="grid min-w-0 grid-cols-[minmax(0,1fr)_auto_auto] items-center gap-x-1.5 gap-y-1 @max-[420px]/inspector:grid-cols-[minmax(0,1fr)_auto]">
				<span className="inline-flex min-w-0 items-center gap-1.5">
					<span className="inline-flex min-w-0 flex-1 items-center gap-1 text-micro font-medium text-muted-foreground">
						{renderAvatar(actor)}
						<span className="truncate">{actor}</span>
						{isBot ? <span className="shrink-0 font-mono text-micro text-passive">{labels.bot}</span> : null}
					</span>
					<VerdictBadge className="shrink-0" verdict={verdict} />
				</span>
				<span className="ml-auto inline-flex min-w-0 shrink-0 flex-wrap items-center justify-end gap-x-1.5 gap-y-0.5 text-right text-micro text-passive @max-[420px]/inspector:col-span-2 @max-[420px]/inspector:col-start-1 @max-[420px]/inspector:row-start-2 @max-[420px]/inspector:ml-0 @max-[420px]/inspector:justify-self-start @max-[420px]/inspector:text-left">
					{isEarlier ? <span>{labels.earlierPass}</span> : null}
					<span className="font-mono">{timestamp}</span>
				</span>
				<ReviewSummaryActions body={body ?? ""} className="@max-[420px]/inspector:col-start-2 @max-[420px]/inspector:row-start-1" externalLink={externalLink} labels={labels} onOpenInTallpineBrowser={onOpenInTallpineBrowser} onSendReviewSummary={onSendReviewSummary} reviewerId={actor} source={source} url={url} />
			</span>
			{body ? (
				<ReviewMarkdownBody
					body={body}
					clamped={!expanded}
					elementRef={bodyRef}
					renderMarkdown={renderMarkdown}
					testId={testId}
				/>
			) : null}
			<ReviewLinks
				clamped={isOverflowing}
				expanded={expanded}
				labels={labels}
				onExpandedChange={() => setExpanded((open) => !open)}
			/>
			<GithubInlineComments
				comments={inlineComments}
				externalLink={externalLink}
				labels={labels}
				onResolveInlineComment={onResolveInlineComment}
				onSendInlineComment={onSendInlineComment}
				onViewInlineCommentInFile={onViewInlineCommentInFile}
				reviewerId={actor}
				reviewUrl={url ?? undefined}
			/>
			{resolvedComments.length > 0 ? (
				<ResolvedInlineComments
					comments={resolvedComments}
					externalLink={externalLink}
					labels={labels}
					onViewInlineCommentInFile={onViewInlineCommentInFile}
					reviewerId={actor}
					reviewUrl={url ?? undefined}
				/>
			) : null}
		</article>
	);
}

export function ReviewMarkdownBody({
	body,
	clamped,
	elementRef,
	renderMarkdown,
	testId,
}: {
	body: string;
	clamped: boolean;
	elementRef?: RefObject<HTMLDivElement | null>;
	renderMarkdown: (body: string) => ReactNode;
	testId: string;
}) {
	return (
		<div
			className={cn(
				"min-w-0 select-text break-words text-xs leading-relaxed text-muted-foreground",
				"[&_a]:font-medium [&_a]:text-foreground [&_a]:underline [&_a]:underline-offset-2",
				"[&_code]:rounded [&_code]:bg-muted/55 [&_code]:px-1 [&_code]:py-0.5 [&_code]:font-mono [&_code]:text-foreground",
				"[&_li]:my-0.5 [&_ol]:my-1.5 [&_ol]:list-decimal [&_ol]:pl-4 [&_p]:my-1.5 [&_pre]:my-2",
				"[&_pre]:overflow-x-auto [&_pre]:rounded-md [&_pre]:border [&_pre]:border-border [&_pre]:bg-muted/35 [&_pre]:p-2",
				"[&_pre_code]:bg-transparent [&_pre_code]:p-0 [&_strong]:text-foreground [&_table]:my-2 [&_table]:w-full",
				"[&_table]:border-collapse [&_td]:border [&_td]:border-border [&_td]:px-2 [&_td]:py-1",
				"[&_th]:border [&_th]:border-border [&_th]:px-2 [&_th]:py-1 [&_th]:text-foreground",
				"[&_ul]:my-1.5 [&_ul]:list-disc [&_ul]:pl-4 [&>*:first-child]:mt-0 [&>*:last-child]:mb-0",
				clamped && "line-clamp-4",
			)}
			data-testid={testId}
			data-overflow-axis="vertical"
			ref={elementRef}
		>
			{renderMarkdown(body)}
		</div>
	);
}

export function ReviewSummaryActions({
	body,
	className,
	externalLink: ExternalLink,
	labels,
	onOpenInTallpineBrowser,
	onRequestRereview,
	onSendReviewSummary,
	pullRequestUrl,
	reviewerId,
	source,
	url,
}: {
	body: string;
	className?: string;
	externalLink: ExternalLinkComponent;
	labels: InspectorReviewLabels;
	onOpenInTallpineBrowser?: (url: string) => void;
	onRequestRereview?: () => Promise<void> | void;
	onSendReviewSummary?: (summary: InspectorReviewSummaryAction) => Promise<void> | void;
	pullRequestUrl?: string;
	reviewerId: string;
	source: InspectorReviewSummaryAction["source"];
	url?: string | null;
}) {
	const [menuOpen, setMenuOpen] = useState(false);
	const menuRef = useClickAway<HTMLSpanElement>(menuOpen, () => setMenuOpen(false));
	const [rereviewState, setRereviewState] = useState<"idle" | "requesting" | "requested" | "error">("idle");
	const [sendState, setSendState] = useState<"idle" | "sending" | "sent" | "error">("idle");
	const canSend = Boolean(body && onSendReviewSummary);
	if (!url && !canSend && !onRequestRereview) return null;
	const requestRereview = async () => {
		if (!onRequestRereview || rereviewState === "requesting") return;
		setRereviewState("requesting");
		try {
			await onRequestRereview();
			setRereviewState("requested");
		} catch {
			setRereviewState("error");
		}
	};
	const send = async () => {
		if (!onSendReviewSummary || !body || sendState === "sending") return;
		setSendState("sending");
		try {
			await onSendReviewSummary({ body, pullRequestUrl, reviewerId, source, url });
			setSendState("sent");
		} catch {
			setSendState("error");
		}
	};
	return (
		<span className={cn("relative flex shrink-0", className)} onClick={(event) => event.stopPropagation()} ref={menuRef}>
			<button aria-expanded={menuOpen} aria-label={labels.reviewActions} className="inline-flex size-7 items-center justify-center rounded-md border border-border/70 text-muted-foreground transition-colors hover:border-border-strong hover:bg-interactive-hover hover:text-foreground" onClick={() => setMenuOpen((current) => !current)} type="button">
				<MoreHorizontalIcon className="size-icon-xs" />
			</button>
			{menuOpen ? (
				<span className="isolate absolute right-0 top-8 z-[100] flex w-48 flex-col rounded-md border border-border-strong bg-[var(--color-bg-settings-menu)] p-1 text-2xs shadow-[0_16px_40px_rgba(0,0,0,0.65)]">
					{canSend ? <button className={cn("rounded px-2 py-1.5 text-left hover:bg-interactive-hover disabled:pointer-events-none", sendState === "sent" ? "text-success" : sendState === "error" ? "text-error" : "text-muted-foreground hover:text-foreground")} disabled={sendState === "sending" || sendState === "sent"} onClick={() => void send()} type="button">{sendState === "sent" ? labels.sentToWorkerAgent : sendState === "error" ? labels.sendToWorkerAgentError : labels.sendToWorkerAgent}</button> : null}
					{onRequestRereview ? <button className={cn("rounded px-2 py-1.5 text-left hover:bg-interactive-hover disabled:pointer-events-none", rereviewState === "requested" ? "text-success" : rereviewState === "error" ? "text-error" : "text-muted-foreground hover:text-foreground")} disabled={rereviewState === "requesting" || rereviewState === "requested"} onClick={() => void requestRereview()} type="button">{rereviewState === "requested" ? labels.rereviewRequested : rereviewState === "error" ? labels.rereviewRequestFailed : labels.requestRereviewPR}</button> : null}
					{url && onOpenInTallpineBrowser ? <button className="rounded px-2 py-1.5 text-left text-muted-foreground hover:bg-interactive-hover hover:text-foreground" onClick={() => onOpenInTallpineBrowser(url)} type="button">{labels.openInTallpineBrowser}</button> : null}
					{url ? <ExternalLink className="rounded px-2 py-1.5 text-muted-foreground no-underline hover:bg-interactive-hover hover:text-foreground" href={url}>{labels.openInSystemBrowser}</ExternalLink> : null}
				</span>
			) : null}
		</span>
	);
}
