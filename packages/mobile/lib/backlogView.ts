import type { BacklogItem } from "./api";

/**
 * The delegate request for a backlog item. The daemon renders the item's title,
 * note, done-when and agent notes from `backlogRef`, links the worker to the
 * item and enforces hold and startability, exactly as `tallpine spawn --backlog`.
 */
export function backlogDelegateRequest(projectId: string, item: Pick<BacklogItem, "ref" | "title">) {
	// The short brief names the worker on the board; the daemon appends the full item.
	return { projectId, brief: `${item.ref}: ${item.title}`, mode: "chat" as const, backlogRef: item.ref };
}

/** Held items wait on a person and parents are not startable, so the phone does not offer to delegate them. */
export function backlogCanDelegate(item: Pick<BacklogItem, "hold" | "state" | "childCount">): boolean {
	return item.state === "open" && !item.hold.trim() && item.childCount === 0;
}

/** One line of progress for a parent item, absent when it has no children. */
export function backlogProgress(item: Pick<BacklogItem, "childCount" | "childClosedCount">): string | undefined {
	return item.childCount > 0 ? `${item.childClosedCount}/${item.childCount} done` : undefined;
}
