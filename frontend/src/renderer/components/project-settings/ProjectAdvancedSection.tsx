import { useTranslation } from "react-i18next";
import type { ProjectLocalFieldsDraft } from "../../lib/project-local-fields";
import { SettingsInlineInput, SettingsRow } from "../settings/SettingsRow";
import { ProjectTextArea } from "./ProjectTextArea";

type Patch = (patch: Partial<ProjectLocalFieldsDraft>) => void;

/** Advanced: standing instructions for worker and orchestrator sessions. */
export function ProjectAdvancedSection({ values, onChange }: { values: ProjectLocalFieldsDraft; onChange: Patch }) {
	const { t } = useTranslation();
	return (
		<>
			<SettingsRow label={t("settings.project.agentRules")} description={t("settings.project.agentRulesDescription")}>
				<ProjectTextArea id="project-agent-rules" label={t("settings.project.agentRules")} rows={5} value={values.agentRules ?? ""} onChange={(agentRules) => onChange({ agentRules })} />
			</SettingsRow>
			<SettingsRow label={t("settings.project.agentRulesFile")} description={t("settings.project.agentRulesFileDescription")}>
				<SettingsInlineInput id="project-agent-rules-file" label={t("settings.project.agentRulesFile")} value={values.agentRulesFile ?? ""} placeholder={t("settings.project.agentRulesFilePlaceholder")} onChange={(agentRulesFile) => onChange({ agentRulesFile })} />
			</SettingsRow>
			<SettingsRow label={t("settings.project.orchestratorRules")} description={t("settings.project.orchestratorRulesDescription")}>
				<ProjectTextArea id="project-orchestrator-rules" label={t("settings.project.orchestratorRules")} rows={5} value={values.orchestratorRules ?? ""} onChange={(orchestratorRules) => onChange({ orchestratorRules })} />
			</SettingsRow>
		</>
	);
}
