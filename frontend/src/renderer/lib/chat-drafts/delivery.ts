/** Composer and inline-edit delivery state transitions. */
import { ChatDraftAttachment, ChatDraftScopeInput, PrepareChatComposerDeliveryInput, DraftStorage, DraftDeliveryResult, ChatComposerDelivery, ChatSessionDraft, DraftWriteResult, PrepareChatInlineEditDeliveryInput, ChatInlineEditDelivery, ChatDraftInlineEdit } from "./types";
import { rendererStorage, loadChatSessionDraft, persistDraftProven } from "./persist";
import { draftEditRevision } from "./migrate";

export function attachmentsEqual(
	current: ChatDraftAttachment[],
	next: ChatDraftAttachment[],
): boolean {
	return (
		current.length === next.length &&
		current.every((attachment, index) => {
			const candidate = next[index];
			return (
				candidate !== undefined &&
				attachment.id === candidate.id &&
				attachment.path === candidate.path &&
				attachment.name === candidate.name &&
				attachment.mimeType === candidate.mimeType &&
				attachment.bytes === candidate.bytes
			);
		})
	);
}

/**
 * Atomically proves the exact composer snapshot and its stable delivery id
 * before the renderer is allowed to call the daemon.
 */
export function prepareChatComposerDelivery(
	scope: ChatDraftScopeInput,
	input: PrepareChatComposerDeliveryInput,
	storage: DraftStorage | undefined = rendererStorage(),
): DraftDeliveryResult<ChatComposerDelivery> {
	const loaded = loadChatSessionDraft(scope, storage);
	if (!loaded.ok) return { ok: false, recovered: false, draft: loaded.draft };
	const existing = loaded.draft.composer.delivery;
	if (existing) {
		const proof = persistDraftProven(loaded.draft, storage);
		return proof.ok
			? { ok: true, recovered: true, draft: proof.draft, mutation: existing }
			: { ok: false, recovered: true, draft: loaded.draft };
	}
	const excerpts = input.excerpts ?? [];
	const exact =
		loaded.draft.composer.text === input.composerText &&
		attachmentsEqual(loaded.draft.composer.attachments, input.attachments) &&
		JSON.stringify(loaded.draft.composer.excerpts ?? []) === JSON.stringify(excerpts);
	const revision = exact
		? loaded.draft.composer.revision
		: loaded.draft.composer.revision + 1;
	// shortcut: excerpt sends stay locked, enable optimism when refused references can be edited separately.
	const optimistic = input.optimistic && input.kind === "send" && excerpts.length === 0;
	const mutation: ChatComposerDelivery = {
		...(input.nativeImages === undefined ? {} : { nativeImages: input.nativeImages }),
		kind: input.kind,
		state: "dispatching",
		revision,
		clientMessageId: input.clientMessageId,
		requestText: input.requestText,
		...(optimistic ? { draft: { text: input.composerText, attachments: input.attachments } } : {}),
		excerpts,
	};
	const next: ChatSessionDraft = {
		...loaded.draft,
		composer: {
			revision: optimistic ? revision + 1 : revision,
			text: optimistic ? "" : input.composerText,
			attachments: optimistic ? [] : input.attachments,
			excerpts,
			delivery: mutation,
		},
	};
	const result = persistDraftProven(next, storage);
	return result.ok
		? { ok: true, recovered: false, draft: result.draft, mutation }
		: { ok: false, recovered: false, draft: loaded.draft };
}

/** Persist the daemon's acceptance before attempting to remove the draft. */
export function markChatComposerDeliveryAccepted(
	scope: ChatDraftScopeInput,
	clientMessageId: string,
	revision: number,
	storage: DraftStorage | undefined = rendererStorage(),
): DraftWriteResult {
	const loaded = loadChatSessionDraft(scope, storage);
	const delivery = loaded.draft.composer.delivery;
	if (
		!loaded.ok ||
		!delivery ||
		delivery.clientMessageId !== clientMessageId ||
		delivery.revision !== revision
	) {
		return { ok: false, draft: loaded.draft };
	}
	if (delivery.state === "accepted") return { ok: true, draft: loaded.draft };
	const result = persistDraftProven(
		{
			...loaded.draft,
			composer: {
				...loaded.draft.composer,
				delivery: { ...delivery, state: "accepted" },
			},
		},
		storage,
	);
	return result.ok ? result : { ok: false, draft: loaded.draft };
}

/**
 * Restore a definitively refused draft, or keep its delivery beside newer input
 * for retry. Legacy sends and steers only lose their delivery journal.
 */
export function clearRejectedChatComposerDelivery(
	scope: ChatDraftScopeInput,
	clientMessageId: string,
	revision: number,
	storage: DraftStorage | undefined = rendererStorage(),
): DraftWriteResult {
	const loaded = loadChatSessionDraft(scope, storage);
	const delivery = loaded.draft.composer.delivery;
	if (
		!loaded.ok ||
		!delivery ||
		delivery.state !== "dispatching" ||
		delivery.clientMessageId !== clientMessageId ||
		delivery.revision !== revision
	) {
		return { ok: false, draft: loaded.draft };
	}
	if (delivery.draft && (loaded.draft.composer.text !== "" || loaded.draft.composer.attachments.length > 0)) {
		return { ok: true, draft: loaded.draft };
	}
	const next: ChatSessionDraft = {
		...loaded.draft,
		composer: {
			revision: loaded.draft.composer.revision + (delivery.draft ? 1 : 0),
			text: delivery.draft?.text ?? loaded.draft.composer.text,
			attachments: delivery.draft?.attachments ?? loaded.draft.composer.attachments,
			excerpts: loaded.draft.composer.excerpts,
		},
	};
	const result = persistDraftProven(next, storage);
	return result.ok ? result : { ok: false, draft: loaded.draft };
}

/**
 * Explicitly abandon a steer whose acceptance is unknowable. This is deliberately
 * separate from definitive refusal: the provider may already have received it.
 * Only the renderer journal is removed; text and staged descriptors remain so the
 * person can decide whether a later submit is worth the duplicate-delivery risk.
 */
export function clearUncertainChatComposerDelivery(
	scope: ChatDraftScopeInput,
	clientMessageId: string,
	revision: number,
	storage: DraftStorage | undefined = rendererStorage(),
): DraftWriteResult {
	const loaded = loadChatSessionDraft(scope, storage);
	const delivery = loaded.draft.composer.delivery;
	if (
		!loaded.ok ||
		!delivery ||
		delivery.kind !== "steer" ||
		delivery.state !== "dispatching" ||
		delivery.clientMessageId !== clientMessageId ||
		delivery.revision !== revision
	) {
		return { ok: false, draft: loaded.draft };
	}
	const next: ChatSessionDraft = {
		...loaded.draft,
		composer: {
			revision: loaded.draft.composer.revision,
			text: loaded.draft.composer.text,
			attachments: loaded.draft.composer.attachments,
			excerpts: loaded.draft.composer.excerpts,
		},
	};
	const result = persistDraftProven(next, storage);
	return result.ok ? result : { ok: false, draft: loaded.draft };
}

/**
 * Atomically proves an inline edit and its stable branch-mutation id before
 * dispatch. A restored journal is returned unchanged for explicit safe retry.
 */
export function prepareChatInlineEditDelivery(
	scope: ChatDraftScopeInput,
	input: PrepareChatInlineEditDeliveryInput,
	storage: DraftStorage | undefined = rendererStorage(),
): DraftDeliveryResult<ChatInlineEditDelivery> {
	const loaded = loadChatSessionDraft(scope, storage);
	if (!loaded.ok) return { ok: false, recovered: false, draft: loaded.draft };
	if (loaded.draft.inlineEditDelivery) {
		const proof = persistDraftProven(loaded.draft, storage);
		return proof.ok
			? {
					ok: true,
					recovered: true,
					draft: proof.draft,
					mutation: loaded.draft.inlineEditDelivery,
				}
			: { ok: false, recovered: true, draft: loaded.draft };
	}
	const current = loaded.draft.inlineEdit;
	const exact =
			current?.turnId === input.turnId &&
			current.text === input.text &&
			current.reconstructedContext === input.reconstructedContext &&
			JSON.stringify(current.content) === JSON.stringify(input.content);
	const inlineEdit: ChatDraftInlineEdit = exact
		? current
		: {
					turnId: input.turnId,
					text: input.text,
					content: input.content,
					reconstructedContext: input.reconstructedContext,
					revision: draftEditRevision(),
			};
	const mutation: ChatInlineEditDelivery = {
		kind: "inline-edit",
		state: "dispatching",
		revision: inlineEdit.revision,
		clientMessageId: input.clientMessageId,
		turnId: input.turnId,
		requestText: input.text,
	};
	const next: ChatSessionDraft = {
		...loaded.draft,
		inlineEdit,
		inlineEditDelivery: mutation,
	};
	const result = persistDraftProven(next, storage);
	return result.ok
		? { ok: true, recovered: false, draft: result.draft, mutation }
		: { ok: false, recovered: false, draft: loaded.draft };
}

export function markChatInlineEditDeliveryAccepted(
	scope: ChatDraftScopeInput,
	clientMessageId: string,
	revision: string,
	storage: DraftStorage | undefined = rendererStorage(),
): DraftWriteResult {
	const loaded = loadChatSessionDraft(scope, storage);
	const delivery = loaded.draft.inlineEditDelivery;
	if (
		!loaded.ok ||
		!delivery ||
		delivery.clientMessageId !== clientMessageId ||
		delivery.revision !== revision
	) {
		return { ok: false, draft: loaded.draft };
	}
	if (delivery.state === "accepted") return { ok: true, draft: loaded.draft };
	const result = persistDraftProven(
		{
			...loaded.draft,
			inlineEditDelivery: { ...delivery, state: "accepted" },
		},
		storage,
	);
	return result.ok ? result : { ok: false, draft: loaded.draft };
}

/** Remove only the journal for an edit the daemon durably proved it rejected. */
export function clearRejectedChatInlineEditDelivery(
	scope: ChatDraftScopeInput,
	clientMessageId: string,
	revision: string,
	storage: DraftStorage | undefined = rendererStorage(),
): DraftWriteResult {
	const loaded = loadChatSessionDraft(scope, storage);
	const delivery = loaded.draft.inlineEditDelivery;
	if (
		!loaded.ok ||
		!delivery ||
		delivery.state !== "dispatching" ||
		delivery.clientMessageId !== clientMessageId ||
		delivery.revision !== revision
	) {
		return { ok: false, draft: loaded.draft };
	}
	const next = { ...loaded.draft };
	delete next.inlineEditDelivery;
	const result = persistDraftProven(next, storage);
	return result.ok ? result : { ok: false, draft: loaded.draft };
}

/** Explicitly abandon an inline edit whose provider acceptance is unknowable. */
export function clearUncertainChatInlineEditDelivery(
	scope: ChatDraftScopeInput,
	clientMessageId: string,
	revision: string,
	storage: DraftStorage | undefined = rendererStorage(),
): DraftWriteResult {
	return clearRejectedChatInlineEditDelivery(
		scope,
		clientMessageId,
		revision,
		storage,
	);
}
