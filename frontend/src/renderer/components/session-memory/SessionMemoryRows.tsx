/** Row and cell components for the session memory table. */
import { type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { ChevronRight } from "lucide-react";
import { type ChipTone } from "@tallpine/product-ui";
import { cn } from "@/lib/utils";
import { formatDurationClock as formatDuration } from "@tallpine/shared/format/duration";
import { formatTimeTerse } from "../../lib/format-time";
import { formatCPU, formatMemory, type SessionMemoryReading, type SessionStepReading } from "../../hooks/useSessionMemory";
import { isOrchestratorSession, type WorkspaceSession } from "../../types/workspace";
import { columnCount, cell } from "./SessionsTable";
import { ProcessRows } from "./ProcessRows";
import { RecentSteps, SpacerRow } from "./FixedRows";

export function GroupRow({ label }: { label: string }) {
	return (
		<tr className="memory-group">
			<td className="pb-2 pt-6 text-xs font-medium leading-4 text-settings-muted first:pt-0" colSpan={columnCount}>{label}</td>
		</tr>
	);
}

/** A session is not a process, so its PID cell says what is under it and
 * that the row opens: "3 processes ›". */
export function ProcessCountCell({ count, isExpanded }: { count: number; isExpanded: boolean }) {
	const { t } = useTranslation();
	return (
		<td className={cn("whitespace-nowrap py-2 align-middle text-xs text-settings-muted", cell.pid)} data-testid="session-memory-process-count">
			{count > 0 && !isExpanded ? t("shell.memoryProcessCount", { count }) : null}
		</td>
	);
}

/** Memory cell: the number over a bar scaled to the biggest row, so "which one is the pig" reads at a glance. */
export function MemoryCell({ bytes, maxBytes, tone }: { bytes: number; maxBytes: number; tone: ChipTone }) {
	return (
		<td className={cn("whitespace-nowrap py-2 align-middle font-mono text-xs tabular-nums", cell.memory)}>
			<span className={cn("font-medium", tone === "critical" ? "text-destructive" : tone === "warning" ? "text-warning" : "text-settings-label")}>
				{formatMemory(bytes)}
			</span>
			<div className="ml-auto mt-1 h-0.5 w-16 rounded-sm bg-foreground/[0.06]" data-testid="session-memory-share">
				<div
					className={cn("h-full rounded-sm transition-[width] duration-500", tone === "critical" ? "bg-destructive" : tone === "warning" ? "bg-warning" : "bg-accent-strong")}
					style={{ width: `${Math.min(100, (bytes / maxBytes) * 100)}%` }}
				/>
			</div>
		</td>
	);
}

/** Enter/Space activates a row the same way a click does, so an expandable
 * row is reachable without a mouse. */
export function toggleOnKeyDown(onToggle: () => void) {
	return (event: KeyboardEvent<HTMLTableRowElement>) => {
		if (event.key === "Enter" || event.key === " ") {
			event.preventDefault();
			onToggle();
		}
	};
}

export function SessionRow({
	chip,
	isExpanded,
	maxBytes,
	onToggle,
	reading,
	session,
}: {
	chip: ChipTone;
	isExpanded: boolean;
	maxBytes: number;
	onToggle: () => void;
	reading: SessionMemoryReading;
	session: WorkspaceSession;
}) {
	const { t } = useTranslation();
	const working = session.activity?.state === "active";
	const recent = reading.activity?.recent ?? [];
	const canExpand = reading.processes.length > 0 || recent.length > 0;
	return (
		<>
			<tr
				aria-expanded={canExpand ? isExpanded : undefined}
				className={cn("group/row memory-row", canExpand && "cursor-pointer hover:bg-interactive-hover")}
				data-chip-tone={chip}
				data-testid="session-memory-row"
				onClick={canExpand ? onToggle : undefined}
				onKeyDown={canExpand ? toggleOnKeyDown(onToggle) : undefined}
				tabIndex={canExpand ? 0 : undefined}
			>
				<td className={cn("py-2 align-middle", cell.name)}>
					<div className="flex items-center gap-1.5">
						<ChevronRight
							aria-hidden="true"
							className={cn("size-icon-2xs shrink-0 text-passive transition-transform", canExpand ? "opacity-100" : "opacity-0", isExpanded && "rotate-90")}
						/>
						<div className="min-w-0">
							<div className="truncate text-sm font-medium text-settings-label" title={session.title}>{session.title}</div>
							<StatusLine current={reading.activity?.current} session={session} working={working} />
						</div>
					</div>
				</td>
				{/* A session's type is its role; its process lines carry their own. */}
				<td className={cn("whitespace-nowrap py-2 align-middle font-mono text-xs text-passive", cell.type)} data-testid="session-memory-type">
					{isOrchestratorSession(session) ? t("settings.models.orchestratorRole") : t("settings.models.workerRole")}
				</td>
				<ProcessCountCell count={reading.processes.length} isExpanded={isExpanded} />
				<MemoryCell bytes={reading.rssBytes} maxBytes={maxBytes} tone={chip} />
				<td className={cn("whitespace-nowrap py-2 align-middle font-mono text-xs tabular-nums text-settings-muted", cell.cpu)}>
					{formatCPU(reading.cpuPercent)}
				</td>
			</tr>
			{isExpanded ? <ProcessRows processes={reading.processes} /> : null}
			{isExpanded && recent.length > 0 ? <RecentSteps steps={recent} /> : null}
			{isExpanded ? <SpacerRow /> : null}
		</>
	);
}

/**
 * The one line under a title. Working with a known step: the step and how
 * long it has run. Working otherwise: just "working". Idle: how long for.
 * The row re-renders on every sample, so the duration ticks with it.
 */
export function statusText(current: SessionStepReading | undefined, session: WorkspaceSession, working: boolean, t: TFunction): string {
	if (isOrchestratorSession(session) && !current) return t("shell.memoryRowOrchestrator");
	if (current) return `${current.tool} · ${formatDuration(Date.now() - Date.parse(current.startedAt))}`;
	if (working) return t("shell.memoryRowWorking");
	const since = formatTimeTerse(session.activity?.lastActivityAt);
	return since === "now" ? t("shell.memoryRowIdle") : t("shell.memoryRowIdleFor", { time: since });
}

export function StatusLine({ current, session, working }: { current?: SessionStepReading; session: WorkspaceSession; working: boolean }) {
	const { t } = useTranslation();
	// Which agent runs the session, with its name rather than in the Type column.
	return (
		<div className="truncate text-xs text-settings-muted">
			{session.provider ? <span data-testid="session-memory-agent">{session.provider} · </span> : null}
			<span data-testid="session-memory-status">{statusText(current, session, working, t)}</span>
		</div>
	);
}
