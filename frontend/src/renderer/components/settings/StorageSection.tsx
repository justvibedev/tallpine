import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useCleanStorage, useStorage, type StorageCleanupReport } from "../../hooks/useStorage";
import { Button } from "../ui/button";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";
import { StorageEntryRow } from "./StorageEntryRow";
import { formatBytes } from "@tallpine/shared/format/bytes";
import { cleanupFailures, groupEntries } from "./storageFormat";

/**
 * Settings > Storage: everything Tallpine and its agents keep on disk, grouped,
 * with size, age, owner and state, and a remove action per item. The daemon
 * measures in the background; "Clean up everything safe" previews first.
 */
export function StorageSection({ titleHidden }: { titleHidden?: boolean }) {
	const { t } = useTranslation();
	const { report, isLoading, error, refresh } = useStorage();
	const { clean, pending, error: cleanError, reset } = useCleanStorage();
	const [preview, setPreview] = useState<StorageCleanupReport | null>(null);
	const [freed, setFreed] = useState<{ bytes: number; failed: number; reason: string } | null>(null);
	const now = Date.now();

	const startClean = async () => {
		setFreed(null);
		try {
			setPreview(await clean(true));
		} catch {
			// Renders from the mutation error.
		}
	};
	const confirmClean = async () => {
		try {
			const done = await clean(false);
			setPreview(null);
			setFreed({ bytes: done.freedBytes, ...(({ count, reason }) => ({ failed: count, reason }))(cleanupFailures(done)) });
		} catch {
			// Renders from the mutation error.
		}
	};
	const wouldFree = preview?.categories.reduce((sum, c) => sum + c.wouldFreeBytes, 0) ?? 0;
	const groups = groupEntries(report?.entries ?? []);
	const goCache = report?.goCache;
	const loading = !report ? isLoading : report.refreshing && report.entries.length === 0;

	return (
		<>
			<SettingsSection
				title={t("settings.storageUsage.title")}
				titleHidden={titleHidden}
				grouped
				action={
					<Button type="button" variant="ghost" size="sm" onClick={refresh} disabled={report?.refreshing}>
						{t("settings.storageUsage.refresh")}
					</Button>
				}
			>
				<SettingsRow
					label={
						loading
							? t("settings.storageUsage.measuring")
							: report
								? t("settings.storageUsage.total", { size: formatBytes(report.totalBytes) })
								: t("settings.storageUsage.title")
					}
					description={
						<>
							{report?.refreshing && !loading ? <span role="status" className="block">{t("settings.storageUsage.measuring")}</span> : null}
							{error ? <span role="alert" className="block text-error">{error}</span> : null}
							{cleanError ? <span role="alert" className="block text-error">{cleanError}</span> : null}
							{freed !== null ? (
								<span role="status" className="block">
									{t("settings.storageUsage.freed", { size: formatBytes(freed.bytes) })}
									{freed.failed > 0 ? ` ${t("settings.storageUsage.cleanFailed", { count: freed.failed, reason: freed.reason })}` : ""}
								</span>
							) : null}
							{preview ? (
								<span role="status" className="block">
									{wouldFree > 0
										? t("settings.storageUsage.wouldFree", { size: formatBytes(wouldFree) })
										: t("settings.storageUsage.nothingToClean")}
								</span>
							) : null}
						</>
					}
				>
					{preview && wouldFree > 0 ? (
						<div className="flex items-center gap-2">
							<Button type="button" variant="footer" disabled={pending} onClick={() => { setPreview(null); reset(); }}>
								{t("confirm.cancel")}
							</Button>
							<Button type="button" variant="footer-primary" disabled={pending} onClick={confirmClean}>
								{t("settings.storageUsage.cleanConfirm")}
							</Button>
						</div>
					) : (
						<Button type="button" variant="footer" disabled={pending || !report} onClick={startClean}>
							{t("settings.storageUsage.clean")}
						</Button>
					)}
				</SettingsRow>
				{goCache ? (
					<SettingsRow
						label={t("settings.storageUsage.goCache")}
						description={goCache.capBytes > 0 ? t("settings.storageUsage.goCacheCap", { cap: formatBytes(goCache.capBytes) }) : t("settings.storageUsage.goCacheNoCap")}
					>
						<span className="text-sm tabular-nums text-settings-label">
							{goCache.partial ? "≥ " : ""}
							{formatBytes(goCache.bytes)}
						</span>
					</SettingsRow>
				) : null}
			</SettingsSection>
			{groups.map((group) => (
				<SettingsSection
					key={group.category}
					title={t(`settings.storageUsage.category.${group.category}`, { defaultValue: group.category })}
					grouped
					action={<span className="text-sm tabular-nums text-settings-muted">{formatBytes(group.bytes)}</span>}
				>
					{group.entries.map((entry) => (
						<StorageEntryRow key={entry.id} entry={entry} now={now} />
					))}
				</SettingsSection>
			))}
			{report && report.processes.length > 0 ? (
				<SettingsSection title={t("settings.storageUsage.processes")} grouped>
					{report.processes.map((p) => (
						<SettingsRow
							key={p.pid}
							label={<span title={p.command}>{p.command.split("/").pop() || String(p.pid)}</span>}
							description={[p.projectId, p.sessionId, t(`settings.storageUsage.state.${p.state}`), `PID ${p.pid}`].filter(Boolean).join(" · ")}
						>
							<span className="text-sm text-settings-muted">{t("settings.storageUsage.reaperStops")}</span>
						</SettingsRow>
					))}
				</SettingsSection>
			) : null}
		</>
	);
}
