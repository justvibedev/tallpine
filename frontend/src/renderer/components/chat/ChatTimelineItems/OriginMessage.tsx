import { useState, type ReactNode } from "react";
import { ChevronRight, CircleAlert } from "lucide-react";
import { cn } from "../../../lib/utils";
import { ChatMarkdown, OriginPreviewMarkdown, SessionLabelLink } from "../ChatMarkdown";
import { type ConversationMessage } from "../../../types/conversation";
import { stripSteerSenderPrefix } from "./SteerMessage";
import { formatTime } from "./format";

export const ORIGIN_REPORT_COLLAPSE_AT = 600;

export const ORIGIN_REPORT_PREVIEW_LENGTH = 240;

function originReportPreview(text: string): string {
	const cut = text.slice(0, ORIGIN_REPORT_PREVIEW_LENGTH);
	const lastCompleteLine = cut.lastIndexOf("\n");
	const preview = lastCompleteLine > 0 ? cut.slice(0, lastCompleteLine) : cut;
	return `${preview.trimEnd()}…`;
}

/**
 * A message from a worker, automation, or the daemon. Attribution comes from the
 * durable origin field, never from a prefix parsed out of the text.
 */
export function OriginMessage({ message }: { message: ConversationMessage }) {
	const [expanded, setExpanded] = useState(false);
	const senderSessionId = message.senderSessionId?.trim();
	const senderProjectId = message.senderProjectId?.trim();
	const senderLabel = message.senderDisplayName?.trim() || senderSessionId;
	const senderHref = senderSessionId && senderProjectId
		? `tallpine://sessions/${encodeURIComponent(senderProjectId)}/${encodeURIComponent(senderSessionId)}`
		: undefined;
	const visibleText = senderSessionId ? stripSteerSenderPrefix(message.text, senderSessionId) : message.text;
	const longReport = visibleText.length > ORIGIN_REPORT_COLLAPSE_AT;
	const preview = longReport
		? originReportPreview(visibleText)
		: visibleText;

	return (
		<AutomationMessageFrame
			label={senderSessionId ? <>{"[from "}{senderHref ? <SessionLabelLink href={senderHref}>{senderLabel}</SessionLabelLink> : senderLabel}{"]"}</> : message.senderLabel ?? message.origin}
			createdAt={message.createdAt}
		>
			{longReport && !expanded ? (
				<div className="line-clamp-3">
					<OriginPreviewMarkdown text={preview} />
				</div>
			) : (
				<ChatMarkdown text={visibleText} muted safeOrigin className="whitespace-pre-wrap text-sm leading-relaxed" />
			)}
			{longReport ? <AutomationExpandButton expanded={expanded} onClick={() => setExpanded((current) => !current)} /> : null}
		</AutomationMessageFrame>
	);
}

export function AutomationMessageFrame({
	label,
	createdAt,
	children,
}: {
	label: ReactNode;
	createdAt: string;
	children: ReactNode;
}) {
	return (
		<div className="cursor-chat-origin-message rounded-md border border-border border-l-2 border-l-logo-accent/60 px-3.5 py-2.5">
			<div className="mb-1.5 flex min-w-0 items-center gap-2 text-xs font-medium text-muted-foreground">
				<CircleAlert aria-hidden="true" className="size-3.5 shrink-0 text-logo-accent" />
				<span className="min-w-0 truncate">{label}</span>
				<span className="ml-auto shrink-0 font-normal tabular-nums">{formatTime(createdAt)}</span>
			</div>
			{children}
		</div>
	);
}

export function AutomationExpandButton({ expanded, onClick }: { expanded: boolean; onClick: () => void }) {
	return (
		<button
			type="button"
			onClick={onClick}
			aria-expanded={expanded}
			className="mt-2 flex items-center gap-1 text-xs font-medium text-logo-accent transition-colors hover:text-markdown-link-hover"
		>
			<ChevronRight aria-hidden="true" className={cn("size-3 transition-transform", expanded && "rotate-90")} />
			{expanded ? "Hide report" : "Show full report"}
		</button>
	);
}
