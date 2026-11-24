import { useTranslation } from "react-i18next";
import { BROWSER_BACKEND_CHROME } from "../../../shared/chrome-mode";
import type { ProjectLocalFieldsDraft } from "../../lib/project-local-fields";
import { SettingsOptionMenu } from "../settings/SettingsOptionMenu";
import { SettingsRow } from "../settings/SettingsRow";

/** Browser: whether agents browse in the in-app browser or in Google Chrome (Real Chrome mode, B849). */
export function ProjectBrowserBackendRow({ values, onChange }: { values: ProjectLocalFieldsDraft; onChange: (patch: Partial<ProjectLocalFieldsDraft>) => void }) {
	const { t } = useTranslation();
	const options = [
		{ value: "", label: t("settings.project.browserBackend.electron") },
		{ value: BROWSER_BACKEND_CHROME, label: t("settings.project.browserBackend.chrome") },
	];
	return (
		<SettingsRow label={t("settings.project.browserBackend")} description={t("settings.project.browserBackendDescription")}>
			<SettingsOptionMenu aria-label={t("settings.project.browserBackend")} value={values.browserBackend === BROWSER_BACKEND_CHROME ? BROWSER_BACKEND_CHROME : ""} options={options} triggerClassName="w-fit" onChange={(browserBackend) => onChange({ browserBackend })} />
		</SettingsRow>
	);
}
