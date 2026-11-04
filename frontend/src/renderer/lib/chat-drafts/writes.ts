/** Field-level draft writes and accepted-delivery clearing. */
import { ChatDraftScopeInput, ChatSessionDraft, DraftStorage, DraftWriteResult, ChatDraftAttachment, ChatDraftExcerptReference, ChatDraftQueuedEdit, ChatDraftInlineEditInput, DraftClearResult } from "./types";
import { DraftReadResult, loadChatSessionDraft, persistDraft, rendererStorage, persistDraftProven } from "./persist";
import { invalidateAcceptedDraftMutation, emitComposerDraftChange } from "./store";
import { attachmentsEqual } from "./delivery";
import { draftEditRevision } from "./migrate";


export function mutateDraft(
	scope: ChatDraftScopeInput,
	mutate: (draft: ChatSessionDraft) => ChatSessionDraft,
	storage: DraftStorage | undefined,
	loaded: DraftReadResult = loadChatSessionDraft(scope, storage),
): DraftWriteResult {
	const next = mutate(loaded.draft);
	if (!loaded.ok) return { ok: false, draft: next };
	return persistDraft(next, storage);
}

export function writeChatComposerText(
	scope: ChatDraftScopeInput,
	text: string,
	storage: DraftStorage | undefined = rendererStorage(),
	expectedRevision?: number,
): DraftWriteResult {
	const loaded = loadChatSessionDraft(scope, storage);
	// A delayed retry of a failed save must not overwrite a newer surface's draft.
	if (loaded.ok && expectedRevision !== undefined && loaded.draft.composer.revision !== expectedRevision) {
		return { ok: true, draft: loaded.draft };
	}
	if (loaded.ok && loaded.draft.composer.text === text) {
		return { ok: true, draft: loaded.draft };
	}
	const result = mutateDraft(
		scope,
		(draft) => ({
			...draft,
			composer: {
				...draft.composer,
				revision: draft.composer.revision + 1,
				text,
			},
		}),
		storage,
		loaded,
	);
	if (result.ok) invalidateAcceptedDraftMutation(scope, "composer");
	return result;
}

export function writeChatAttachments(
	scope: ChatDraftScopeInput,
	attachments: ChatDraftAttachment[],
	storage: DraftStorage | undefined = rendererStorage(),
): DraftWriteResult {
	const loaded = loadChatSessionDraft(scope, storage);
	const current = loaded.draft;
	if (loaded.ok && attachmentsEqual(current.composer.attachments, attachments)) {
		return { ok: true, draft: current };
	}
	const result = mutateDraft(
		scope,
		(draft) => ({
			...draft,
			composer: {
				...draft.composer,
				revision: draft.composer.revision + 1,
				attachments,
			},
		}),
		storage,
		loaded,
	);
	if (result.ok) invalidateAcceptedDraftMutation(scope, "composer");
	return result;
}

export function writeChatExcerptReferences(
	scope: ChatDraftScopeInput,
	excerpts: ChatDraftExcerptReference[],
	storage: DraftStorage | undefined = rendererStorage(),
): DraftWriteResult {
	const loaded = loadChatSessionDraft(scope, storage);
	if (loaded.ok && JSON.stringify(loaded.draft.composer.excerpts ?? []) === JSON.stringify(excerpts)) {
		return { ok: true, draft: loaded.draft };
	}
	const result = mutateDraft(
		scope,
		(draft) => ({
			...draft,
			composer: {
				...draft.composer,
				revision: draft.composer.revision + 1,
				excerpts,
			},
		}),
		storage,
		loaded,
	);
	if (result.ok) {
		invalidateAcceptedDraftMutation(scope, "composer");
		emitComposerDraftChange(scope);
	}
	return result;
}

/** Queue edits are independent from both the ordinary prompt and history edits. */
export function writeChatQueuedEdit(
	scope: ChatDraftScopeInput,
	edit: Omit<ChatDraftQueuedEdit, "revision"> | undefined,
	expectedRevision?: string,
	storage: DraftStorage | undefined = rendererStorage(),
	previousUnprovenWrite?: { revisions: (string | undefined)[] },
): DraftWriteResult & { attempted?: { revisions: (string | undefined)[] } } {
	const loaded = loadChatSessionDraft(scope, storage);
	if (!loaded.ok || (expectedRevision !== undefined && loaded.draft.queuedEdit?.revision !== expectedRevision &&
		(!previousUnprovenWrite || !previousUnprovenWrite.revisions.includes(loaded.draft.queuedEdit?.revision)))) {
		return { ok: false, draft: loaded.draft };
	}
	const next = { ...loaded.draft };
	if (edit) next.queuedEdit = { ...edit, revision: draftEditRevision() };
	else delete next.queuedEdit;
	const result = persistDraftProven(next, storage);
	// A write can commit before its readback fails. Permit a later attempt to
	// recognize only the loaded or attempted revision, never another editor's
	// changes. Retain both in case the write itself failed before changing storage.
	return { ...result, attempted: { revisions: [loaded.draft.queuedEdit?.revision, next.queuedEdit?.revision] } };
}

export function writeChatInlineEdit(
	scope: ChatDraftScopeInput,
	inlineEdit: ChatDraftInlineEditInput | undefined,
	storage: DraftStorage | undefined = rendererStorage(),
): DraftWriteResult {
	const result = mutateDraft(
		scope,
		(draft) => {
			const next = { ...draft };
			if (inlineEdit) {
				next.inlineEdit = {
					...inlineEdit,
					revision: draftEditRevision(),
				};
			} else delete next.inlineEdit;
			return next;
		},
		storage,
	);
	if (result.ok) invalidateAcceptedDraftMutation(scope, "inline-edit");
	return result;
}

/**
 * Clear an edit accepted by the daemon only if no later renderer has changed it.
 * This protects a remounted editor from a stale request completing afterward.
 */
export function clearAcceptedChatInlineEdit(
	scope: ChatDraftScopeInput,
	acceptedRevision: string,
	storage: DraftStorage | undefined = rendererStorage(),
): DraftClearResult {
	const loaded = loadChatSessionDraft(scope, storage);
	const current = loaded.draft;
	if (!loaded.ok) return { ok: false, cleared: false, draft: current };
	if (!current.inlineEdit || current.inlineEdit.revision !== acceptedRevision) {
		if (current.inlineEditDelivery?.revision !== acceptedRevision) {
			return { ok: true, cleared: false, draft: current };
		}
		const next = { ...current };
		delete next.inlineEditDelivery;
		const result = persistDraftProven(next, storage);
		return result.ok
			? { ok: true, cleared: false, draft: result.draft }
			: { ok: false, cleared: false, draft: current };
	}
	const next = { ...current };
	delete next.inlineEdit;
	delete next.inlineEditDelivery;
	const result = persistDraftProven(next, storage);
	return result.ok
		? { ok: true, cleared: true, draft: result.draft }
		: { ok: false, cleared: false, draft: current };
}

/**
 * Clear the composer accepted by the daemon only if it is still the same
 * revision. A keystroke or attachment change that happened while send awaited
 * acceptance must remain both on screen and in durable storage.
 * Sent attachments belong to the accepted message, even if the next text is newer.
 */
export function clearAcceptedChatComposer(
	scope: ChatDraftScopeInput,
	acceptedRevision: number,
	storage: DraftStorage | undefined = rendererStorage(),
	acceptedAttachmentIds: readonly string[] = [],
): DraftClearResult {
	const loaded = loadChatSessionDraft(scope, storage);
	const current = loaded.draft;
	if (!loaded.ok) return { ok: false, cleared: false, draft: current };
	if (current.composer.delivery?.draft || current.composer.revision !== acceptedRevision) {
		if (current.composer.delivery?.revision !== acceptedRevision) {
			return { ok: true, cleared: false, draft: current };
		}
		const next: ChatSessionDraft = {
			...current,
			composer: {
				revision: current.composer.revision,
				text: current.composer.text,
				attachments: current.composer.delivery.draft
					? current.composer.attachments
					: current.composer.attachments.filter((attachment) => !acceptedAttachmentIds.includes(attachment.id)),
				excerpts: current.composer.excerpts,
			},
		};
		const result = persistDraftProven(next, storage);
		return result.ok
			? {
				ok: true,
				cleared: Boolean(current.composer.delivery.draft) && next.composer.text === "" && next.composer.attachments.length === 0,
				draft: result.draft,
			}
			: { ok: false, cleared: false, draft: current };
	}
	const next: ChatSessionDraft = {
		...current,
		composer: {
			revision: current.composer.revision + 1,
			text: "",
			attachments: [],
			excerpts: [],
		},
	};
	const result = persistDraftProven(next, storage);
	return result.ok
		? { ok: true, cleared: true, draft: result.draft }
		: { ok: false, cleared: false, draft: current };
}
