/** Per-scope draft runtime registry and mutation tracking. */
import { ChatDraftMutationSnapshot, ChatDraftRuntime, ChatDraftScopeInput, ChatDraftScope, DraftMutationKind, ChatDraftMutationToken, DraftClearResult } from "./types";

export const EMPTY_COMPOSER_MUTATION: ChatDraftMutationSnapshot<number> = { pending: false };
export const EMPTY_INLINE_EDIT_MUTATION: ChatDraftMutationSnapshot<string> = { pending: false };
export const draftRuntimes = new Map<string, ChatDraftRuntime>();
let draftMutationSequence = 0;

export function normalizeScope(scope: ChatDraftScopeInput): ChatDraftScope {
	return typeof scope === "string"
		? { sessionId: scope, incarnation: scope }
		: scope;
}

export function chatDraftScopeKey(scope: ChatDraftScopeInput): string {
	const identity = normalizeScope(scope);
	return JSON.stringify([identity.sessionId, identity.incarnation]);
}

export function chatQueuedAttachmentScopeKey(scope: ChatDraftScope, editorId: string): string {
	return JSON.stringify([scope.sessionId, scope.incarnation, `queue:${editorId}`]);
}

export function chatDraftScopeSessionId(key: string): string | undefined {
	try {
		const parsed: unknown = JSON.parse(key);
		return Array.isArray(parsed) &&
			(parsed.length === 2 || (parsed.length === 3 && typeof parsed[2] === "string" && parsed[2].startsWith("queue:"))) &&
			typeof parsed[0] === "string" &&
			typeof parsed[1] === "string"
			? parsed[0]
			: undefined;
	} catch {
		return undefined;
	}
}

export function draftRuntimeKey(scope: ChatDraftScopeInput): string {
	return chatDraftScopeKey(scope);
}

export function purgeDraftRuntimes(sessionId: string): void {
	for (const [key, runtime] of draftRuntimes) {
		if (runtime.sessionId !== sessionId) continue;
		runtime.composerToken = undefined;
		runtime.inlineEditToken = undefined;
		runtime.composer = EMPTY_COMPOSER_MUTATION;
		runtime.inlineEdit = EMPTY_INLINE_EDIT_MUTATION;
		draftRuntimes.delete(key);
	}
}

export function draftRuntime(scope: ChatDraftScopeInput): ChatDraftRuntime {
	const identity = normalizeScope(scope);
	const key = draftRuntimeKey(identity);
	let runtime = draftRuntimes.get(key);
	if (!runtime) {
		runtime = {
			sessionId: identity.sessionId,
			listeners: new Set(),
			composer: EMPTY_COMPOSER_MUTATION,
			inlineEdit: EMPTY_INLINE_EDIT_MUTATION,
		};
		draftRuntimes.set(key, runtime);
	}
	return runtime;
}

export function evictSettledDraftRuntime(key: string, runtime: ChatDraftRuntime): void {
	if (
		runtime.listeners.size > 0 ||
		runtime.composer.pending ||
		runtime.inlineEdit.pending ||
		runtime.composer.accepted ||
		runtime.inlineEdit.accepted ||
		draftRuntimes.get(key) !== runtime
	) return;
	draftRuntimes.delete(key);
}

export function clearDraftMutationReceipt(
	runtime: ChatDraftRuntime,
	kind: DraftMutationKind,
	pending?: boolean,
): void {
	if (kind === "composer") {
		runtime.composer = { pending: pending ?? runtime.composer.pending };
		return;
	}
	runtime.inlineEdit = { pending: pending ?? runtime.inlineEdit.pending };
}

export function emitDraftRuntime(runtime: ChatDraftRuntime): void {
	for (const listener of runtime.listeners) listener();
}

export function emitComposerDraftChange(scope: ChatDraftScopeInput): void {
	const runtime = draftRuntime(scope);
	// useSyncExternalStore compares snapshots by identity. Excerpt changes can be
	// initiated from the transcript rather than the composer, so publish a fresh
	// snapshot even when no delivery mutation is pending.
	runtime.composer = { ...runtime.composer };
	emitDraftRuntime(runtime);
}

export function subscribeChatDraftRuntime(scope: ChatDraftScopeInput, listener: () => void): () => void {
	if (!normalizeScope(scope).sessionId) return () => undefined;
	const key = draftRuntimeKey(scope);
	const runtime = draftRuntime(scope);
	runtime.listeners.add(listener);
	return () => {
		runtime.listeners.delete(listener);
		evictSettledDraftRuntime(key, runtime);
	};
}

export function getChatComposerMutation(
	scope: ChatDraftScopeInput,
): ChatDraftMutationSnapshot<number> {
	const runtime = draftRuntimes.get(draftRuntimeKey(scope));
	if (!runtime) return EMPTY_COMPOSER_MUTATION;
	return runtime.composer;
}

/** Async preparation may outlive authoritative replacement of this session. */
export function isChatComposerMutationCurrent(
	scope: ChatDraftScopeInput,
	token: ChatDraftMutationToken,
): boolean {
	return draftRuntimes.get(draftRuntimeKey(scope))?.composerToken === token;
}

export function getChatInlineEditMutation(
	scope: ChatDraftScopeInput,
): ChatDraftMutationSnapshot<string> {
	const runtime = draftRuntimes.get(draftRuntimeKey(scope));
	if (!runtime) return EMPTY_INLINE_EDIT_MUTATION;
	return runtime.inlineEdit;
}

export function beginDraftMutation(
	scope: ChatDraftScopeInput,
	kind: DraftMutationKind,
): ChatDraftMutationToken | undefined {
	if (!normalizeScope(scope).sessionId) return undefined;
	const runtime = draftRuntime(scope);
	if (kind === "composer") {
		if (runtime.composer.pending || runtime.composer.accepted) return undefined;
		const token = Symbol("chat-composer-mutation");
		runtime.composerToken = token;
		runtime.composer = { pending: true };
		emitDraftRuntime(runtime);
		return token;
	}
	if (runtime.inlineEdit.pending || runtime.inlineEdit.accepted) return undefined;
	const token = Symbol("chat-inline-edit-mutation");
	runtime.inlineEditToken = token;
	runtime.inlineEdit = { pending: true };
	emitDraftRuntime(runtime);
	return token;
}

export function beginChatComposerMutation(scope: ChatDraftScopeInput): ChatDraftMutationToken | undefined {
	return beginDraftMutation(scope, "composer");
}

export function beginChatInlineEditMutation(scope: ChatDraftScopeInput): ChatDraftMutationToken | undefined {
	return beginDraftMutation(scope, "inline-edit");
}

export function cancelDraftMutation(
	scope: ChatDraftScopeInput,
	kind: DraftMutationKind,
	token: ChatDraftMutationToken,
): void {
	const key = draftRuntimeKey(scope);
	const runtime = draftRuntimes.get(key);
	if (!runtime) return;
	if (kind === "composer") {
		if (runtime.composerToken !== token) return;
		runtime.composerToken = undefined;
		clearDraftMutationReceipt(runtime, "composer", false);
	} else {
		if (runtime.inlineEditToken !== token) return;
		runtime.inlineEditToken = undefined;
		clearDraftMutationReceipt(runtime, "inline-edit", false);
	}
	emitDraftRuntime(runtime);
	evictSettledDraftRuntime(key, runtime);
}

export function cancelChatComposerMutation(
	scope: ChatDraftScopeInput,
	token: ChatDraftMutationToken,
): void {
	cancelDraftMutation(scope, "composer", token);
}

export function cancelChatInlineEditMutation(
	scope: ChatDraftScopeInput,
	token: ChatDraftMutationToken,
): void {
	cancelDraftMutation(scope, "inline-edit", token);
}

export function finishChatComposerMutation(
	scope: ChatDraftScopeInput,
	token: ChatDraftMutationToken,
	revision: number,
	result: DraftClearResult,
): void {
	const key = draftRuntimeKey(scope);
	const runtime = draftRuntimes.get(key);
	if (!runtime || runtime.composerToken !== token) return;
	runtime.composerToken = undefined;
	const sequence = ++draftMutationSequence;
	runtime.composer = {
		pending: false,
		accepted: { sequence, revision, result },
	};
	emitDraftRuntime(runtime);
	evictSettledDraftRuntime(key, runtime);
}

export function finishChatInlineEditMutation(
	scope: ChatDraftScopeInput,
	token: ChatDraftMutationToken,
	revision: string,
	result: DraftClearResult,
): void {
	const key = draftRuntimeKey(scope);
	const runtime = draftRuntimes.get(key);
	if (!runtime || runtime.inlineEditToken !== token) return;
	runtime.inlineEditToken = undefined;
	const sequence = ++draftMutationSequence;
	runtime.inlineEdit = {
		pending: false,
		accepted: { sequence, revision, result },
	};
	emitDraftRuntime(runtime);
	evictSettledDraftRuntime(key, runtime);
}

export function acknowledgeDraftMutation(
	scope: ChatDraftScopeInput,
	kind: DraftMutationKind,
	sequence: number,
): void {
	const key = draftRuntimeKey(scope);
	const runtime = draftRuntimes.get(key);
	if (!runtime) return;
	if (kind === "composer") {
		if (runtime.composer.accepted?.sequence !== sequence) return;
		clearDraftMutationReceipt(runtime, "composer");
	} else {
		if (runtime.inlineEdit.accepted?.sequence !== sequence) return;
		clearDraftMutationReceipt(runtime, "inline-edit");
	}
	emitDraftRuntime(runtime);
	evictSettledDraftRuntime(key, runtime);
}

/**
 * Release a settled composer receipt only after a committed subscriber applied
 * it. No task duration or global pressure decides this lifetime. Each exact
 * session incarnation owns at most one composer and one inline-edit receipt;
 * acknowledgement, a superseding durable edit, or authoritative incarnation
 * cleanup retires them.
 */
export function acknowledgeChatComposerMutation(
	scope: ChatDraftScopeInput,
	sequence: number,
): void {
	acknowledgeDraftMutation(scope, "composer", sequence);
}

/** See acknowledgeChatComposerMutation. */
export function acknowledgeChatInlineEditMutation(
	scope: ChatDraftScopeInput,
	sequence: number,
): void {
	acknowledgeDraftMutation(scope, "inline-edit", sequence);
}

export function invalidateAcceptedDraftMutation(scope: ChatDraftScopeInput, kind: DraftMutationKind): void {
	const key = draftRuntimeKey(scope);
	const runtime = draftRuntimes.get(key);
	if (!runtime) return;
	if (kind === "composer") {
		if (!runtime.composer.accepted) return;
		clearDraftMutationReceipt(runtime, "composer");
	} else {
		if (!runtime.inlineEdit.accepted) return;
		clearDraftMutationReceipt(runtime, "inline-edit");
	}
	emitDraftRuntime(runtime);
	evictSettledDraftRuntime(key, runtime);
}
