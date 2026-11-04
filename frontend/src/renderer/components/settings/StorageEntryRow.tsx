import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useDeleteStorageEntry, type StorageEntry } from "../../hooks/useStorage";
import { Button } from "../ui/button";
import { SettingsRow } from "./SettingsRow";
import { formatBytes } from "@tallpine/shared/format/bytes";
import { ageParts } from "./storageFormat";

/** One thing on disk: what it is, who owns it, how old, how big, and a
 * two-step remove (or why it cannot be removed). */
export function StorageEntryRow({ entry, now }: { entry: StorageEntry; now: number }) {
	const { t } = useTranslation();
	const [confirming, setConfirming] = useState(false);
	const { remove, pending, error, reset } = useDeleteStorageEntry();
	const age = ageParts(entry.newestAt, now);
	const owner = [entry.projectId, entry.sessionId].filter(Boolean).join(" / ");
	const parts = [
		owner,
		entry.state ? t(`settings.storageUsage.state.${entry.state}`) : "",
		age ? t(`settings.storageUsage.age.${age.unit}`, { count: age.count }) : "",
		entry.partial ? t("settings.storageUsage.partial") : "",
	].filter(Boolean);
	const name = entry.path.split("/").pop() || entry.label;

	const run = async () => {
		try {
			await remove(entry.id);
		} catch {
			// The error renders below the row; the list refetches either way.
		}
		setConfirming(false);
	};

	return (
		<SettingsRow
			label={<span title={entry.path}>{entry.category === "database" ? entry.label : entry.label === name ? name : `${entry.label} · ${name}`}</span>}
			description={
				<>
					{parts.join(" · ")}
					{entry.protected ? <span className="block">{entry.protected}</span> : null}
					{error ? <span role="alert" className="block text-error">{error}</span> : null}
				</>
			}
		>
			<div className="flex items-center gap-3">
				<span className="w-20 text-right text-sm tabular-nums text-settings-label">
					{entry.partial ? "≥ " : ""}
					{formatBytes(entry.bytes)}
				</span>
				{entry.protected ? null : confirming ? (
					<>
						<Button type="button" variant="footer" disabled={pending} onClick={() => { setConfirming(false); reset(); }}>
							{t("confirm.cancel")}
						</Button>
						<Button type="button" variant="footer-primary" disabled={pending} onClick={run}>
							{t("settings.storageUsage.confirmRemove")}
						</Button>
					</>
				) : (
					<Button type="button" variant="footer" onClick={() => { reset(); setConfirming(true); }}>
						{t("settings.storageUsage.remove")}
					</Button>
				)}
			</div>
		</SettingsRow>
	);
}
