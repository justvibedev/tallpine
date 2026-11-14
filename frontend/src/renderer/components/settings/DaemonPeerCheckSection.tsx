import { useTranslation } from "react-i18next";
import { useDaemonPeerCheck } from "../../hooks/useDaemonPeerCheck";
import { thisComputerOptions } from "../../lib/this-computer";
import { Switch } from "../ui/switch";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

/**
 * Settings > Security: whether the daemon refuses callers this app did not
 * start (B631), and the switch to turn that off. A build that cannot check
 * callers (unsigned, not macOS) shows why and offers no switch.
 */
export function DaemonPeerCheckSection() {
	const { t } = useTranslation();
	const { check, on, setOn, saving, error } = useDaemonPeerCheck();
	if (!check) return null;
	const status = !check.active
		? t("settings.peerCheck.status.inactive", { reason: check.modeReason })
		: check.mode === "enforce"
			? t("settings.peerCheck.status.enforce", thisComputerOptions())
			: check.mode === "report"
				? t("settings.peerCheck.status.report")
				: t("settings.peerCheck.status.off");
	return (
		<SettingsSection title={t("settings.peerCheck.title")} sectionId="peerCheck" grouped>
			<SettingsRow className="rounded-none" label={t("settings.peerCheck.label", thisComputerOptions())} description={t("settings.peerCheck.description", thisComputerOptions())}>
				{check.active ? <Switch aria-label={t("settings.peerCheck.label", thisComputerOptions())} checked={on} disabled={saving} onCheckedChange={setOn} /> : null}
			</SettingsRow>
			<p data-testid="peer-check-status" className={`px-3 pb-2 text-caption leading-4 ${check.active && !on ? "text-error" : "text-settings-muted"}`}>
				{status}
			</p>
			{error ? (
				<p role="alert" className="px-3 pb-2 text-caption leading-4 text-error">
					{error}
				</p>
			) : null}
		</SettingsSection>
	);
}
