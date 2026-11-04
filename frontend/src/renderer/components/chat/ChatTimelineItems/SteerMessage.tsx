import { stripSenderPrefix as stripSteerSenderPrefix } from "@tallpine/shared/chat/automation/sender-prefix";
import { stagedAttachmentParts, IMAGE_ATTACHMENT_PATH } from "@tallpine/shared/chat/attachments";
import { useState } from "react";
import { CornerDownRight } from "lucide-react";
import { cn } from "../../../lib/utils";
import { getApiBaseUrl } from "../../../lib/api-client";
import { ChatMarkdown, SessionLabelLink, SessionLinkedText } from "../ChatMarkdown";
import { ConversationContentItems } from "../ConversationContentItems";
import { type ConversationActivity } from "../../../types/conversation";
import { ProseWithInlineImages, StagedAttachmentItems } from "./MessageParts";
import { ORIGIN_REPORT_COLLAPSE_AT, ORIGIN_REPORT_PREVIEW_LENGTH, AutomationMessageFrame, AutomationExpandButton } from "./OriginMessage";

/**
 * Guidance the user delivered into a turn that was already running.
 *
 * Drawn as the user's own words, because they are — the daemon records it as an
 * activity only because a message would have opened a second turn. It carries a
 * marker rather than looking identical to an ordinary message: a reader scrolling
 * back needs to see that this arrived mid-turn, since it explains why the agent
 * changed course without a new exchange starting.
 */
export function SteerMessage({
	activity,
	sessionId,
	apiBaseUrl = getApiBaseUrl(),
}: {
	activity: ConversationActivity;
	sessionId: string;
	apiBaseUrl?: string | null;
}) {
	const [expanded, setExpanded] = useState(false);
	const text = activity.detail?.text ?? activity.summary;
	const senderSessionId = activity.detail?.senderSessionId?.trim();
	const senderProjectId = activity.detail?.senderProjectId?.trim();
	const senderLabel = activity.detail?.senderDisplayName?.trim() || senderSessionId;
	const automationSteer = Boolean(senderSessionId);
	const senderHref = senderSessionId && senderProjectId
		? `tallpine://sessions/${encodeURIComponent(senderProjectId)}/${encodeURIComponent(senderSessionId)}`
		: undefined;
	const visibleText = senderSessionId ? stripSteerSenderPrefix(text, senderSessionId) : text;
	const { body, attachments } = stagedAttachmentParts(visibleText);
	const longReport = body.length > ORIGIN_REPORT_COLLAPSE_AT;
	const preview = longReport
		? `${body.slice(0, ORIGIN_REPORT_PREVIEW_LENGTH).trimEnd()}…`
		: body;
	let stagedImagesToMatch = attachments.filter((path) => IMAGE_ATTACHMENT_PATH.test(path)).length;
	// Composer images are recorded twice: once as durable staged paths and once as
	// native prompt blocks. Suppress only the corresponding leading native images;
	// later image blocks may be distinct provider content and must remain visible.
	const remainingContent = (activity.detail?.content ?? []).filter((item) => {
		if (item.type !== "image" || stagedImagesToMatch === 0) return true;
		stagedImagesToMatch -= 1;
		return false;
	});
	const automationBody = (
		<>
			{longReport && expanded ? (
				<ChatMarkdown text={body} muted />
			) : body ? (
				<p className={cn("whitespace-pre-wrap text-sm leading-relaxed text-muted-foreground", longReport && "line-clamp-3")}>
					<SessionLinkedText text={preview} />
				</p>
			) : null}
				<StagedAttachmentItems
					paths={attachments}
					sessionId={sessionId}
					apiBaseUrl={apiBaseUrl}
					ariaLabel="Steered attachments"
					className={cn(body && "mt-2")}
				/>
				<ConversationContentItems
					content={remainingContent}
					ariaLabel={attachments.length > 0 ? "Steered content" : "Steered attachments"}
					imageLabel="Image"
					imageAlt={(position) => `Steered attachment ${position}`}
					className={cn((body || attachments.length > 0) && "mt-2")}
				/>
			{longReport ? <AutomationExpandButton expanded={expanded} onClick={() => setExpanded((current) => !current)} /> : null}
		</>
	);
	return (
		<div className={cn("flex flex-col gap-1", automationSteer ? "items-stretch" : "items-end")}>
			{automationSteer ? (
				<AutomationMessageFrame
					label={<>[from {senderHref ? <SessionLabelLink href={senderHref}>{senderLabel}</SessionLabelLink> : senderLabel}]</>}
					createdAt={activity.createdAt}
				>
					{automationBody}
				</AutomationMessageFrame>
			) : (
				<div className="break-words whitespace-pre-wrap text-sm leading-[1.55] w-fit max-w-[min(78%,560px)] rounded-[10px] bg-raised px-3 py-2.5 text-foreground">
					{body ? (
						<p>
							<ProseWithInlineImages
								text={body}
								attachments={attachments}
								sessionId={sessionId}
								apiBaseUrl={apiBaseUrl}
								renderText={(text) => text}
							/>
						</p>
					) : null}
					<StagedAttachmentItems
						paths={attachments}
						sessionId={sessionId}
						apiBaseUrl={apiBaseUrl}
						ariaLabel="Steered attachments"
						className={cn(body && "mt-2")}
					/>
					<ConversationContentItems
						content={remainingContent}
						ariaLabel={attachments.length > 0 ? "Steered content" : "Steered attachments"}
						imageLabel="Image"
						imageAlt={(position) => `Steered attachment ${position}`}
						className={cn((body || attachments.length > 0) && "mt-2")}
					/>
				</div>
			)}
			<span className="flex items-center gap-1 text-xs text-muted-foreground">
				<CornerDownRight aria-hidden="true" className="size-3" />
				Steered
			</span>
		</div>
	);
}

export { stripSenderPrefix as stripSteerSenderPrefix } from "@tallpine/shared/chat/automation/sender-prefix";

/* -------------------------------------------------------------------------- */
/* approval                                                                    */
/* -------------------------------------------------------------------------- */
