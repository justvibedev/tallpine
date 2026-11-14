import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useStartUpdate, useUpdateStatus } from "../../hooks/useUpdate";
import { Button } from "../ui/button";
import {
	Dialog,
	DialogClose,
	DialogContent,
	DialogDescription,
	DialogTitle,
	settingsDialogBodyClass,
	settingsDialogContentClass,
	settingsDialogFooterClass,
	settingsDialogHeaderClass,
} from "../ui/dialog";
import { isWindowsPlatform } from "../../lib/platform";
import { SettingsRow } from "./SettingsRow";
import { WindowsUpdateRow } from "./WindowsUpdateRow";

const shortSha = (sha: string) => sha.slice(0, 7);

/**
 * Settings > Updates: shows a CI-passed build newer than the running one and
 * offers "Update & restart" behind a confirmation. The update runs as a
 * detached job, so the status survives the app and daemon restarting.
 */
export function UpdateRow() {
	// Windows has no daemon update service (it reports unavailable): link to the
	// installer releases instead.
	return isWindowsPlatform() ? <WindowsUpdateRow /> : <DaemonUpdateRow />;
}

function DaemonUpdateRow() {
	const { t } = useTranslation();
	const { data, isError } = useUpdateStatus();
	const { start, starting, error } = useStartUpdate();
	const [confirming, setConfirming] = useState(false);
	if (!data) {
		return isError ? <SettingsRow label={t("settings.update.label", "Updates")} description={t("settings.update.unavailable", "Update status is unavailable.")} /> : null;
	}

	const busy = data.running || starting;
	const last = data.last;
	let description: string;
	if (busy) {
		description = t("settings.update.running", "Updating. The app restarts when it finishes.");
	} else if (data.updateAvailable) {
		description = t("settings.update.available", "Update available: {{sha}} (CI passed)", { sha: shortSha(data.candidate) });
	} else if (!data.candidate) {
		description = t("settings.update.noCandidate", "No CI-passed build found.");
	} else {
		description = t("settings.update.upToDate", "Up to date ({{sha}})", { sha: shortSha(data.installedCommit) });
	}
	const failed = !busy && last && (last.state === "failed" || last.state === "rolled_back");
	const succeeded = !busy && last?.state === "succeeded";

	return (
		<>
			<SettingsRow label={t("settings.update.label", "Updates")} description={description}>
				<Button type="button" variant="footer" data-testid="update-restart" disabled={!data.updateAvailable || busy} onClick={() => setConfirming(true)}>
					{busy ? t("settings.update.updating", "Updating…") : t("settings.update.button", "Update & restart")}
				</Button>
			</SettingsRow>
			{failed ? (
				<p className="px-4 pb-2 text-sm text-destructive" role="alert" data-testid="update-failed">
					{last.state === "rolled_back"
						? t("settings.update.rolledBack", "The last update to {{sha}} was rolled back: {{message}}", { sha: shortSha(last.commit), message: last.message })
						: t("settings.update.failed", "The last update to {{sha}} failed: {{message}}", { sha: shortSha(last.commit), message: last.message })}
				</p>
			) : null}
			{succeeded ? (
				<p className="px-4 pb-2 text-sm text-settings-muted" role="status" data-testid="update-succeeded">
					{t("settings.update.succeeded", "Updated to {{sha}}.", { sha: shortSha(last.commit) })}
				</p>
			) : null}
			{confirming ? (
				<Dialog open onOpenChange={(next) => { if (!next) setConfirming(false); }}>
					<DialogContent className={settingsDialogContentClass} showCloseButton={false}>
						<div className={settingsDialogHeaderClass}>
							<DialogTitle className="settings-dialog-title">{t("settings.update.confirmTitle", "Update & restart?")}</DialogTitle>
							<DialogDescription>
								{t("settings.update.confirmBody", "Tallpine quits and relaunches on {{sha}}. Running agents pause and resume on their own, and unsent chat drafts are discarded. If the new version does not start, it rolls back automatically.", { sha: shortSha(data.candidate) })}
							</DialogDescription>
						</div>
						<div className={settingsDialogBodyClass}>
							{error ? <p className="text-sm text-destructive" role="alert">{error}</p> : null}
						</div>
						<div className={settingsDialogFooterClass}>
							<DialogClose asChild>
								<Button type="button" variant="ghost">{t("common.cancel", "Cancel")}</Button>
							</DialogClose>
							<Button
								type="button"
								data-testid="update-confirm"
								disabled={starting}
								onClick={() => void start(data.candidate).then(() => setConfirming(false), () => undefined)}
							>
								{t("settings.update.confirm", "Update & restart")}
							</Button>
						</div>
					</DialogContent>
				</Dialog>
			) : null}
		</>
	);
}
