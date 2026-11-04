/** Process tree and process rows. */
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { ChevronRight } from "lucide-react";
import { processKind, processList, type ProcessRow as ProcessListRow } from "@tallpine/product-ui";
import { cn } from "@/lib/utils";
import { formatCPU, formatMemory, type SessionMemoryReading } from "../../hooks/useSessionMemory";
import { toggleOnKeyDown } from "./SessionMemoryRows";
import { cell } from "./SessionsTable";
import { CopyControl } from "./CopyControl";

// The copied report and older imports still name processes the same way.
export { processKind };

/**
 * Each process under its parent, siblings largest first, depth as indent. A
 * child whose parent is outside the row starts at the top. Shared by the
 * rendered tree and the copied report.
 */
export function processTree(processes: SessionMemoryReading["processes"]): { process: SessionMemoryReading["processes"][number]; depth: number; last: boolean }[] {
	const pids = new Set(processes.map((p) => p.pid));
	const children = new Map<number, SessionMemoryReading["processes"]>();
	for (const p of processes) {
		const parent = pids.has(p.ppid) && p.ppid !== p.pid ? p.ppid : -1;
		children.set(parent, [...(children.get(parent) ?? []), p]);
	}
	const rows: { process: SessionMemoryReading["processes"][number]; depth: number; last: boolean }[] = [];
	const walk = (parent: number, depth: number) => {
		const kids = [...(children.get(parent) ?? [])].sort((a, b) => b.rssBytes - a.rssBytes);
		kids.forEach((kid, index) => {
			rows.push({ process: kid, depth, last: index === kids.length - 1 });
			walk(kid.pid, depth + 1);
		});
	};
	walk(-1, 0);
	return rows;
}

/**
 * The process list on screen: one flat line per program, plumbing folded in,
 * the small tail summed into "N other processes". The rows add up to the
 * session; the copied report keeps every raw process instead.
 */
export function ProcessRows({ processes, own = false }: { processes: SessionMemoryReading["processes"]; own?: boolean }) {
	const { t } = useTranslation();
	const { rows, other } = useMemo(() => processList(processes, { own }), [processes, own]);
	const [showAll, setShowAll] = useState(false);
	const toggle = () => setShowAll((current) => !current);
	return (
		<>
			{[...rows, ...(showAll && other ? other.rows : [])].map((row) => (
				<ProcessRow key={row.pid} row={row} />
			))}
			{other ? (
				<tr
					aria-expanded={showAll}
					className="cursor-pointer text-xs hover:bg-interactive-hover"
					data-testid="session-memory-process-other"
					onClick={toggle}
					onKeyDown={toggleOnKeyDown(toggle)}
					tabIndex={0}
				>
					<td className={cn("py-1 font-mono text-passive", cell.process)}>
						<span className="flex min-w-0 items-center gap-1" title={showAll ? undefined : other.commands.join("\n")}>
							<span className="truncate">{showAll ? t("shell.memoryProcessFewer") : t("shell.memoryProcessOther", { count: other.count })}</span>
							<ChevronRight aria-hidden="true" className={cn("size-icon-2xs shrink-0 transition-transform", showAll ? "-rotate-90" : "rotate-90")} />
						</span>
					</td>
					<td />
					<td />
					<td className={cn("whitespace-nowrap py-1 font-mono tabular-nums text-settings-muted", cell.memory)}>{showAll ? null : formatMemory(other.bytes)}</td>
					<td className={cn("whitespace-nowrap py-1 font-mono tabular-nums text-passive", cell.cpu)}>
						{showAll ? null : other.cpu >= 1 ? formatCPU(other.cpu) : "·"}
					</td>
				</tr>
			) : null}
		</>
	);
}

export function ProcessRow({ row }: { row: ProcessListRow }) {
	const { t } = useTranslation();
	return (
		<tr className="text-xs" data-testid="session-memory-process-row">
			<td className={cn("py-1 font-mono text-settings-muted", cell.process)}>
				<span className="block truncate" title={row.commands.join("\n")}>{row.kind}</span>
			</td>
			<td className={cn("whitespace-nowrap py-1 font-mono text-passive", cell.type)} data-testid="session-memory-process-type">
				<span title={t(`shell.memoryCategoryHint.${row.category}`)}>{t(`shell.memoryCategory.${row.category}`)}</span>
			</td>
			<td className={cn("whitespace-nowrap py-1 align-middle font-mono tabular-nums", cell.pid)}>
				<CopyControl
					copiedLabel={t("shell.memoryPidCopied", { pid: row.pid })}
					label={t("shell.memoryCopyPid", { pid: row.pid })}
					testId="session-memory-pid"
					value={() => String(row.pid)}
				>
					<span>{row.pid}</span>
				</CopyControl>
			</td>
			<td className={cn("whitespace-nowrap py-1 font-mono tabular-nums text-settings-muted", cell.memory)}>{formatMemory(row.bytes)}</td>
			<td className={cn("whitespace-nowrap py-1 font-mono tabular-nums text-passive", cell.cpu)}>{row.cpu >= 1 ? formatCPU(row.cpu) : "·"}</td>
		</tr>
	);
}
