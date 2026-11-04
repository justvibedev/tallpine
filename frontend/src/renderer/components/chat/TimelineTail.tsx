import type { RefObject } from "react";
import type { TFunction } from "i18next";
import type { ChatDraftInlineEdit } from "../../lib/chat-drafts";
import type { ConversationTurn } from "../../types/conversation";
import type { TimelineGroup } from "./timeline-groups";
import type { TimelineProps } from "./TimelineProps";
import { LiveResponseStatus } from "./ChatTimelineItems";
import { HumanMessageEditor } from "./HumanMessageEditor";
import { SessionStartup } from "./SessionStartup";

export interface TimelineTailContext {
	translateDraft: (message: string | null | undefined) => string | undefined;
	promptSpacer: RefObject<HTMLDivElement | null>;
	messageEdit: ChatDraftInlineEdit | undefined;
	draftPersistenceError: string | undefined;
	inlineEditRecoveryNotice: string | undefined;
	inlineEditOutcomeNotice: string | undefined;
	inlineEditPending: boolean;
	inlineEditSendBlocked: boolean;
	inlineEditRecoveryLabel: string | undefined;
	canAbandonInlineEditRecovery: boolean;
	turn: ConversationTurn | undefined;
	openingTurnId: string | undefined;
	showStartup: boolean;
	abandonUncertainInlineEdit: () => void;
	updateMessageEdit: (text: string) => void;
	cancelMessageEdit: () => void;
	submitMessageEdit: (text: string) => Promise<void>;
	editedMessageVisible: boolean;
	groups: TimelineGroup[];
	startup: TimelineProps["startup"];
	t: TFunction;
	newWorkDisabled: TimelineProps["newWorkDisabled"];
	editBusy: TimelineProps["editBusy"];
	editError: TimelineProps["editError"];
}

/** Startup status, inline message editor and the prompt spacer below the groups. */
export function TimelineTail(ctx: TimelineTailContext) {
	const {
		translateDraft,
		promptSpacer,
		messageEdit,
		draftPersistenceError,
		inlineEditRecoveryNotice,
		inlineEditOutcomeNotice,
		inlineEditPending,
		inlineEditSendBlocked,
		inlineEditRecoveryLabel,
		canAbandonInlineEditRecovery,
		turn,
		openingTurnId,
		showStartup,
		abandonUncertainInlineEdit,
		updateMessageEdit,
		cancelMessageEdit,
		submitMessageEdit,
		editedMessageVisible,
		groups,
		startup,
		t,
		newWorkDisabled,
		editBusy,
		editError,
	} = ctx;
	return (
		<>
					{/* Normally drawn inside the opening turn's group; this covers a start
					    whose brief is not in the timeline. */}
					{startup && showStartup && !groups.some((group) => group.turnId === openingTurnId) ? (
						<LiveResponseStatus startupLabel={t(startup.failed ? "chat.startup.failed" : "chat.startup.running")} failed={startup.failed}>
							<SessionStartup {...startup} />
						</LiveResponseStatus>
					) : turn?.state === "running" && !groups.some((group) => group.turnId === turn.id) ? (
						<LiveResponseStatus startedAt={turn.startedAt ?? turn.requestedAt} />
					) : null}
					{messageEdit && !editedMessageVisible ? (
						<div className="flex justify-end" data-chat-scroll-anchor="">
							<HumanMessageEditor
								text={messageEdit.text}
								content={messageEdit.content}
								pending={inlineEditPending}
								locked={Boolean(inlineEditRecoveryLabel)}
								recoveryLabel={translateDraft(inlineEditRecoveryLabel)}
								sendBlocked={inlineEditSendBlocked}
								busy={Boolean(editBusy || newWorkDisabled)}
								error={translateDraft(editError ??
									draftPersistenceError ??
									inlineEditRecoveryNotice ??
									inlineEditOutcomeNotice)}
								reconstructedContext={messageEdit.reconstructedContext}
								onDraftChange={updateMessageEdit}
								onCancel={cancelMessageEdit}
								onAbandonRecovery={
									canAbandonInlineEditRecovery ? abandonUncertainInlineEdit : undefined
								}
								onSend={submitMessageEdit}
							/>
						</div>
					) : null}
					{/* Pushes the latest prompt toward the top of the viewport when following,
					    then shrinks as the reply fills the space below it. */}
					<div
						ref={promptSpacer}
						aria-hidden="true"
						className="chat-prompt-spacer"
						data-testid="chat-prompt-spacer"
					/>
		</>
	);
}
