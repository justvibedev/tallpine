import { formatBytes } from "@tallpine/shared/format/bytes";
import { Feather } from "../../icons";
import type { Theme } from "../../theme";
import type { ConversationActivity, ConversationItem, FileChange } from "../types";
import { PRODUCT_NAME } from "../../brand";

export type TimelineRow =
	| { kind: "single"; key: string; items: [ConversationItem] }
	| { kind: "activities"; key: string; items: ConversationActivity[] };

export function deliveryCopy(state?: string): string | undefined {
	switch (state) {
		case "queued": return "Queued — sends when the agent finishes";
		case "sending": return "Sending…";
		case "uncertain": return "Delivery unconfirmed — check the conversation before retrying";
		case "failed": return "Not sent";
		default: return undefined;
	}
}

/** Collapse consecutive mechanics so agent prose remains the visual hierarchy. */
export function activityRuns(items: ConversationItem[]): TimelineRow[] {
	const rows: TimelineRow[] = [];
	for (const item of items) {
		const runnable = item.kind === "activity" &&
			item.activityKind !== "approval" &&
			item.activityKind !== "user_input" &&
			item.activityKind !== "error" &&
			item.activityKind !== "file_change" &&
			item.activityKind !== "reasoning" &&
			item.detail?.event === undefined;
		const previous = rows[rows.length - 1];
		if (runnable && previous?.kind === "activities" && previous.items[0]?.turnId === item.turnId) {
			previous.items.push(item);
		} else if (runnable) {
			rows.push({ kind: "activities", key: `run-${item.sequence}`, items: [item] });
		} else {
			rows.push({ kind: "single", key: `${item.kind}:${item.id}`, items: [item] });
		}
	}
	return rows;
}

export function printable(value: unknown): string {
	if (value === null || value === undefined) return "";
	if (typeof value === "string") return value;
	try { return JSON.stringify(value, null, 2); } catch { return String(value); }
}

export function fileChanges(value: unknown): FileChange[] {
	if (!Array.isArray(value)) return [];
	return value.flatMap((entry): FileChange[] => {
		if (!entry || typeof entry !== "object") return [];
		const file = entry as Record<string, unknown>;
		if (typeof file.path !== "string") return [];
		const status = ["added", "modified", "deleted", "renamed"].includes(String(file.status)) ? String(file.status) as FileChange["status"] : "modified";
		return [{ path: file.path, oldPath: typeof file.oldPath === "string" ? file.oldPath : undefined, status, additions: typeof file.additions === "number" ? file.additions : 0, deletions: typeof file.deletions === "number" ? file.deletions : 0, patch: typeof file.patch === "string" ? file.patch : undefined, patchTruncated: file.patchTruncated === true }];
	});
}

export function reviewPaths(value: unknown): string[] {
	if (!Array.isArray(value)) return [];
	return value.flatMap((entry) => typeof entry === "string" ? [entry] : entry && typeof entry === "object" && typeof (entry as { path?: unknown }).path === "string" ? [(entry as { path: string }).path] : []);
}

export function truncationNote(value: unknown): string | undefined {
	if (!value || typeof value !== "object" || Array.isArray(value)) return undefined;
	const record = value as { truncated?: unknown; bytes?: unknown };
	if (record.truncated !== true) return undefined;
	const bytes = typeof record.bytes === "number" ? ` (${formatBytes(record.bytes, { wholeKb: true })})` : "";
	return `This payload${bytes} was larger than ${PRODUCT_NAME} stores, so it was not kept.`;
}

export function lastLine(value: string): string { return value.trimEnd().split("\n").at(-1) ?? ""; }

export function activityMeta(activity: ConversationActivity): { icon: keyof typeof Feather.glyphMap; prefix?: string; color(t: Theme): string } {
	switch (activity.activityKind) {
		case "command": return { icon: "terminal", color: (t) => activity.status === "failed" ? t.red : t.textTertiary };
		case "file_change": return { icon: "edit-3", prefix: "Changed", color: (t) => t.accent };
		case "mcp_tool": return { icon: "tool", prefix: activity.detail?.server ? `${activity.detail.server} ·` : "MCP ·", color: (t) => t.textSecondary };
		case "auto_review": return { icon: "shield", prefix: "Reviewed", color: (t) => t.green };
		default: return { icon: "activity", color: (t) => t.textTertiary };
	}
}

export function elapsed(start?: string, end?: string): string | undefined { if (!start || !end) return undefined; const ms = Date.parse(end) - Date.parse(start); if (!Number.isFinite(ms) || ms < 0) return undefined; const seconds = Math.round(ms / 1000); return seconds < 60 ? `${seconds}s` : `${Math.floor(seconds / 60)}m ${seconds % 60}s`; }
export function formatTokens(value: number): string { return value >= 1_000 ? `${(value / 1_000).toFixed(value >= 10_000 ? 0 : 1)}k` : String(value); }
