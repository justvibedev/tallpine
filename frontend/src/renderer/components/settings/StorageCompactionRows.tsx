import { useTranslation } from "react-i18next";
import { useManagedLocks } from "../../hooks/useManagedLocks";
import { useSettings, useUpdateStorageCompaction } from "../../hooks/useSettings";
import { Switch } from "../ui/switch";
import { AgentPolicyLock } from "./AgentPolicyLock";
import { SettingsOptionMenu, type SettingsOption } from "./SettingsOptionMenu";
import { SettingsRow } from "./SettingsRow";

const RETENTION_DAYS = ["7", "14", "30", "90", "365"] as const;

/**
 * Daemon-owned storage compaction: skip activity writes that change nothing and
 * keep the raw provider-event archive small. Conversation history renders from
 * the projected timeline, so it is unaffected either way.
 */
export function StorageCompactionRows() {
	const { t } = useTranslation();
	const { settings, isLoading } = useSettings();
	const { update, saving, error } = useUpdateStorageCompaction();
	const retentionLocked = useManagedLocks().has("app.providerEventRetentionDays");
	const enabled = settings?.storageCompaction ?? false;
	const days = String(settings?.providerEventRetentionDays ?? 14);
	const options: SettingsOption<string>[] = RETENTION_DAYS.map((value) => ({
		value,
		label: t("settings.storageCompaction.retentionDays", { days: value }),
	}));
	if (!options.some((option) => option.value === days)) {
		options.push({ value: days, label: t("settings.storageCompaction.retentionDays", { days }) });
	}
	return (
		<>
			<div className="flex w-full flex-col">
				<SettingsRow
					className="rounded-none"
					label={t("settings.storageCompaction.label")}
					description={t("settings.storageCompaction.description")}
				>
					<Switch
						aria-label={t("settings.storageCompaction.label")}
						checked={enabled}
						disabled={isLoading || saving || !settings}
						onCheckedChange={(next) => update({ enabled: next })}
					/>
				</SettingsRow>
				{error ? (
					<p role="alert" className="px-3 pb-2 text-caption leading-4 text-error">
						{error}
					</p>
				) : null}
			</div>
			<SettingsRow
				label={t("settings.storageCompaction.retention")}
				description={t("settings.storageCompaction.retentionDescription")}
			>
				{retentionLocked ? <AgentPolicyLock source="managed" /> : null}
				<SettingsOptionMenu
					aria-label={t("settings.storageCompaction.retention")}
					value={days}
					options={options}
					onChange={(next) => update({ providerEventRetentionDays: Number(next) })}
					disabled={isLoading || saving || !settings || !enabled || retentionLocked}
				/>
			</SettingsRow>
		</>
	);
}
