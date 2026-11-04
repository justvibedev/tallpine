import { useTranslation } from "react-i18next";
import { SESSION_MODES, type ProjectLocalFieldsDraft } from "../../lib/project-local-fields";
import { SettingsOptionMenu } from "../settings/SettingsOptionMenu";
import { SettingsRow } from "../settings/SettingsRow";

/** Sessions: the interface new worker sessions open in. */
export function ProjectSessionModeRow({ values, onChange }: { values: ProjectLocalFieldsDraft; onChange: (patch: Partial<ProjectLocalFieldsDraft>) => void }) {
	const { t } = useTranslation();
	const options = [{ value: "", label: t("settings.project.sessionMode.default") }, ...SESSION_MODES.map((m) => ({ value: m, label: t(`settings.project.sessionMode.${m}`) }))];
	return (
		<SettingsRow label={t("settings.project.defaultSessionMode")} description={t("settings.project.defaultSessionModeDescription")}>
			<SettingsOptionMenu aria-label={t("settings.project.defaultSessionMode")} value={values.defaultSessionMode ?? ""} options={options} triggerClassName="w-fit" onChange={(defaultSessionMode) => onChange({ defaultSessionMode })} />
		</SettingsRow>
	);
}
