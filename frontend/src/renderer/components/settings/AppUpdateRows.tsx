import { useTranslation } from "react-i18next";
import type { UpdateChannel, UpdateStatus } from "../../../main/update-settings";
import { useAppUpdatePreferences, useAppUpdateStatus } from "../../hooks/useAppUpdates";
import { tallpineBridge } from "../../lib/bridge";
import { Button } from "../ui/button";
import { Switch } from "../ui/switch";
import { AgentPolicyLock } from "./AgentPolicyLock";
import { SettingsOptionMenu, type SettingsOption } from "./SettingsOptionMenu";
import { SettingsRow } from "./SettingsRow";

function useStatusText(status: UpdateStatus | null): string | null {
	const { t } = useTranslation();
	if (!status) return null;
	switch (status.state) {
		case "disabled":
			switch (status.reason) {
				case "user":
					return null;
				case "managed":
					return t("settings.appUpdate.off.managed", "Turned off by your organization.");
				case "unpackaged":
				case "not-a-release":
					return t("settings.appUpdate.off.localBuild", "Not available on development or local builds.");
				case "unsigned":
					return t("settings.appUpdate.off.unsigned", "Not available: this build is not signed for release.");
				default:
					return t("settings.appUpdate.off.unsupported", "Not available on this platform.");
			}
		case "checking":
			return t("settings.appUpdate.checking", "Checking for updates…");
		case "downloading":
			return t("settings.appUpdate.downloading", "Downloading {{version}}…", { version: status.version ?? "" });
		case "downloaded":
			return t("settings.appUpdate.ready", "Version {{version}} is ready. Restart to update.", { version: status.version ?? "" });
		case "installing":
			return t("settings.appUpdate.installing", "Restarting to update…");
		case "error":
			return t("settings.appUpdate.error", "The last update failed: {{message}}", { message: status.message ?? "" });
		default:
			return t("settings.appUpdate.idle", "Tallpine checks GitHub Releases at launch and every 6 hours.");
	}
}

/**
 * Settings > Updates: the desktop app's own updater, which takes signed
 * releases from GitHub. Managed config (MDM) makes these rows read-only.
 */
export function AppUpdateRows() {
	const { t } = useTranslation();
	const status = useAppUpdateStatus();
	const { preferences, update, saving, error } = useAppUpdatePreferences();
	const statusText = useStatusText(status);
	if (!preferences) return null;
	const { settings, locked, pinnedVersion } = preferences;
	const channelOptions = [
		{ value: "stable", label: t("settings.appUpdate.channel.stable", "Stable") },
		{ value: "beta", label: t("settings.appUpdate.channel.beta", "Beta") },
	] satisfies SettingsOption<UpdateChannel>[];
	const failed = status?.state === "error" || Boolean(error);
	return (
		<>
			<div className="flex w-full flex-col">
				<SettingsRow
					className="rounded-none"
					label={t("settings.appUpdate.label", "Automatic updates")}
					description={
						<span className="flex flex-col gap-1">
							{statusText ? <span className={failed ? "text-error" : undefined}>{statusText}</span> : null}
							{pinnedVersion ? <span>{t("settings.appUpdate.pinned", "Held at version {{version}}.", { version: pinnedVersion })}</span> : null}
							{locked ? <AgentPolicyLock source="managed" /> : null}
						</span>
					}
				>
					{status?.state === "downloaded" ? (
						<Button type="button" variant="footer" data-testid="app-update-restart" onClick={() => void tallpineBridge.appUpdates.install()}>
							{t("settings.appUpdate.restart", "Restart to update")}
						</Button>
					) : null}
					<Switch
						aria-label={t("settings.appUpdate.label", "Automatic updates")}
						data-testid="app-update-toggle"
						checked={settings.enabled}
						disabled={locked || saving}
						onCheckedChange={(enabled) => void update({ ...settings, enabled })}
					/>
				</SettingsRow>
				{error ? (
					<p role="alert" className="px-3 pb-2 text-caption leading-4 text-error">
						{error}
					</p>
				) : null}
			</div>
			<SettingsRow label={t("settings.appUpdate.channel.label", "Update channel")}>
				<SettingsOptionMenu
					aria-label={t("settings.appUpdate.channel.label", "Update channel")}
					value={settings.channel}
					options={channelOptions}
					onChange={(channel) => void update({ ...settings, channel })}
					disabled={locked || saving || !settings.enabled}
				/>
			</SettingsRow>
		</>
	);
}
