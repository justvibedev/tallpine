/** Diagnostics body, memory diagnostics page and the memory panel. */
import { useEffect, useRef, useState, type RefObject } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";
import { useWorkspaceQuery } from "../../hooks/useWorkspaceQuery";
import { formatCPU, formatMemory, useAppMemory, useFastMemorySampling, cpuSampleOf, useCPUHistory } from "../../hooks/useSessionMemory";
import { CpuGraph } from "./CpuGraph";
import { EMPTY_WORKSPACES } from "./session-memory-facts";
import { ReportRow, diagnosticsReport, sessionTitles } from "./session-memory-reports";
import { MachineSection } from "./MachineSection";
import { CopyControl } from "./CopyControl";
import { SessionsTable } from "./SessionsTable";

/** The CPU graph and its per-core bars. */
export function CpuSection() {
	const appMemory = useAppMemory().data;
	const system = appMemory?.system;
	const cpuHistory = useCPUHistory();
	// An unmeasured reading shows the last real point instead of a false zero.
	const current = cpuSampleOf(system, appMemory?.app) ?? cpuHistory.at(-1);
	if (!system || !current) return null;
	return <CpuGraph current={current} history={cpuHistory} system={system} />;
}

/**
 * The window and the settings page are the same thing: machine, CPU, then the
 * sessions, in one scroller. Nothing is pinned to the top or the bottom — on a
 * 680px dialog that cost ~290px of permanent chrome and left four rows of
 * list. The numbers stay reachable through the summary line, which only
 * appears once the graphs have scrolled away.
 */
export function DiagnosticsBody({ projectId, scroller, underToolbar }: { projectId?: string; scroller?: RefObject<HTMLElement | null>; underToolbar?: boolean }) {
	const { t } = useTranslation();
	const appMemory = useAppMemory().data;
	const workspaces = useWorkspaceQuery().data ?? EMPTY_WORKSPACES;
	const [rows, setRows] = useState<ReportRow[]>([]);
	const graphs = useRef<HTMLDivElement>(null);
	const scrolledPast = useScrolledPast(graphs, scroller);
	return (
		<>
			<div className="flex flex-col gap-(--size-settings-section-gap,1.5rem)" ref={graphs}>
				<MachineSection
					action={
						<CopyControl
							copiedLabel={t("shell.memoryReportCopied")}
							label={t("shell.memoryCopyReport")}
							testId="session-memory-copy"
							value={() => diagnosticsReport({ app: appMemory?.app, system: appMemory?.system }, rows, t, sessionTitles(workspaces))}
						>
							<span>{t("shell.memoryCopyReport")}</span>
						</CopyControl>
					}
				/>
				<CpuSection />
			</div>
			{/* One block with the sessions, so the zero-height bar adds no section gap. */}
			<div className="flex w-full flex-col">
			{/* Zero height, so nothing shifts when the bar appears: the bar itself
			    floats over the rows it is pinned above. On the settings page it pins
			    below the window's transparent toolbar strip. */}
			<div className={cn("sticky z-20 h-0 overflow-visible", underToolbar ? "top-toolbar" : "top-0")}>
				<div
					aria-hidden={!scrolledPast}
					className={cn(
						"absolute inset-x-0 top-0 flex h-8 items-center gap-4 border-b border-(--color-border-settings-dialog-header) px-(--settings-row-inset) font-mono text-xs tabular-nums text-settings-muted transition-opacity",
						underToolbar ? "bg-(--color-bg-primary)" : "bg-popover",
						scrolledPast ? "opacity-100" : "pointer-events-none opacity-0",
					)}
					data-testid="session-memory-pinned"
				>
					{appMemory?.app ? (
						<span>
							{t("shell.memoryLegendTallpine")} <span className="text-settings-label">{formatMemory(appMemory.app.rssBytes)}</span>
							{appMemory.system ? ` · ${formatMemory(appMemory.system.availableBytes)} ${t("shell.memoryLegendAvailable").toLowerCase()}` : null}
						</span>
					) : null}
					{appMemory?.system ? (
						<span>
							{t("shell.memoryColumnCpu")} <span className="text-settings-label">{formatCPU(appMemory.system.cpuPercent)}</span>
						</span>
					) : null}
				</div>
			</div>
			<SessionsTable onRows={setRows} projectId={projectId} underToolbar={underToolbar} />
			</div>
		</>
	);
}

/**
 * True once `target` has scrolled out of the top of its scroller. An observer
 * rather than a scroll listener: no work at all while the target is on screen.
 */
export function useScrolledPast(target: RefObject<HTMLElement | null>, scroller?: RefObject<HTMLElement | null>): boolean {
	const [past, setPast] = useState(false);
	useEffect(() => {
		const element = target.current;
		if (!element || typeof IntersectionObserver !== "function") return;
		const observer = new IntersectionObserver(([entry]) => setPast(!entry.isIntersecting), {
			root: scroller?.current ?? null,
			threshold: 0,
		});
		observer.observe(element);
		return () => observer.disconnect();
	}, [target, scroller]);
	return past;
}

/** Settings page: the settings body is the scroller, so this only supplies content. */
export function MemoryDiagnostics() {
	useFastMemorySampling();
	return (
		<div className="settings-dialog-body flex w-full flex-col gap-(--size-settings-section-gap,1.5rem)" data-testid="memory-diagnostics">
			<DiagnosticsBody underToolbar />
		</div>
	);
}
