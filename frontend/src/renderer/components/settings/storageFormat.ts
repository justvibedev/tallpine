import type { StorageCleanupReport, StorageEntry } from "../../hooks/useStorage";
/** Page order; a category the daemon sends that is not listed goes last. */
export const STORAGE_CATEGORIES = [
	"worktrees",
	"session_temp",
	"artifacts",
	"attachments",
	"salvage",
	"caches",
	"go_build_cache",
	"releases",
	"release_builds",
	"database",
	"logs",
	"exports",
	"backups",
	"build_cache",
	"local_ci",
] as const;

/** Whole days, hours or minutes since an ISO time; "" when unknown. */
export function ageParts(iso: string | undefined, now: number): { unit: "days" | "hours" | "minutes"; count: number } | null {
	if (!iso) return null;
	const ms = now - Date.parse(iso);
	if (!Number.isFinite(ms) || ms < 0) return { unit: "minutes", count: 0 };
	const minutes = Math.floor(ms / 60_000);
	if (minutes < 60) return { unit: "minutes", count: minutes };
	if (minutes < 60 * 24) return { unit: "hours", count: Math.floor(minutes / 60) };
	return { unit: "days", count: Math.floor(minutes / (60 * 24)) };
}

export function groupEntries(entries: StorageEntry[]): Array<{ category: string; entries: StorageEntry[]; bytes: number }> {
	const order = (category: string) => {
		const i = (STORAGE_CATEGORIES as readonly string[]).indexOf(category);
		return i === -1 ? STORAGE_CATEGORIES.length : i;
	};
	const byCategory = new Map<string, StorageEntry[]>();
	// The Go build cache has its own summary row (size, cap), so it is not listed twice.
	for (const entry of entries.filter((e) => e.category !== "go_build_cache")) byCategory.set(entry.category, [...(byCategory.get(entry.category) ?? []), entry]);
	return [...byCategory.entries()]
		.sort(([a], [b]) => order(a) - order(b))
		.map(([category, list]) => ({
			category,
			entries: [...list].sort((a, b) => b.bytes - a.bytes),
			bytes: list.reduce((sum, e) => sum + e.bytes, 0),
		}));
}

/** What a real clean-up could not remove: the count and the first reason. */
export function cleanupFailures(report: StorageCleanupReport): { count: number; reason: string } {
	let count = 0;
	let reason = report.error ?? "";
	for (const category of report.categories) {
		count += category.failed;
		if (category.error) {
			count += 1;
			reason ||= category.error;
		}
		reason ||= category.items.find((item) => item.status === "failed")?.reason ?? "";
	}
	return { count, reason };
}
