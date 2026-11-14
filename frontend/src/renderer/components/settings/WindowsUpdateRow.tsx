import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { WINDOWS_RELEASES_URL } from "../../../shared/brand";
import { tallpineBridge } from "../../lib/bridge";
import { Button } from "../ui/button";
import { SettingsRow } from "./SettingsRow";

/**
 * Settings > Updates on Windows: the beta has no in-app updater, so the row
 * shows the running version and links to the installer releases page.
 */
export function WindowsUpdateRow() {
	const { t } = useTranslation();
	const { data: version } = useQuery({ queryKey: ["app-version"], queryFn: () => tallpineBridge.app.getVersion(), staleTime: Infinity });
	return (
		<SettingsRow
			label={t("settings.update.label", "Updates")}
			description={version ? t("settings.update.windowsVersion", "Tallpine {{version}}. Install a newer build to update.", { version }) : undefined}
		>
			<Button type="button" variant="footer" data-testid="update-windows-download" onClick={() => void tallpineBridge.app.openExternal(WINDOWS_RELEASES_URL)}>
				{t("settings.update.windowsDownload", "Download the latest Windows installer")}
			</Button>
		</SettingsRow>
	);
}
