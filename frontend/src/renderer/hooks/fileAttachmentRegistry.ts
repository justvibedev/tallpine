import type { FileAttachment } from "./useFileAttachments";

// Staging registry shared by every useFileAttachments mount of one Tallpine session.

type SharedAttachmentUpdate = {
	pending: number;
	attachments?: FileAttachment[];
	error?: string | null;
};

type SharedAttachmentEntry = {
	pending: Set<symbol>;
	generation: number;
	listeners: Map<symbol, (update: SharedAttachmentUpdate) => void>;
	attachments?: FileAttachment[];
	error?: string | null;
};

export type SharedAttachmentWork = { token: symbol; generation: number };

export function sharedAttachmentDescriptors(attachments: FileAttachment[]): FileAttachment[] {
	return attachments.map(({ id, mimeType, bytes, name, stagedPath, resize }) => ({
		id,
		mimeType,
		bytes,
		name,
		...(stagedPath ? { stagedPath } : {}),
		...(resize ? { resize: { note: resize.note, originalBytes: resize.originalBytes, originalPath: resize.originalPath } } : {}),
	}));
}

// Staging belongs to the Tallpine session, not to one React mount. A controller or
// surface remount can happen while the daemon is writing bytes; keeping this
// tiny registry lets the replacement hook remain pending and receive the staged
// descriptors instead of restoring storage before the write and missing them.
export const sharedAttachmentEntries = new Map<string, SharedAttachmentEntry>();

function sharedEntry(key: string): SharedAttachmentEntry {
	let entry = sharedAttachmentEntries.get(key);
	if (!entry) {
		entry = { pending: new Set(), generation: 0, listeners: new Map() };
		sharedAttachmentEntries.set(key, entry);
	}
	return entry;
}

export function notifySharedAttachmentEntry(
	key: string,
	update: Omit<SharedAttachmentUpdate, "pending"> = {},
	originToken?: symbol,
): void {
	const entry = sharedAttachmentEntries.get(key);
	if (!entry) return;
	if (update.attachments !== undefined) entry.attachments = update.attachments;
	if (update.error !== undefined) entry.error = update.error;
	const notification = { pending: entry.pending.size, ...update };
	for (const [token, listener] of entry.listeners) {
		if (token !== originToken) listener(notification);
	}
}

export function beginSharedAttachmentWork(key: string): SharedAttachmentWork {
	const entry = sharedEntry(key);
	const work = { token: Symbol("chat-attachment-work"), generation: entry.generation };
	entry.pending.add(work.token);
	notifySharedAttachmentEntry(key);
	return work;
}

export function endSharedAttachmentWork(key: string, token: symbol): void {
	const entry = sharedAttachmentEntries.get(key);
	if (!entry) return;
	entry.pending.delete(token);
	notifySharedAttachmentEntry(key);
	if (
		entry.pending.size === 0 &&
		entry.listeners.size === 0 &&
		(entry.attachments?.length ?? 0) === 0
	) {
		sharedAttachmentEntries.delete(key);
	}
}

export function subscribeSharedAttachmentWork(
	key: string,
	token: symbol,
	listener: (update: SharedAttachmentUpdate) => void,
): () => void {
	const entry = sharedEntry(key);
	entry.listeners.set(token, listener);
	listener({
		pending: entry.pending.size,
		...(entry.attachments !== undefined ? { attachments: entry.attachments } : {}),
		...(entry.error !== undefined ? { error: entry.error } : {}),
	});
	return () => {
		entry.listeners.delete(token);
		if (
			entry.pending.size === 0 &&
			entry.listeners.size === 0 &&
			(entry.attachments?.length ?? 0) === 0
		) {
			sharedAttachmentEntries.delete(key);
		}
	};
}

export function sharedAttachmentPending(key: string | undefined): boolean {
	return Boolean(key && (sharedAttachmentEntries.get(key)?.pending.size ?? 0) > 0);
}

export function sharedAttachmentWorkIsCurrent(key: string, work: SharedAttachmentWork): boolean {
	const entry = sharedAttachmentEntries.get(key);
	return Boolean(entry && entry.generation === work.generation && entry.pending.has(work.token));
}
