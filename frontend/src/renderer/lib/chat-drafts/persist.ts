/** Draft storage access, load and write-through persistence. */
import { DraftStorage, ChatSessionDraft, ChatDraftScopeInput, CHAT_DRAFT_SCOPE_SCHEMA_VERSION, DraftWriteResult } from "./types";
import { normalizeScope } from "./store";
import { emptyDraft, isChatSessionDraft } from "./migrate";
import { readScopeLease, storageKey, storedDraftIncarnation, writeScopeLeaseProven } from "./scope-lease";

export function rendererStorage(): DraftStorage | undefined {
	if (typeof window === "undefined") return undefined;
	try {
		return window.localStorage;
	} catch {
		return undefined;
	}
}

export type DraftReadResult = { ok: true; draft: ChatSessionDraft } | { ok: false; draft: ChatSessionDraft };

export function loadChatSessionDraft(
	scopeInput: ChatDraftScopeInput,
	storage: DraftStorage | undefined = rendererStorage(),
): DraftReadResult {
	const scope = normalizeScope(scopeInput);
	const empty = emptyDraft(scope);
	if (!scope.sessionId || !scope.incarnation || !storage) return { ok: false, draft: empty };
	const leaseRead = readScopeLease(scope.sessionId, storage);
	if (!leaseRead.ok) return { ok: false, draft: empty };
	if (
		leaseRead.lease &&
		(leaseRead.lease.incarnation !== scope.incarnation || leaseRead.lease.state !== "active")
	) {
		return { ok: false, draft: empty };
	}
	let raw: string | null;
	try {
		raw = storage.getItem(storageKey(scope.sessionId));
	} catch {
		return { ok: false, draft: empty };
	}
	if (!raw) return { ok: true, draft: empty };
	let parsed: unknown;
	try {
		parsed = JSON.parse(raw);
	} catch {
		// Corrupt renderer data is recoverable; the next proven write replaces it.
		return { ok: true, draft: empty };
	}
	if (
		parsed &&
		typeof parsed === "object" &&
		"sessionId" in parsed &&
		(parsed as { sessionId?: unknown }).sessionId === scope.sessionId &&
		"incarnation" in parsed &&
		(parsed as { incarnation?: unknown }).incarnation !== scope.incarnation
	) return { ok: false, draft: empty };
	return {
		ok: true,
		draft: isChatSessionDraft(parsed, scope) ? parsed : empty,
	};
}

export function readChatSessionDraft(
	scope: ChatDraftScopeInput,
	storage: DraftStorage | undefined = rendererStorage(),
): ChatSessionDraft {
	return loadChatSessionDraft(scope, storage).draft;
}

export function hasContent(draft: ChatSessionDraft): boolean {
	return (
		draft.composer.text !== "" ||
		draft.composer.attachments.length > 0 ||
		(draft.composer.excerpts?.length ?? 0) > 0 ||
		Boolean(draft.composer.delivery) ||
		Boolean(draft.queuedEdit) ||
		Boolean(draft.inlineEdit) ||
		Boolean(draft.inlineEditDelivery)
	);
}

export function claimOrProveDraftScope(
	draft: ChatSessionDraft,
	storage: DraftStorage,
): boolean {
	const leaseRead = readScopeLease(draft.sessionId, storage);
	if (!leaseRead.ok) return false;
	if (leaseRead.lease) {
		return (
			leaseRead.lease.state === "active" &&
			leaseRead.lease.incarnation === draft.incarnation
		);
	}
	let raw: string | null;
	try {
		raw = storage.getItem(storageKey(draft.sessionId));
	} catch {
		return false;
	}
	const existingIncarnation = storedDraftIncarnation(raw, draft.sessionId);
	if (existingIncarnation && existingIncarnation !== draft.incarnation) return false;
	return writeScopeLeaseProven(
		{
			schemaVersion: CHAT_DRAFT_SCOPE_SCHEMA_VERSION,
			sessionId: draft.sessionId,
			incarnation: draft.incarnation,
			state: "active",
		},
		storage,
	);
}

export function persistDraft(draft: ChatSessionDraft, storage: DraftStorage | undefined): DraftWriteResult {
	if (!draft.sessionId || !storage) return { ok: false, draft };
	if (!claimOrProveDraftScope(draft, storage)) return { ok: false, draft };
	try {
		if (hasContent(draft)) storage.setItem(storageKey(draft.sessionId), JSON.stringify(draft));
		else storage.removeItem(storageKey(draft.sessionId));
		return { ok: true, draft };
	} catch {
		return { ok: false, draft };
	}
}

/**
 * Persist and immediately read back the exact record. Dispatch callers use this
 * stronger boundary so a silent/no-op storage adapter cannot be mistaken for a
 * durable draft or idempotency key.
 */
export function persistDraftProven(
	draft: ChatSessionDraft,
	storage: DraftStorage | undefined,
): DraftWriteResult {
	if (!draft.sessionId || !storage) return { ok: false, draft };
	if (!claimOrProveDraftScope(draft, storage)) return { ok: false, draft };
	const key = storageKey(draft.sessionId);
	try {
		if (hasContent(draft)) {
			const serialized = JSON.stringify(draft);
			storage.setItem(key, serialized);
			if (storage.getItem(key) !== serialized) return { ok: false, draft };
		} else {
			storage.removeItem(key);
			if (storage.getItem(key) !== null) return { ok: false, draft };
		}
		return { ok: true, draft };
	} catch {
		return { ok: false, draft };
	}
}
