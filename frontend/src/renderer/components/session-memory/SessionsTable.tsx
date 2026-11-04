/** Sortable table of per-session memory and CPU rows. */
import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { ChevronDown, ChevronUp } from "lucide-react";
import { chipTone, largestSession, stableResourceOrder } from "@tallpine/product-ui";
import { cn } from "@/lib/utils";
import { useWorkspaceQuery } from "../../hooks/useWorkspaceQuery";
import { useAppMemory, useSessionMemory, type SessionMemoryReading } from "../../hooks/useSessionMemory";
import { useSessionUsageSummaries } from "../../hooks/useSessionUsageSummaries";
import { type WorkspaceSession } from "../../types/workspace";
import { SettingsSection } from "../settings/SettingsSection";
import { ReportRow, sessionTitles, reviewerLabel } from "./session-memory-reports";
import { EMPTY_WORKSPACES, usePressureFacts, toSessionFacts } from "./session-memory-facts";
import { GroupRow, SessionRow } from "./SessionMemoryRows";
import { FixedRow } from "./FixedRows";

/**
 * Each column's width, padding and alignment, in one place: the header and
 * every row take their classes from here, so the columns line up.
 */
export const columnWidth = { type: "w-24", pid: "w-28", memory: "w-28", cpu: "w-16" } as const;
export const cell = {
	name: "pl-(--settings-row-inset) pr-3 text-left",
	process: "pl-12 pr-3 text-left",
	type: "px-3 text-left",
	pid: "px-3 text-right",
	memory: "px-3 text-right",
	cpu: "pl-3 pr-(--settings-row-inset) text-right",
} as const;

/** Every full-width row (group label, spacer, step heading) spans all five. */
export const columnCount = 5;

/**
 * Sessions by CPU alone: memory plays no part. Sessions with exactly the same
 * CPU (most often every one idle at 0) fall back to their names, A to Z, in
 * either direction, so a tie never reads as some other order turned around.
 */
export function cpuOrder<T extends { id: string; session: WorkspaceSession; reading: SessionMemoryReading }>(rows: T[], ascending: boolean): T[] {
	const direction = ascending ? 1 : -1;
	return [...rows].sort(
		(a, b) =>
			direction * (a.reading.cpuPercent - b.reading.cpuPercent) ||
			a.session.title.localeCompare(b.session.title) ||
			a.id.localeCompare(b.id),
	);
}

/** A column header that orders the sessions by its own figure; a second click flips the direction. */
export function SortHeader({ active, ascending, className, label, onSort }: { active: boolean; ascending: boolean; className: string; label: string; onSort: () => void }) {
	// Always visible, so the header reads as sortable before any click: faint
	// on an idle column, full strength showing the direction on the active one.
	const Arrow = active && ascending ? ChevronUp : ChevronDown;
	return (
		<th aria-sort={active ? (ascending ? "ascending" : "descending") : undefined} className={cn("bg-popover pb-2 pt-3 font-medium", className)} scope="col">
			<button
				className={cn(
					"inline-flex items-center gap-1 rounded-sm transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring",
					active && "text-settings-label",
				)}
				data-testid="session-memory-sort"
				onClick={onSort}
				type="button"
			>
				{label}
				<Arrow aria-hidden="true" className={cn("size-icon-2xs", active ? "opacity-100" : "opacity-50")} />
			</button>
		</th>
	);
}

/** Every live session and Tallpine's own processes, largest first. */
export function SessionsTable({ onRows, projectId, underToolbar }: { onRows?: (rows: ReportRow[]) => void; projectId?: string; underToolbar?: boolean }) {
	const { t } = useTranslation();
	const workspaces = useWorkspaceQuery().data ?? EMPTY_WORKSPACES;
	const readings = useSessionMemory(projectId).data;
	const app = useAppMemory().data?.app;
	// Cost belongs in a shared report even though the window never shows it.
	const usage = useSessionUsageSummaries(projectId).data;
	const { state, facts } = usePressureFacts(projectId);
	// Orchestrators are listed too: they hold memory like any session, and
	// leaving them out made the rows add up to less than Tallpine's total.
	const sessions = useMemo(
		() =>
			workspaces
				.filter((workspace) => !projectId || workspace.id === projectId)
				.flatMap((workspace) => workspace.sessions)
				.filter((session) => session.isTerminated !== true),
		[workspaces, projectId],
	);
	// Rows with a reading are the live ones; a session without a process tree
	// is not listed, never shown as 0 MB.
	const orderRef = useRef<string[]>([]);
	// Sessions only: Tallpine's own row stays pinned below them whatever the order.
	const [sort, setSort] = useState<{ by: "memory" | "cpu"; ascending: boolean }>({ by: "memory", ascending: false });
	// Clicking the active column flips its direction; another column starts largest first.
	const sortBy = (by: "memory" | "cpu") => setSort((current) => ({ by, ascending: current.by === by ? !current.ascending : false }));
	const live = useMemo(() => {
		const rows = sessions
			.map((session) => ({ id: session.id, session, reading: readings?.get(session.id) }))
			.filter((row): row is { id: string; session: WorkspaceSession; reading: SessionMemoryReading } => row.reading !== undefined)
			.map((row) => ({ ...row, rssBytes: row.reading.rssBytes }));
		if (sort.by === "cpu") return cpuOrder(rows, sort.ascending);
		const largestFirst = stableResourceOrder(orderRef.current, rows);
		orderRef.current = largestFirst.map((row) => row.id);
		return sort.ascending ? [...largestFirst].reverse() : largestFirst;
	}, [sessions, readings, sort]);
	const largest = largestSession(facts);
	// Tallpine's total counts reviewer panes too; without their rows the window
	// could never add up to it.
	const reviewers = useMemo(() => [...(app?.reviewers ?? [])].sort((a, b) => b.memory.rssBytes - a.memory.rssBytes), [app?.reviewers]);
	const titles = useMemo(() => sessionTitles(workspaces), [workspaces]);
	const maxBytes = Math.max(app?.own?.rssBytes ?? 0, ...live.map((row) => row.rssBytes), ...reviewers.map((r) => r.memory.rssBytes), 1);
	// The copy button sits above the table, so the rows it would copy travel
	// up. Same order as the screen, so the text matches what was seen.
	useEffect(() => {
		onRows?.(live.map(({ session, reading }) => ({ session, reading, usage: usage?.get(session.id) })));
	}, [live, usage, onRows]);
	// Any number of rows open at once: comparing two trees is the point.
	const [expanded, setExpanded] = useState<ReadonlySet<string>>(() => new Set());
	const toggleExpanded = (id: string) =>
		setExpanded((current) => {
			const next = new Set(current);
			if (!next.delete(id)) next.add(id);
			return next;
		});
	if (live.length === 0 && reviewers.length === 0 && !app?.own) {
		return (
			<SettingsSection title={t("shell.memoryGroupSessions")} grouped>
				<p className="settings-row-bar text-sm text-settings-muted">{t("shell.memoryEmpty")}</p>
			</SettingsSection>
		);
	}
	return (
		<SettingsSection title={t("shell.memoryGroupSessions")}>
		{/* overflow-clip, not the card's overflow-hidden: hidden makes the card a
		    scroll container, which would stop the header sticking to the page. */}
		<div className="settings-grouped-rows overflow-clip!">
		<table className="w-full table-fixed border-collapse text-xs" data-testid="session-memory-table">
			<colgroup>
				<col />
				<col className={columnWidth.type} />
				<col className={columnWidth.pid} />
				<col className={columnWidth.memory} />
				<col className={columnWidth.cpu} />
			</colgroup>
			{/* The columns stay readable however far the list scrolls. */}
			{/* Below the pinned summary (h-8) and, on the settings page, the window's
			    transparent toolbar strip. */}
			<thead className={cn("sticky z-10 bg-popover", underToolbar ? "top-[calc(var(--spacing-toolbar)+--spacing(8))]" : "top-8")}>
				<tr className="border-b border-(--color-border-settings-dialog-header) text-xs text-settings-muted">
					<th className={cn("bg-popover pb-2 pt-3 font-medium", cell.name)} scope="col">{t("shell.memoryColumnName")}</th>
					<th className={cn("bg-popover pb-2 pt-3 font-medium", cell.type)} scope="col">{t("shell.memoryColumnType")}</th>
					<th className={cn("bg-popover pb-2 pt-3 font-medium", cell.pid)} scope="col">{t("shell.memoryColumnPid")}</th>
					<SortHeader active={sort.by === "memory"} ascending={sort.ascending} className={cell.memory} label={t("shell.memoryColumnRss")} onSort={() => sortBy("memory")} />
					<SortHeader active={sort.by === "cpu"} ascending={sort.ascending} className={cell.cpu} label={t("shell.memoryColumnCpu")} onSort={() => sortBy("cpu")} />
				</tr>
			</thead>
			<tbody className="[&_tr:not(.memory-group)+tr.memory-row]:border-t [&_tr.memory-row]:border-(--color-border-settings-dialog-header)">
				{live.length > 0 ? <GroupRow label={t("shell.memoryGroupSessions")} /> : null}
				{live.map((row) => (
					<SessionRow
						chip={chipTone(state ?? "fine", facts.find((f) => f.id === row.id) ?? toSessionFacts(row.session, row.reading, Date.now()), largest)}
						isExpanded={expanded.has(row.id)}
						key={row.id}
						maxBytes={maxBytes}
						onToggle={() => toggleExpanded(row.id)}
						reading={row.reading}
						session={row.session}
					/>
				))}
				{reviewers.length > 0 ? <GroupRow label={t("shell.memoryGroupReviewers")} /> : null}
				{reviewers.map((reviewer) => (
					<FixedRow
						isExpanded={expanded.has(`review:${reviewer.reviewId}`)}
						key={reviewer.reviewId}
						label={reviewerLabel(reviewer, titles, t)}
						maxBytes={maxBytes}
						onToggle={() => toggleExpanded(`review:${reviewer.reviewId}`)}
						reading={reviewer.memory}
						subtitle={reviewer.harness}
						testId="session-memory-reviewer-row"
						type={t("shell.memoryCategory.reviewer")}
					/>
				))}
				{app?.own ? (
					<>
						<GroupRow label={t("shell.memoryGroupApp")} />
						<FixedRow
							isExpanded={expanded.has("ao")}
							label={t("shell.memoryOwnRow")}
							maxBytes={maxBytes}
							onToggle={() => toggleExpanded("ao")}
							own
							reading={app.own}
							testId="session-memory-own-row"
							type={t("shell.memoryCategory.ao")}
						/>
					</>
				) : null}
			</tbody>
		</table>
		</div>
		</SettingsSection>
	);
}
