/** Cross-tab scope lease and draft scope activation. */
import { ChatDraftScopeLease, CHAT_DRAFT_SCOPE_SCHEMA_VERSION, DraftStorage, ChatDraftScope, ChatDraftScopeActivationResult, ChatDraftScopeInput } from "./types";
import { purgeDraftRuntimes, normalizeScope } from "./store";
import { rendererStorage } from "./persist";

export function storageKey(sessionId: string): string {
	// The key remains stable across schema versions so a future decoder can find
	// and migrate the prior record rather than orphaning it under a v1-only key.
	return `ao.chat.draft:${encodeURIComponent(sessionId)}`;
}

export function scopeLeaseStorageKey(sessionId: string): string {
	return `ao.chat.draft.active:${encodeURIComponent(sessionId)}`;
}

export function isScopeLease(value: unknown, sessionId: string): value is ChatDraftScopeLease {
	if (!value || typeof value !== "object") return false;
	const lease = value as Partial<ChatDraftScopeLease>;
	return (
		lease.schemaVersion === CHAT_DRAFT_SCOPE_SCHEMA_VERSION &&
		lease.sessionId === sessionId &&
		typeof lease.incarnation === "string" &&
		lease.incarnation.length > 0 &&
		(lease.state === "active" || lease.state === "activating") &&
		(lease.previousIncarnation === undefined ||
			typeof lease.previousIncarnation === "string")
	);
}

export type ScopeLeaseReadResult =
	| { ok: true; lease?: ChatDraftScopeLease }
	| { ok: false };

export function readScopeLease(
	sessionId: string,
	storage: DraftStorage | undefined,
): ScopeLeaseReadResult {
	if (!sessionId || !storage) return { ok: false };
	let raw: string | null;
	try {
		raw = storage.getItem(scopeLeaseStorageKey(sessionId));
	} catch {
		return { ok: false };
	}
	if (raw === null) return { ok: true };
	try {
		const parsed: unknown = JSON.parse(raw);
		return isScopeLease(parsed, sessionId) ? { ok: true, lease: parsed } : { ok: false };
	} catch {
		return { ok: false };
	}
}

export function storedDraftIncarnation(raw: string | null, sessionId: string): string | undefined {
	if (raw === null) return undefined;
	try {
		const parsed = JSON.parse(raw) as {
			sessionId?: unknown;
			incarnation?: unknown;
		};
		return parsed?.sessionId === sessionId && typeof parsed.incarnation === "string"
			? parsed.incarnation
			: undefined;
	} catch {
		return undefined;
	}
}

export function isStrictlyNewerIncarnation(
	sessionId: string,
	candidate: string,
	current: string,
): boolean {
	if (candidate === current) return false;
	// The production incarnation is the daemon's createdAt. A legacy renderer used
	// the logical session id itself; an authoritative createdAt may replace that
	// sentinel, but the sentinel may never replace an already-dated incarnation.
	if (current === sessionId) return candidate !== sessionId;
	if (candidate === sessionId) return false;
	const candidateTime = Date.parse(candidate);
	const currentTime = Date.parse(current);
	return Number.isFinite(candidateTime) &&
		Number.isFinite(currentTime) &&
		candidateTime > currentTime;
}

export function writeScopeLeaseProven(
	lease: ChatDraftScopeLease,
	storage: DraftStorage,
): boolean {
	const serialized = JSON.stringify(lease);
	try {
		storage.setItem(scopeLeaseStorageKey(lease.sessionId), serialized);
		return storage.getItem(scopeLeaseStorageKey(lease.sessionId)) === serialized;
	} catch {
		return false;
	}
}

export function finishScopeActivation(
	scope: ChatDraftScope,
	lease: ChatDraftScopeLease,
	storage: DraftStorage,
): ChatDraftScopeActivationResult {
	const key = storageKey(scope.sessionId);
	try {
		storage.removeItem(key);
		if (storage.getItem(key) !== null) return { ok: false, reason: "storage" };
	} catch {
		return { ok: false, reason: "storage" };
	}
	purgeDraftRuntimes(scope.sessionId);
	const active: ChatDraftScopeLease = {
		schemaVersion: CHAT_DRAFT_SCOPE_SCHEMA_VERSION,
		sessionId: scope.sessionId,
		incarnation: scope.incarnation,
		state: "active",
	};
	if (!writeScopeLeaseProven(active, storage)) return { ok: false, reason: "storage" };
	return {
		ok: true,
		replaced: lease.previousIncarnation !== undefined,
		...(lease.previousIncarnation !== undefined
			? { previousIncarnation: lease.previousIncarnation }
			: {}),
	};
}

/**
 * Establish the one daemon-authoritative incarnation allowed to own a logical
 * session's renderer draft. Replacement is a monotonic, recoverable transition:
 * the successor blocks every predecessor first, then proves the old record is
 * gone, and only then becomes writable.
 */
export function activateChatDraftScope(
	scopeInput: ChatDraftScopeInput,
	storage: DraftStorage | undefined = rendererStorage(),
): ChatDraftScopeActivationResult {
	const scope = normalizeScope(scopeInput);
	if (!scope.sessionId || !scope.incarnation || !storage) {
		return { ok: false, reason: "storage" };
	}
	const leaseRead = readScopeLease(scope.sessionId, storage);
	if (!leaseRead.ok) return { ok: false, reason: "storage" };
	const lease = leaseRead.lease;
	if (lease?.incarnation === scope.incarnation) {
		if (lease.state === "active") return { ok: true, replaced: false };
		return finishScopeActivation(scope, lease, storage);
	}

	let previousIncarnation = lease?.incarnation;
	if (!previousIncarnation) {
		let raw: string | null;
		try {
			raw = storage.getItem(storageKey(scope.sessionId));
		} catch {
			return { ok: false, reason: "storage" };
		}
		previousIncarnation = storedDraftIncarnation(raw, scope.sessionId);
		if (!previousIncarnation || previousIncarnation === scope.incarnation) {
			const active: ChatDraftScopeLease = {
				schemaVersion: CHAT_DRAFT_SCOPE_SCHEMA_VERSION,
				sessionId: scope.sessionId,
				incarnation: scope.incarnation,
				state: "active",
			};
			return writeScopeLeaseProven(active, storage)
				? { ok: true, replaced: false }
				: { ok: false, reason: "storage" };
		}
	}
	if (!isStrictlyNewerIncarnation(scope.sessionId, scope.incarnation, previousIncarnation)) {
		return { ok: false, reason: "obsolete" };
	}
	const activating: ChatDraftScopeLease = {
		schemaVersion: CHAT_DRAFT_SCOPE_SCHEMA_VERSION,
		sessionId: scope.sessionId,
		incarnation: scope.incarnation,
		state: "activating",
		previousIncarnation,
	};
	if (!writeScopeLeaseProven(activating, storage)) {
		return { ok: false, reason: "storage" };
	}
	return finishScopeActivation(scope, activating, storage);
}
