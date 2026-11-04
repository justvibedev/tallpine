import { formatBytes as formatSharedBytes } from "@tallpine/shared/format/bytes";

export const formatBytes = (bytes: number): string => formatSharedBytes(bytes, { wholeKb: true });
export { formatDurationStatus as formatDuration } from "@tallpine/shared/format/duration";


const timeFormatter = new Intl.DateTimeFormat(undefined, {
	hour: "2-digit",
	minute: "2-digit",
	hourCycle: "h23",
});

const dateFormatter = new Intl.DateTimeFormat(undefined, {
	month: "short",
	day: "numeric",
	year: "numeric",
});

export function formatTokens(tokens: number): string {
	if (tokens < 1000) return `${tokens}`;
	return `${(tokens / 1000).toFixed(1)}k`;
}

/** Collapse the home directory so a long absolute path does not eat the row. */
export function shortenPaths(text: string): string {
	return text.replace(/\/(?:Users|home)\/[^/\s]+/g, "~");
}

export function formatDecisionDuration(ms: number): string {
	if (ms < 1_000) return `${Math.max(0, Math.round(ms))}ms`;
	return `${(ms / 1_000).toFixed(1)}s`;
}

export function formatTime(iso: string): string {
	const parsed = new Date(iso);
	return Number.isNaN(parsed.getTime()) ? "" : timeFormatter.format(parsed);
}

export function formatMessageTimestamp(iso: string, now = new Date()): string {
	const parsed = new Date(iso);
	if (Number.isNaN(parsed.getTime())) return "";

	const messageDay = new Date(parsed.getFullYear(), parsed.getMonth(), parsed.getDate()).getTime();
	const today = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
	const daysAgo = Math.round((today - messageDay) / 86_400_000);
	if (daysAgo === 0) return timeFormatter.format(parsed);
	if (daysAgo === 1) return `Yesterday · ${timeFormatter.format(parsed)}`;
	return dateFormatter.format(parsed);
}

/* -------------------------------------------------------------------------- */
/* messages                                                                    */
/* -------------------------------------------------------------------------- */

export function formatJson(value: unknown): string {
	if (typeof value === "string") return value;
	try {
		return JSON.stringify(value, null, 2) ?? String(value);
	} catch {
		return String(value);
	}
}

/** The most recent line of a progress stream, for the collapsed row. */
export function lastLine(text: string): string {
	const lines = text.trimEnd().split("\n");
	return lines[lines.length - 1] ?? "";
}

/* -------------------------------------------------------------------------- */
/* auto review                                                                 */
/* -------------------------------------------------------------------------- */

/**
 * Where the provider stopped accepting work until someone signs in.
 *
 * The banner above the timeline is what the user acts on; this row is the record of
 * when it happened, so a turn that failed for this reason is not left looking like an
 * ordinary failure.
 */
/** Whole seconds for a short duration, minutes and seconds past a minute. */
export function setupDuration(durationMs: number): { minutes: number; seconds: number } {
	const total = Math.max(0, Math.round(durationMs / 1000));
	return { minutes: Math.floor(total / 60), seconds: total % 60 };
}
