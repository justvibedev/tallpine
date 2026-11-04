/** Recent steps and fixed rows in the memory table. */
import { useTranslation } from "react-i18next";
import { ChevronRight } from "lucide-react";
import { cn } from "@/lib/utils";
import { formatDurationClock as formatDuration } from "@tallpine/shared/format/duration";
import { formatCPU, type SessionMemoryReading, type SessionStepReading } from "../../hooks/useSessionMemory";
import { columnCount, cell } from "./SessionsTable";
import { toggleOnKeyDown, ProcessCountCell, MemoryCell } from "./SessionMemoryRows";
import { ProcessRows } from "./ProcessRows";

/** The last few tool calls under an expanded row, newest first. */
export function RecentSteps({ steps }: { steps: SessionStepReading[] }) {
	const { t } = useTranslation();
	return (
		<>
			<tr>
				<td className="pb-1 pl-12 pt-2 text-xs font-medium text-settings-muted" colSpan={columnCount}>{t("shell.memoryRecent")}</td>
			</tr>
			{steps.map((step) => {
				const started = new Date(step.startedAt);
				const duration = step.endedAt ? Date.parse(step.endedAt) - started.getTime() : undefined;
				return (
					<tr className="text-xs" data-testid="session-memory-step-row" key={`${step.startedAt}-${step.tool}`}>
						<td className={cn("py-1 font-mono", cell.process)}>
							<div className="flex min-w-0 items-baseline gap-3">
								<span className="shrink-0 text-passive">{started.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}</span>
								<span className={cn("min-w-0 truncate", step.failed ? "text-error" : "text-settings-label")}>{step.tool}</span>
								{step.failed ? <span className="shrink-0 text-error">{t("shell.memoryStepFailed")}</span> : null}
							</div>
						</td>
						<td />
						<td />
						<td />
						<td className={cn("whitespace-nowrap py-1 font-mono tabular-nums text-passive", cell.cpu)}>
							{duration !== undefined && duration >= 1000 ? formatDuration(duration) : "·"}
						</td>
					</tr>
				);
			})}
		</>
	);
}

/** A row that is real cost but not a session, so it has no action: Tallpine's
 * daemon and desktop shell, or a reviewer pane. */
export function FixedRow({ isExpanded, label, maxBytes, onToggle, own = false, reading, subtitle, testId, type }: {
	isExpanded: boolean;
	label: string;
	maxBytes: number;
	onToggle: () => void;
	own?: boolean;
	reading: SessionMemoryReading;
	subtitle?: string;
	testId: string;
	type: string;
}) {
	const canExpand = reading.processes.length > 0;
	return (
		<>
			<tr
				aria-expanded={canExpand ? isExpanded : undefined}
				className={cn("memory-row", canExpand && "cursor-pointer hover:bg-interactive-hover")}
				data-testid={testId}
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
							<div className="truncate text-sm font-medium text-settings-label" title={label}>{label}</div>
							{subtitle ? <div className="truncate text-xs text-settings-muted">{subtitle}</div> : null}
						</div>
					</div>
				</td>
				<td className={cn("whitespace-nowrap py-2 align-middle font-mono text-xs text-passive", cell.type)} data-testid="session-memory-type">
					{type}
				</td>
				<ProcessCountCell count={reading.processes.length} isExpanded={isExpanded} />
				<MemoryCell bytes={reading.rssBytes} maxBytes={maxBytes} tone="neutral" />
				<td className={cn("whitespace-nowrap py-2 align-middle font-mono text-xs tabular-nums text-settings-muted", cell.cpu)}>{formatCPU(reading.cpuPercent)}</td>
			</tr>
			{isExpanded ? <ProcessRows own={own} processes={reading.processes} /> : null}
			{isExpanded ? <SpacerRow /> : null}
		</>
	);
}

/** Breathing room under an expanded block, so the next rule is not glued to the last child. */
export function SpacerRow() {
	return (
		<tr aria-hidden="true">
			<td className="h-2 p-0" colSpan={columnCount} />
		</tr>
	);
}
