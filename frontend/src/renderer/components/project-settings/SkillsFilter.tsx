import { useState } from "react";
import { useTranslation } from "react-i18next";

/** Rows whose name or description contains the query (case-insensitive); all rows for a blank query. */
export function filterSkills<T extends { name: string; description?: string }>(rows: readonly T[], query: string): T[] {
	const q = query.trim().toLowerCase();
	if (!q) return [...rows];
	return rows.filter((row) => row.name.toLowerCase().includes(q) || (row.description ?? "").toLowerCase().includes(q));
}

/**
 * The harness note most rows share (at least two, and more than half of all
 * rows), so it can be shown once above the list; a row shows its own note only
 * when it differs from this one. Without such a majority there is none.
 */
export function sharedNote(notes: readonly (string | undefined)[]): string | undefined {
	const counts = new Map<string, number>();
	for (const note of notes) if (note) counts.set(note, (counts.get(note) ?? 0) + 1);
	const [top, count] = [...counts].sort((a, b) => b[1] - a[1])[0] ?? [];
	return top && count >= 2 && count * 2 > notes.length ? top : undefined;
}

/** Query state plus the filter field for a skills list . */
export function useSkillsFilter<T extends { name: string; description?: string }>(rows: readonly T[], labelKey: "settings.instructions.filter" | "settings.instructions.filterPlugins" = "settings.instructions.filter") {
	const { t } = useTranslation();
	const [query, setQuery] = useState("");
	const shown = filterSkills(rows, query);
	const field = <div className="px-(--settings-row-inset) pt-2.5">
		<input type="search" value={query} onChange={(event) => setQuery(event.target.value)} placeholder={t(labelKey)} aria-label={t(labelKey)}
			className="h-8 w-full min-w-0 rounded-md border border-settings-border bg-transparent px-2 text-sm text-settings-label placeholder:text-settings-muted" />
	</div>;
	const empty = shown.length === 0 && query.trim() !== "" ? <p className="px-(--settings-row-inset) py-2.5 text-sm text-settings-muted">{t("settings.instructions.filterEmpty", { query: query.trim() })}</p> : null;
	return { shown, field, empty };
}
