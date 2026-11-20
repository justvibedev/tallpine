import { drawnReplies, groupHasHumanPrompt, type TimelineGroup } from "./timeline-groups";

/** localStorage lives under ~/.tallpine/electron, where main.ts pins userData. */
export const lastSeenStorageKey = "tallpine.chat.lastSeenReply";
const MAX_CHATS = 200;

type LastSeenMap = Record<string, number>;

function read(): LastSeenMap {
	try {
		const parsed: unknown = JSON.parse(window.localStorage.getItem(lastSeenStorageKey) ?? "{}");
		return parsed && typeof parsed === "object" ? (parsed as LastSeenMap) : {};
	} catch {
		return {};
	}
}

/** The newest owner-facing reply this chat's reader has seen, by conversation sequence. */
export const chatLastSeen = {
	get: (chatKey: string): number | undefined => {
		const value = read()[chatKey];
		return typeof value === "number" ? value : undefined;
	},
	set: (chatKey: string, sequence: number) => {
		try {
			const map = read();
			delete map[chatKey];
			map[chatKey] = sequence;
			// Insertion order is recency order, so the oldest chats fall off first.
			const keys = Object.keys(map);
			for (const stale of keys.slice(0, Math.max(0, keys.length - MAX_CHATS))) delete map[stale];
			window.localStorage.setItem(lastSeenStorageKey, JSON.stringify(map));
		} catch {
			// Unpersisted: the next visit just shows no unread marker.
		}
	},
};

export type OwnerReply = { sequence: number; index: number };

/**
 * Assistant replies drawn in the open in the owner's own turns, with the index of
 * the group drawing each. Replies folded under a settled turn's "Worked for" row are
 * left out: the "N new replies" pill must only count what the reader can see.
 */
export function ownerReplies(groups: readonly TimelineGroup[], workerUpdates?: boolean): OwnerReply[] {
	const replies: OwnerReply[] = [];
	groups.forEach((group, index) => {
		if (!groupHasHumanPrompt(group)) return;
		for (const item of drawnReplies(group, workerUpdates)) replies.push({ sequence: item.sequence, index });
	});
	return replies;
}
