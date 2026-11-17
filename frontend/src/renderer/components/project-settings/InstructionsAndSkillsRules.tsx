import { useState } from "react";
import { useTranslation } from "react-i18next";
import { ProjectSettingsSection } from "@tallpine/product-ui";
import type { ProjectLocalFieldsDraft } from "../../lib/project-local-fields";
import { SettingsInlineInput, SettingsRow } from "../settings/SettingsRow";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "../ui/tabs";
import { ProjectTextArea } from "./ProjectTextArea";
import { SKILL_ROLES, type SkillRole } from "./SkillRoleRow";

type Patch = (patch: Partial<ProjectLocalFieldsDraft>) => void;
type RulesField = "agentRules" | "orchestratorRules" | "reviewerRules";
type FileField = "agentRulesFile" | "orchestratorRulesFile" | "reviewerRulesFile";

/** The draft fields and copy of each role's rules: inline text plus a repo-relative file. */
const ROLE_RULES = {
	worker: {
		rules: "agentRules", file: "agentRulesFile",
		label: "settings.project.agentRules", description: "settings.project.agentRulesDescription",
		fileLabel: "settings.project.agentRulesFile", fileDescription: "settings.project.agentRulesFileDescription", filePlaceholder: "settings.project.agentRulesFilePlaceholder",
	},
	orchestrator: {
		rules: "orchestratorRules", file: "orchestratorRulesFile",
		label: "settings.project.orchestratorRules", description: "settings.project.orchestratorRulesDescription",
		fileLabel: "settings.project.orchestratorRulesFile", fileDescription: "settings.project.orchestratorRulesFileDescription", filePlaceholder: "settings.project.orchestratorRulesFilePlaceholder",
	},
	reviewer: {
		rules: "reviewerRules", file: "reviewerRulesFile",
		label: "settings.project.reviewerRules", description: "settings.project.reviewerRulesDescription",
		fileLabel: "settings.project.reviewerRulesFile", fileDescription: "settings.project.reviewerRulesFileDescription", filePlaceholder: "settings.project.reviewerRulesFilePlaceholder",
	},
} as const satisfies Record<SkillRole, { rules: RulesField; file: FileField; label: string; description: string; fileLabel: string; fileDescription: string; filePlaceholder: string }>;

/** Standing instructions per session role, one tab each. */
export function InstructionsAndSkillsRules({ values, onChange }: { values: ProjectLocalFieldsDraft; onChange: Patch }) {
	const { t } = useTranslation();
	const [role, setRole] = useState<SkillRole>("worker");
	return <ProjectSettingsSection title={t("settings.instructions.rules")} grouped>
		<Tabs value={role} onValueChange={(value) => setRole(value as SkillRole)}>
			<div className="px-(--settings-row-inset) pt-2.5">
				<TabsList aria-label={t("settings.instructions.rules")}>
					{SKILL_ROLES.map((r) => <TabsTrigger key={r} value={r}>{t(`settings.instructions.role.${r}`)}</TabsTrigger>)}
				</TabsList>
			</div>
			{SKILL_ROLES.map((r) => {
				const f = ROLE_RULES[r];
				return <TabsContent key={r} value={r}>
					<SettingsRow stacked label={t(f.label)} description={t(f.description)}>
						<ProjectTextArea id={`project-${r}-rules`} label={t(f.label)} rows={5} autoGrow placeholder={t("settings.instructions.rulesPlaceholder")} value={values[f.rules] ?? ""} onChange={(text) => onChange({ [f.rules]: text })} />
					</SettingsRow>
					<SettingsRow label={t(f.fileLabel)} description={t(f.fileDescription)}>
						<SettingsInlineInput id={`project-${r}-rules-file`} label={t(f.fileLabel)} value={values[f.file] ?? ""} placeholder={t(f.filePlaceholder)} onChange={(text) => onChange({ [f.file]: text })} />
					</SettingsRow>
				</TabsContent>;
			})}
		</Tabs>
	</ProjectSettingsSection>;
}
