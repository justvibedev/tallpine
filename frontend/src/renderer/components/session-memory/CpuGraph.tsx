/** CPU graph. */
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";
import { formatCPU, sampleHistoryLength, type CPUSample, type SystemMemoryReading } from "../../hooks/useSessionMemory";
import { SettingsSection } from "../settings/SettingsSection";

/** Graph geometry: a wide, short strip like btop's, in SVG user units. */
export const graphWidth = 600;
export const graphHeight = 56;

/** An SVG path along the samples, newest on the right. */
export function linePath(values: number[]): string {
	if (values.length === 0) return "";
	const step = graphWidth / (sampleHistoryLength - 1);
	const x0 = graphWidth - step * (values.length - 1);
	const points = values.map((v, i) => `${(x0 + i * step).toFixed(1)},${(graphHeight - (Math.min(100, Math.max(0, v)) / 100) * graphHeight).toFixed(1)}`);
	return points.length > 1 ? `M${points[0]} L${points.slice(1).join(" L")}` : `M${points[0]}`;
}

/**
 * Two lines over the last two minutes: how busy the machine is, and Tallpine's
 * share of it. Newest sample on the right. No per-core detail — the number
 * that matters is whether the machine has room left.
 */
export function CpuGraph({ current, history, system }: { current: CPUSample; history: CPUSample[]; system: SystemMemoryReading }) {
	const { t } = useTranslation();
	const now = current;
	return (
		<SettingsSection
			title={t("shell.cpuBarTitle", { cores: system.cpuCount })}
			testId="session-cpu-graph"
			grouped
			// A negative load average is Windows' "not applicable" sentinel: the platform has no such concept, and printing 0.00 would read as an idle machine rather than a missing number.
			action={system.load1 >= 0 ? (
				<span className="font-mono text-xs tabular-nums text-settings-muted">{t("shell.cpuBarLoad", { load: system.load1.toFixed(2) })}</span>
			) : undefined}
		>
			<div className="settings-row-bar h-auto flex-col items-stretch gap-2 py-3">
				<svg aria-hidden="true" className="h-14 w-full" preserveAspectRatio="none" viewBox={`0 0 ${graphWidth} ${graphHeight}`}>
					{[25, 50, 75].map((line) => (
						<line
							className="stroke-foreground/[0.06]"
							key={line}
							strokeWidth={1}
							x1={0}
							x2={graphWidth}
							y1={graphHeight - (line / 100) * graphHeight}
							y2={graphHeight - (line / 100) * graphHeight}
						/>
					))}
					<path className="fill-none stroke-warning/80" d={linePath(history.map((s) => s.host))} strokeWidth={1.5} vectorEffect="non-scaling-stroke" />
					<path className="fill-none stroke-accent-strong" d={linePath(history.map((s) => s.tallpine))} strokeWidth={1.5} vectorEffect="non-scaling-stroke" />
				</svg>
				{/* A line's colour means nothing without a key: name each one,
				    the same way the memory bar names its parts. */}
				<div className="flex flex-wrap gap-x-4 gap-y-1 font-mono text-xs tabular-nums text-settings-muted">
					{[
						{ key: "machine", className: "bg-warning/80", label: t("shell.cpuLegendMachine"), value: now.host },
						{ key: "ao", className: "bg-accent-strong", label: t("shell.memoryLegendTallpine"), value: now.tallpine },
					].map((part) => (
						<span className="inline-flex items-center gap-1.5" key={part.key}>
							<span aria-hidden="true" className={cn("size-1.5 rounded-full", part.className)} />
							{part.label} <span className="text-settings-label">{formatCPU(part.value)}</span>
						</span>
					))}
				</div>
			</div>
		</SettingsSection>
	);
}
