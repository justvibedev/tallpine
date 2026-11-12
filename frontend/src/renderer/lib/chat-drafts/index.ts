/**
 * Renderer-owned, session-scoped Chat drafts.
 *
 * Electron pins this origin's storage beneath Tallpine's userData directory, so a
 * synchronous localStorage write survives Chat surface teardown, renderer
 * reload, and a supported app restart without putting daemon state in the UI.
 * Attachment bytes are deliberately absent: a descriptor is written only after
 * the daemon has durably staged those bytes in the session worktree.
 */


export { clearRejectedChatComposerDelivery, clearRejectedChatInlineEditDelivery, clearUncertainChatComposerDelivery, clearUncertainChatInlineEditDelivery, markChatComposerDeliveryAccepted, markChatInlineEditDeliveryAccepted, prepareChatComposerDelivery, prepareChatInlineEditDelivery } from "./delivery";
export { loadChatSessionDraft, readChatSessionDraft } from "./persist";
export { activateChatDraftScope } from "./scope-lease";
export { acknowledgeChatComposerMutation, acknowledgeChatInlineEditMutation, beginChatComposerMutation, beginChatInlineEditMutation, cancelChatComposerMutation, cancelChatInlineEditMutation, chatDraftScopeKey, chatDraftScopeSessionId, chatQueuedAttachmentScopeKey, finishChatComposerMutation, finishChatInlineEditMutation, getChatComposerMutation, getChatInlineEditMutation, isChatComposerMutationCurrent, subscribeChatDraftRuntime } from "./store";
export { CHAT_DRAFT_SCHEMA_VERSION } from "./types";
export type { ChatComposerDelivery, ChatDraftAttachment, ChatDraftAttachmentResize, ChatDraftDeliveryState, ChatDraftExcerptReference, ChatDraftInlineEdit, ChatDraftInlineEditInput, ChatDraftMutationSnapshot, ChatDraftMutationToken, ChatDraftQueuedEdit, ChatDraftRetainedAttachment, ChatDraftScope, ChatDraftScopeActivationResult, ChatInlineEditDelivery, ChatSessionDraft, DraftClearResult, DraftDeliveryResult, DraftStorage, DraftWriteResult, PrepareChatComposerDeliveryInput, PrepareChatInlineEditDeliveryInput } from "./types";
export { clearAcceptedChatComposer, clearAcceptedChatInlineEdit, writeChatAttachments, writeChatComposerText, writeChatExcerptReferences, writeChatInlineEdit, writeChatQueuedEdit } from "./writes";
