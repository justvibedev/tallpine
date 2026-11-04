import type { BacklogItem } from "./backlog-api";

/** Open items untouched this long drop into the muted stale row. */
export const BACKLOG_STALE_DAYS = 60;
const DAY_MS = 24 * 60 * 60 * 1000;

export type BacklogProgress = { done: number; total: number };

export type BacklogGroups = {
	inbox: BacklogItem[];
	open: BacklogItem[];
	held: BacklogItem[];
	stale: BacklogItem[];
	archive: BacklogItem[];
	/** Children by parent id, in drag order. Only parents that appear in a group above list their children. */
	childrenByParent: Map<string, BacklogItem[]>;
	progress: Map<string, BacklogProgress>;
};

const byPosition = (a: BacklogItem, b: BacklogItem) => a.position - b.position || a.createdAt.localeCompare(b.createdAt);
export const isClosed = (item: BacklogItem) => item.state === "done" || item.state === "dropped";

/**
 * Splits a project's items into the page's groups. Children nest one level under
 * a live (inbox or open) parent; a child whose parent is closed or missing is
 * listed on its own so it never disappears. Inbox and Open keep drag order, the
 * stale row lists the longest untouched first, and the archive lists the most
 * recently closed first.
 */
export function groupBacklog(items: readonly BacklogItem[], now: number = Date.now()): BacklogGroups {
	const live = new Set(items.filter((item) => !isClosed(item)).map((item) => item.id));
	const childrenByParent = new Map<string, BacklogItem[]>();
	const progress = new Map<string, BacklogProgress>();
	const topLevel: BacklogItem[] = [];
	for (const item of items) {
		if (item.parentId && live.has(item.parentId)) {
			const siblings = childrenByParent.get(item.parentId) ?? [];
			siblings.push(item);
			childrenByParent.set(item.parentId, siblings);
		} else {
			topLevel.push(item);
		}
	}
	for (const siblings of childrenByParent.values()) siblings.sort(byPosition);
	// Progress comes from the server's counts, not from the closed children a list page
	// happened to fetch: closed items only accumulate and the lists are capped.
	for (const item of items) {
		if (item.childCount > 0) progress.set(item.id, { done: item.childClosedCount, total: item.childCount });
	}

	const staleBefore = now - BACKLOG_STALE_DAYS * DAY_MS;
	const groups: BacklogGroups = { inbox: [], open: [], held: [], stale: [], archive: [], childrenByParent, progress };
	for (const item of [...topLevel].sort(byPosition)) {
		if (isClosed(item)) groups.archive.push(item);
		else if (item.state === "inbox") groups.inbox.push(item);
		else if (item.hold !== "") groups.held.push(item);
		else if (Date.parse(item.updatedAt) < staleBefore && !item.badges.active && !item.badges.inReview) groups.stale.push(item);
		else groups.open.push(item);
	}
	groups.stale.sort((a, b) => Date.parse(a.updatedAt) - Date.parse(b.updatedAt));
	groups.archive.sort((a, b) => Date.parse(b.closedAt ?? b.updatedAt) - Date.parse(a.closedAt ?? a.updatedAt));
	return groups;
}
