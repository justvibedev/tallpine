/** Machine memory bar, app memory indicator and machine section. */
import { type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Loader2 } from "lucide-react";
import { cn } from "@/lib/utils";
import { formatMemory, useAppMemory, type SystemMemoryReading } from "../../hooks/useSessionMemory";
import { SettingsSection } from "../settings/SettingsSection";
import { Spinner } from "../ui/spinner";

/** One bar: Tallpine against free, and nothing else. The bar is scaled to the two
 * of them, so there is no gap standing in for other apps. */
export function MachineBar({ appBytes, system }: { appBytes: number; system: SystemMemoryReading }) {
	const { t } = useTranslation();
	const total = appBytes + system.availableBytes || 1;
	const pct = (bytes: number) => `${Math.min(100, (bytes / total) * 100)}%`;
	const legend = [
		{ key: "ao", label: t("shell.memoryLegendTallpine"), bytes: appBytes, className: "bg-accent-strong" },
		{ key: "free", label: t("shell.memoryLegendAvailable"), bytes: system.availableBytes, className: "bg-success/70" },
	];
	return (
		<div className="settings-row-bar h-auto flex-col items-stretch gap-2 py-3" data-testid="session-memory-stacked">
			<div className="flex h-2 w-full overflow-hidden rounded-sm bg-foreground/[0.06]">
				<div className="h-full bg-accent-strong transition-[width] duration-500" style={{ width: pct(appBytes) }} />
				<div className="h-full bg-success/70 transition-[width] duration-500" style={{ width: pct(system.availableBytes) }} />
			</div>
			<div className="flex flex-wrap gap-x-4 gap-y-1 font-mono text-xs tabular-nums text-settings-muted">
				{legend.map((part) => (
					<span className="inline-flex items-center gap-1.5" key={part.key}>
						<span aria-hidden="true" className={cn("size-1.5 rounded-full", part.className)} />
						{part.label} <span className="text-settings-label">{formatMemory(part.bytes)}</span>
					</span>
				))}
			</div>
		</div>
	);
}

/** The machine's own memory: Tallpine against what is free. */
export function MachineSection({ action }: { action?: ReactNode }) {
	const { t } = useTranslation();
	const appMemory = useAppMemory().data;
	const app = appMemory?.app;
	const system = appMemory?.system;
	return (
		<SettingsSection title={t("shell.memorySectionMachine")} action={action} grouped>
			{system && app ? (
				<MachineBar appBytes={app.rssBytes} system={system} />
			) : (
				// Never an empty card: until the first sample lands, one row holds its place.
				<p aria-busy="true" className="settings-row-bar text-sm text-settings-muted" data-testid="session-memory-loading">
					<span className="inline-flex items-center gap-2">
						<Spinner icon={Loader2} className="size-icon-sm" />
						{t("common.loading")}
					</span>
				</p>
			)}
		</SettingsSection>
	);
}
