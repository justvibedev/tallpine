import { humanVisibleText } from "@tallpine/shared/chat/automation/worker-reports";
import { stagedAttachmentParts } from "@tallpine/shared/chat/attachments";
import { Pencil } from "lucide-react";
import { cn } from "../../../lib/utils";
import { getApiBaseUrl } from "../../../lib/api-client";
import { SessionLinkedText } from "../ChatMarkdown";
import { CopyButton } from "../CopyButton";
import { HumanMessageEditor } from "../HumanMessageEditor";
import { ConversationBranchNavigator } from "../ConversationBranchNavigator";
import { Tooltip, TooltipContent, TooltipTrigger } from "../../ui/tooltip";
import { ChatAnnotationSummary } from "../ChatAnnotationSummary";
import { type ConversationBranchPoint, type ConversationMessage } from "../../../types/conversation";
import { ProseWithInlineImages, StagedAttachmentItems, MessageTimestamp, QueuedNote, DeliveryNote } from "./MessageParts";

/** The user's bubble. A queued one reads as not-yet-sent rather than as sent-and-ignored. */
export function humanBubbleClass(queued?: boolean): string {
	return cn(
		"cursor-chat-human-message w-fit max-w-[min(78%,560px)] rounded-[10px] px-3 py-2.5 text-sm leading-[1.55]",
		queued ? "border border-dashed border-border-strong bg-transparent text-muted-foreground" : "bg-raised text-foreground",
	);
}

/** What the user typed. Right-aligned and enclosed so it reads as theirs. */
export { humanVisibleText } from "@tallpine/shared/chat/automation/worker-reports";

export function HumanMessage({
	message,
	sessionId,
	apiBaseUrl = getApiBaseUrl(),
	queued,
	animateIn = false,
	onEdit,
	editing = false,
	editText,
	editReconstructedContext = false,
	onEditStart,
	onEditDraftChange,
	onEditCancel,
	onEditAbandonRecovery,
	editPending = false,
	editSendBlocked = false,
	editRecoveryLabel,
	editBusy = false,
	editError,
	branchPoint,
	onActivateBranch,
	activateBranchPending = false,
	activateBranchError,
	onSelectAnnotation,
}: {
	message: ConversationMessage;
	/** The staged paths are relative to this session's workspace. */
	sessionId: string;
	/** The live daemon origin; passed by the timeline so daemon restarts refresh images. */
	apiBaseUrl?: string | null;
	/** Typed while the agent was busy, and not sent yet. */
	queued?: boolean;
	/** True only for a human message added after the timeline first mounted. */
	animateIn?: boolean;
	onEdit?: (turnId: string, text: string) => Promise<unknown> | void;
	editing?: boolean;
	editText?: string;
	editReconstructedContext?: boolean;
	onEditStart?: () => void;
	onEditDraftChange?: (text: string) => void;
	onEditCancel?: () => void;
	onEditAbandonRecovery?: () => void;
	editPending?: boolean;
	editSendBlocked?: boolean;
	editRecoveryLabel?: string;
	editBusy?: boolean;
	editError?: string;
	branchPoint?: ConversationBranchPoint;
	onActivateBranch?: (branchId: string) => Promise<unknown> | void;
	activateBranchPending?: boolean;
	activateBranchError?: string;
	onSelectAnnotation?: (annotation: { text: string; messageId?: string; revision?: number }) => void;
}) {
	const visibleMessageText = humanVisibleText(message.text);
	const { body, attachments } = stagedAttachmentParts(visibleMessageText);
	const excerptReferences = (message.content ?? []).filter(
		(content) => content.type === "excerpt" && Boolean(content.text),
	);
	return (
		<div
			className="group/message flex flex-col items-end gap-1"
			data-chat-message-id={!queued && !editing ? message.id : undefined}
			data-chat-message-revision={!queued && !editing ? message.revision : undefined}
			data-chat-message-role={!queued && !editing ? message.role : undefined}
		>
			{/* A queued message reads as not-yet-sent rather than as sent-and-ignored:
			    the agent has not seen it, and the timeline should not imply it has. */}
			{editing ? (
				<HumanMessageEditor
					text={editText ?? visibleMessageText}
					content={message.content ?? []}
					pending={editPending}
					locked={Boolean(editRecoveryLabel)}
					recoveryLabel={editRecoveryLabel}
					sendBlocked={editSendBlocked}
					busy={editBusy}
					reconstructedContext={editReconstructedContext}
					error={editError}
					onDraftChange={onEditDraftChange}
					onCancel={() => onEditCancel?.()}
					onAbandonRecovery={onEditAbandonRecovery}
					onSend={(text) => {
						if (!message.turnId || !onEdit) return;
						return onEdit(message.turnId, text);
					}}
				/>
			) : (
				<div
					/* Themes draw sent and queued differently; light theme needs to tell them
					   apart in CSS because it paints an enclosure only around a sent one. */
					data-queued={queued ? "" : undefined}
					className={cn(humanBubbleClass(queued), animateIn && "chat-human-message-enter")}
				>
					{excerptReferences.length > 0 ? (
						<ChatAnnotationSummary
							annotations={excerptReferences.map((excerpt) => ({
								text: excerpt.text ?? "",
								conversationId: excerpt.sourceConversationId,
 messageId: excerpt.sourceMessageId,
								revision: excerpt.sourceRevision,
							}))}
							onSelect={onSelectAnnotation}
							className="mb-2"
						/>
					) : null}
					{body ? (
						<p data-chat-message-body="" className="break-words whitespace-pre-wrap text-pretty">
							<ProseWithInlineImages
								text={body}
								attachments={attachments}
								sessionId={sessionId}
								apiBaseUrl={apiBaseUrl}
								renderText={(text) => <SessionLinkedText text={text} />}
							/>
						</p>
					) : null}
					<StagedAttachmentItems
						paths={attachments}
						sessionId={sessionId}
						apiBaseUrl={apiBaseUrl}
						ariaLabel="Attached files"
						className={cn(body && "mt-2")}
					/>
				</div>
			)}
			{editing ? null : (
				<div className="mt-1 flex h-7 items-center gap-1">
					<div className="flex items-center gap-1 opacity-0 transition-opacity duration-150 ease-out focus-within:opacity-100 group-hover/message:opacity-100 motion-reduce:transition-none">
						<MessageTimestamp createdAt={message.createdAt} />
						{onEdit && onEditStart && message.turnId ? (
							<Tooltip>
								<TooltipTrigger asChild>
									<button
										type="button"
										onClick={onEditStart}
										aria-label="Edit user message"
										className="flex size-7 items-center justify-center rounded-md text-muted-foreground transition-[scale,background-color,color] duration-150 ease-out hover:bg-interactive-hover hover:text-foreground active:scale-[0.96] motion-reduce:transition-none motion-reduce:active:scale-100"
									>
										<Pencil aria-hidden="true" className="size-3" />
									</button>
								</TooltipTrigger>
								<TooltipContent side="bottom">Edit user message</TooltipContent>
							</Tooltip>
						) : null}
						<CopyButton
							text={visibleMessageText}
							label="Copy user message"
							compact
							className="size-7 justify-center rounded-md px-0 py-0 transition-[scale,background-color,color] duration-150 ease-out hover:bg-interactive-hover hover:text-foreground active:scale-[0.96] motion-reduce:transition-none motion-reduce:active:scale-100"
						/>
					</div>
					{branchPoint && onActivateBranch ? (
						<ConversationBranchNavigator
							point={branchPoint}
							pending={activateBranchPending}
							error={activateBranchError}
							onActivate={onActivateBranch}
						/>
					) : null}
				</div>
			)}
			{queued ? <QueuedNote /> : null}
			{message.delivery && message.delivery !== "accepted" && message.delivery !== "sending" ? (
				<DeliveryNote state={message.delivery} />
			) : null}
		</div>
	);
}
