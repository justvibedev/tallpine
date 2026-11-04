/** Inline review comment lists and rows. */
import { useEffect, useState } from "react";
import type { ExternalLinkComponent } from "../external-link";
import { CheckIcon, ChevronIcon, MoreHorizontalIcon } from "../icons";
import { cn } from "../utils";
import { InspectorInlineComment, InspectorReviewLabels } from "./review-types";
import { useRenderedOverflow } from "./review-hooks";

export function GithubInlineComments({
	comments,
	externalLink: ExternalLink,
	labels,
	onResolveInlineComment,
	onSendInlineComment,
	onViewInlineCommentInFile,
	reviewerId,
	reviewUrl,
}: {
	comments: InspectorInlineComment[];
	externalLink: ExternalLinkComponent;
	labels: InspectorReviewLabels;
	onResolveInlineComment?: (comment: InspectorInlineComment & { reviewerId?: string }) => Promise<void> | void;
	onSendInlineComment?: (comment: InspectorInlineComment & { reviewerId?: string }) => Promise<void> | void;
	onViewInlineCommentInFile?: (comment: InspectorInlineComment & { reviewerId?: string }) => void;
	reviewerId: string;
	reviewUrl?: string;
}) {
	const [open, setOpen] = useState(true);
	if (comments.length === 0) return null;
	return (
		<section className="min-w-0" data-testid="github-inline-comments">
			<button aria-expanded={open} className="flex min-h-8 w-full min-w-0 items-center gap-2 rounded-md px-1.5 py-1 text-left text-xs font-medium leading-none text-muted-foreground transition-colors hover:bg-interactive-hover/20 hover:text-foreground" onClick={() => setOpen((current) => !current)} type="button">
				<ChevronIcon className="size-icon-2xs shrink-0" direction={open ? "down" : "right"} />
				<span>{labels.openComments} · {comments.length}</span>
			</button>
			{open ? <InlineCommentList comments={comments} externalLink={ExternalLink} labels={labels} onResolveInlineComment={onResolveInlineComment} onSendInlineComment={onSendInlineComment} onViewInlineCommentInFile={onViewInlineCommentInFile} reviewerId={reviewerId} reviewUrl={reviewUrl} /> : null}
		</section>
	);
}

export function ResolvedInlineComments({
	comments,
	externalLink: ExternalLink,
	labels,
	onViewInlineCommentInFile,
	reviewerId,
	reviewUrl,
}: {
	comments: InspectorInlineComment[];
	externalLink: ExternalLinkComponent;
	labels: InspectorReviewLabels;
	onViewInlineCommentInFile?: (comment: InspectorInlineComment & { reviewerId?: string }) => void;
	reviewerId: string;
	reviewUrl?: string;
}) {
	const [open, setOpen] = useState(false);
	return (
		<section className="min-w-0 border-t border-border/60 pt-2" data-testid="github-resolved-comments">
			<button aria-expanded={open} className="flex min-h-8 w-full min-w-0 items-center gap-2 rounded-md px-1.5 py-1 text-left text-xs font-medium leading-none text-muted-foreground transition-colors hover:bg-interactive-hover/20 hover:text-foreground" onClick={() => setOpen((current) => !current)} type="button">
				<ChevronIcon className="size-icon-2xs shrink-0" direction={open ? "down" : "right"} />
				<span>{labels.resolvedComments(comments.length)}</span>
			</button>
			{open ? <InlineCommentList comments={comments} externalLink={ExternalLink} labels={labels} onViewInlineCommentInFile={onViewInlineCommentInFile} reviewerId={reviewerId} reviewUrl={reviewUrl} /> : null}
		</section>
	);
}

export function InlineCommentList({
	comments,
	externalLink,
	labels,
	onResolveInlineComment,
	onSendInlineComment,
	onViewInlineCommentInFile,
	reviewerId,
	reviewUrl,
}: {
	comments: InspectorInlineComment[];
	externalLink: ExternalLinkComponent;
	labels: InspectorReviewLabels;
	onResolveInlineComment?: (comment: InspectorInlineComment & { reviewerId?: string }) => Promise<void> | void;
	onSendInlineComment?: (comment: InspectorInlineComment & { reviewerId?: string }) => Promise<void> | void;
	onViewInlineCommentInFile?: (comment: InspectorInlineComment & { reviewerId?: string }) => void;
	reviewerId: string;
	reviewUrl?: string;
}) {
	const [manuallyResolvedCommentIds, setManuallyResolvedCommentIds] = useState<Set<string>>(() => new Set());
	const [manuallySentCommentIds, setManuallySentCommentIds] = useState<Set<string>>(() => new Set());
	const [resolvingCommentIds, setResolvingCommentIds] = useState<Set<string>>(() => new Set());
	const [resolveErrorCommentIds, setResolveErrorCommentIds] = useState<Set<string>>(() => new Set());
	const [sendingCommentIds, setSendingCommentIds] = useState<Set<string>>(() => new Set());
	const [sendErrorCommentIds, setSendErrorCommentIds] = useState<Set<string>>(() => new Set());
	const keyedComments = comments.map((comment, index) => ({
		...comment,
		id: `${reviewerId}:${comment.url ?? `${comment.file ?? ""}:${comment.line ?? ""}:${index}`}`,
		reviewerId: comment.reviewerId || reviewerId,
		url: comment.url || reviewUrl,
	}));
	return (
		<div className="divide-y divide-border/60">
			{keyedComments.map((comment) => (
				<InlineCommentRow
					comment={comment}
					externalLink={externalLink}
					key={comment.id}
					labels={labels}
					onResolve={!comment.resolved && onResolveInlineComment ? async () => {
						setResolvingCommentIds((current) => new Set(current).add(comment.id));
						setResolveErrorCommentIds((current) => {
							const next = new Set(current);
							next.delete(comment.id);
							return next;
						});
						try {
							await onResolveInlineComment(comment);
							setManuallyResolvedCommentIds((current) => new Set(current).add(comment.id));
						} catch {
							setResolveErrorCommentIds((current) => new Set(current).add(comment.id));
						} finally {
							setResolvingCommentIds((current) => {
								const next = new Set(current);
								next.delete(comment.id);
								return next;
							});
						}
					} : undefined}
					onSend={comment.autoInjectReview === false && onSendInlineComment ? async () => {
						setSendingCommentIds((current) => new Set(current).add(comment.id));
						setSendErrorCommentIds((current) => {
							const next = new Set(current);
							next.delete(comment.id);
							return next;
						});
						try {
							await onSendInlineComment(comment);
							setManuallySentCommentIds((current) => new Set(current).add(comment.id));
						} catch {
							setSendErrorCommentIds((current) => new Set(current).add(comment.id));
						} finally {
							setSendingCommentIds((current) => {
								const next = new Set(current);
								next.delete(comment.id);
								return next;
							});
						}
					} : undefined}
					onViewInFile={comment.file && onViewInlineCommentInFile ? () => onViewInlineCommentInFile(comment) : undefined}
					resolveError={resolveErrorCommentIds.has(comment.id)}
					resolvedSuccess={manuallyResolvedCommentIds.has(comment.id)}
					resolving={resolvingCommentIds.has(comment.id)}
					sendError={sendErrorCommentIds.has(comment.id)}
					sent={Boolean(comment.autoInjectReview) || manuallySentCommentIds.has(comment.id)}
					sending={sendingCommentIds.has(comment.id)}
				/>
			))}
		</div>
	);
}

export function InlineCommentRow({
	comment,
	externalLink: ExternalLink,
	labels,
	onResolve,
	onSend,
	onViewInFile,
	resolveError = false,
	resolvedSuccess = false,
	resolving = false,
	sendError = false,
	sending = false,
	sent,
}: {
	comment: InspectorInlineComment & { id: string; reviewerId?: string };
	externalLink: ExternalLinkComponent;
	labels: InspectorReviewLabels;
	onResolve?: () => void;
	onSend?: () => void;
	onViewInFile?: () => void;
	resolveError?: boolean;
	resolvedSuccess?: boolean;
	resolving?: boolean;
	sendError?: boolean;
	sending?: boolean;
	sent: boolean;
}) {
	const [expanded, setExpanded] = useState(false);
	const [menuOpen, setMenuOpen] = useState(false);
	const body = comment.body?.trim();
	const fileLabel = comment.file ? `${comment.file}${comment.line ? `:${comment.line}` : ""}` : labels.commentNumber(1);
	const preview = body ? body.split("\n")[0] : "";
	const hasHiddenLines = Boolean(body && body.split("\n").slice(1).some((line) => line.trim()));
	const { ref: previewRef, isOverflowing: previewOverflows } = useRenderedOverflow<HTMLSpanElement>(preview, "horizontal", !expanded);
	const canExpand = hasHiddenLines || previewOverflows;
	useEffect(() => {
		if (!canExpand && expanded) setExpanded(false);
	}, [canExpand, expanded]);
	const copy = async (value?: string) => {
		if (!value) return;
		try {
			await navigator.clipboard?.writeText(value);
		} catch {
			// Clipboard access may be unavailable in tests or restricted contexts.
		}
	};
	return (
		<div className="relative flex min-w-0 flex-col gap-1.5 py-2.5 text-xs">
			<div
				{...(canExpand ? { "aria-expanded": expanded, role: "button", tabIndex: 0 } : {})}
				className={cn("w-full min-w-0 rounded-md py-1 text-left transition-colors", canExpand && "cursor-pointer hover:bg-interactive-hover/20")}
				onClick={canExpand ? () => setExpanded((current) => !current) : undefined}
				onKeyDown={canExpand ? (event) => {
					if (event.key === "Enter" || event.key === " ") {
						event.preventDefault();
						setExpanded((current) => !current);
					}
				} : undefined}
			>
				<span className="flex min-w-0 items-start gap-2">
					<span className="flex min-w-0 flex-1 items-center gap-1.5 font-mono text-2xs font-semibold text-foreground">
						{canExpand ? <ChevronIcon className="size-icon-2xs shrink-0 text-passive" direction={expanded ? "down" : "right"} /> : null}
						<span className="truncate" title={fileLabel}>{fileLabel}</span>
					</span>
					<span className="flex shrink-0 items-center justify-end" onClick={(event) => event.stopPropagation()}>
						{sent ? (
							<span aria-label={labels.sentToWorkerAgent} className="inline-flex size-7 items-center justify-center rounded-md text-success" role="status" title={labels.sentToWorkerAgent}>
								<CheckIcon className="size-icon-xs shrink-0" />
							</span>
						) : null}
					</span>
					<span className="relative flex shrink-0 items-start justify-end" onClick={(event) => event.stopPropagation()}>
						<button aria-expanded={menuOpen} aria-label="Comment actions" className="inline-flex size-7 items-center justify-center rounded-md border border-border/70 text-muted-foreground transition-colors hover:border-border-strong hover:bg-interactive-hover hover:text-foreground" onClick={() => setMenuOpen((current) => !current)} type="button">
							<MoreHorizontalIcon className="size-icon-xs" />
						</button>
						{menuOpen ? (
							<div className="isolate absolute right-0 top-8 z-[100] flex w-40 flex-col rounded-md border border-border-strong bg-[var(--color-bg-settings-menu)] p-1 text-2xs shadow-[0_16px_40px_rgba(0,0,0,0.65)]">
								{onSend && !sent ? <button className="rounded px-2 py-1.5 text-left text-muted-foreground hover:bg-interactive-hover hover:text-foreground disabled:pointer-events-none disabled:opacity-60" disabled={sending} onClick={() => { setMenuOpen(false); onSend(); }} type="button">{labels.sendToWorkerAgent}</button> : null}
								{onResolve ? <button className="rounded px-2 py-1.5 text-left text-muted-foreground hover:bg-interactive-hover hover:text-foreground disabled:pointer-events-none disabled:opacity-60" disabled={resolving} onClick={() => void onResolve()} type="button">{labels.resolveComment}</button> : null}
								{onViewInFile ? <button className="rounded px-2 py-1.5 text-left text-muted-foreground hover:bg-interactive-hover hover:text-foreground" onClick={onViewInFile} type="button">{labels.viewInFile}</button> : null}
								{comment.url ? <ExternalLink className="rounded px-2 py-1.5 text-muted-foreground no-underline hover:bg-interactive-hover hover:text-foreground" href={comment.url}>Open on GitHub</ExternalLink> : null}
								{comment.url ? <button className="rounded px-2 py-1.5 text-left text-muted-foreground hover:bg-interactive-hover hover:text-foreground" onClick={() => void copy(comment.url)} type="button">Copy comment link</button> : null}
							</div>
						) : null}
					</span>
				</span>
				{body ? <span data-overflow-axis="horizontal" ref={previewRef} className={cn("mt-1 block min-w-0 text-muted-foreground", expanded ? "whitespace-pre-wrap break-words" : "truncate")}>{expanded ? body : preview}</span> : null}
			</div>
			{resolvedSuccess ? <p className="m-0 text-2xs font-medium text-success">{labels.resolvedReview}</p> : null}
			{resolveError ? <p className="m-0 text-2xs font-medium text-error">{labels.resolveReviewFailed}</p> : null}
			{sendError && !sent ? <p className="m-0 text-2xs font-medium text-error">{labels.sendToWorkerAgentError}</p> : null}
		</div>
	);
}
