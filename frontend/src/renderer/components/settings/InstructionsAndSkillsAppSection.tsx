import { useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { ProjectSettingsSection } from "@tallpine/product-ui";
import { useAppInstructions, type AppInstructions } from "../../hooks/useAppInstructions";
import { appSkillsQueryKey, fetchAppSkills } from "../../lib/custom-skills";
import { CustomSkillsSection } from "../project-settings/CustomSkillsSection";
import { withoutSkillSwitches, withSkillSwitch } from "../project-settings/InstructionsAndSkillsSkills";
import { ProjectTextArea } from "../project-settings/ProjectTextArea";
import { useSkillsFilter, sharedNote } from "../project-settings/SkillsFilter";
import { accessValues, effectNote, rowNote, roleValues, SKILL_ROLES, SkillRoleHeader, SkillRoleRow, type SkillRole } from "../project-settings/SkillRoleRow";
import { Button } from "../ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "../ui/tabs";
import { SettingsRow } from "./SettingsRow";

type RulesField = "workerRules" | "orchestratorRules" | "reviewerRules";
const RULES_FIELD = { worker: "workerRules", orchestrator: "orchestratorRules", reviewer: "reviewerRules" } as const satisfies Record<SkillRole, RulesField>;

/** One role's app-wide rules: a draft of the saved text and its own Save. */
function AppRulesTab({ role, saved, saving, onSave }: { role: SkillRole; saved: string; saving: boolean; onSave: (text: string) => void }) {
	const { t } = useTranslation();
	const [draft, setDraft] = useState(saved);
	useEffect(() => setDraft(saved), [saved]);
	const label = t(`settings.instructions.app.rules.${role}`);
	return <>
		<SettingsRow stacked label={label} description={t(`settings.instructions.app.rules.${role}Description`)}>
			<ProjectTextArea id={`app-${role}-rules`} label={label} rows={5} autoGrow placeholder={t("settings.instructions.rulesPlaceholder")} value={draft} onChange={setDraft} />
		</SettingsRow>
		<div className="flex justify-end px-(--settings-row-inset) pb-2.5">
			<Button size="sm" type="button" disabled={saving || draft === saved} onClick={() => onSave(draft)}>{t("settings.instructions.app.save")}</Button>
		</div>
	</>;
}

/**
 * Settings > Instructions and skills: the app-wide rules of each role, placed
 * before every project's own; the app skill switches every project inherits
 * (a project's own switch wins); and the custom skills every project sees.
 * Only a person can change them.
 */
export function InstructionsAndSkillsAppSection() {
	const { t } = useTranslation();
	const { instructions, loadError, save, saving, saveError } = useAppInstructions();
	const skills = useQuery({ queryKey: appSkillsQueryKey, queryFn: fetchAppSkills });
	const [role, setRole] = useState<SkillRole>("worker");
	const rows = skills.data?.skills ?? [];
	const note = sharedNote(rows.map((s) => effectNote(s.roles, t)));
	const { shown, field, empty } = useSkillsFilter(rows);
	if (loadError) return <p role="alert" className="px-(--settings-row-inset) text-sm text-error">{loadError}</p>;
	if (!instructions) return <p className="px-(--settings-row-inset) text-sm text-settings-muted">{t("settings.project.skills.loading")}</p>;
	const overrides = instructions.skillRoles;
	const saveRoles = (skillRoles: AppInstructions["skillRoles"]) => save({ skillRoles });
	return <div className="flex min-h-full flex-col gap-(--size-settings-section-inner-gap)">
		<ProjectSettingsSection title={t("settings.instructions.rules")} grouped>
			<p className="px-(--settings-row-inset) pt-2.5 text-xs leading-4 text-settings-muted">{t("settings.instructions.app.rulesNote")}</p>
			<Tabs value={role} onValueChange={(value) => setRole(value as SkillRole)}>
				<div className="px-(--settings-row-inset) pt-2.5">
					<TabsList aria-label={t("settings.instructions.rules")}>
						{SKILL_ROLES.map((r) => <TabsTrigger key={r} value={r}>{t(`settings.instructions.role.${r}`)}</TabsTrigger>)}
					</TabsList>
				</div>
				{SKILL_ROLES.map((r) => <TabsContent key={r} value={r}>
					<AppRulesTab role={r} saved={instructions[RULES_FIELD[r]]} saving={saving} onSave={(text) => save({ [RULES_FIELD[r]]: text })} />
				</TabsContent>)}
			</Tabs>
		</ProjectSettingsSection>
		<ProjectSettingsSection title={t("settings.instructions.skills")} grouped>
			<p className="px-(--settings-row-inset) pt-2.5 text-xs leading-4 text-settings-muted">{t("settings.instructions.app.skillsNote")}</p>
			{note && <p className="px-(--settings-row-inset) pt-1 text-xs leading-4 text-settings-muted" data-testid="skills-effect-note">{note}</p>}
			{field}
			{skills.isError
				? <p role="alert" className="px-(--settings-row-inset) py-2.5 text-sm text-error">{skills.error.message}</p>
				: !skills.data
					? <p className="px-(--settings-row-inset) py-2.5 text-sm text-settings-muted">{t("settings.project.skills.loading")}</p>
					: rows.length === 0
						? <p className="px-(--settings-row-inset) py-2.5 text-sm text-settings-muted">{t("settings.project.skills.empty")}</p>
						: <><SkillRoleHeader withAccess />{empty}{shown.map((skill) => <SkillRoleRow key={skill.name} id={`app-skill-${skill.name}`} name={skill.name} description={skill.description} tooltip={skill.path}
							note={rowNote(skill.roles, note, t)}
							values={roleValues(skill.roles, (r) => overrides[r]?.[skill.name])}
							access={accessValues(skill.access, (key) => overrides[key]?.[skill.name])}
							onAccessChange={(key, on) => saveRoles(withSkillSwitch(overrides, key, skill.name, on))}
							onChange={(r, on) => saveRoles(withSkillSwitch(overrides, r, skill.name, on))}
							onReset={() => saveRoles(withoutSkillSwitches(overrides, skill.name))} />)}</>}
		</ProjectSettingsSection>
		<CustomSkillsSection projectId={null} skills={rows.filter((s) => s.convention === "custom-app")} />
		{saveError && <p role="alert" className="px-(--settings-row-inset) text-xs text-error">{saveError}</p>}
	</div>;
}
