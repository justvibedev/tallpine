import { useTranslation } from "react-i18next";
import { ProjectSettingsSection } from "@tallpine/product-ui";
import type { components } from "../../../api/schema";
import type { ProjectLocalFieldsDraft, SkillRoles } from "../../lib/project-local-fields";
import { useSkillsFilter, sharedNote } from "./SkillsFilter";
import { accessValues, effectNote, rowNote, roleValues, SkillRoleHeader, SkillRoleRow, type SkillRole } from "./SkillRoleRow";

type Skill = components["schemas"]["ProjectSkill"];
type Patch = (patch: Partial<ProjectLocalFieldsDraft>) => void;

/** The skill switch map with one role's switch for one skill set explicitly. */
export function withSkillSwitch(roles: SkillRoles | undefined, role: SkillRole | string, name: string, on: boolean): SkillRoles {
	return { ...roles, [role]: { ...roles?.[role], [name]: on } };
}

/** The switch map without any role's switch for name: every role inherits again. */
export function withoutSkillSwitches(roles: SkillRoles | undefined, name: string): SkillRoles {
	const out: SkillRoles = {};
	for (const [role, skills] of Object.entries(roles ?? {})) {
		const rest = Object.fromEntries(Object.entries(skills ?? {}).filter(([skill]) => skill !== name));
		if (Object.keys(rest).length > 0) out[role] = rest;
	}
	return out;
}

/**
 * One switch per role for every repo, account and custom skill (by name, as
 * the agents see them) and each default-branch reviewer skill (reviewer
 * only). Tallpine's own skills are always on and not listed.
 * Saved with the page; sessions pick changes up at their next launch.
 */
export function InstructionsAndSkillsSkills({ values, skills, onChange }: { values: ProjectLocalFieldsDraft; skills: readonly Skill[] | undefined; onChange: Patch }) {
	const { t } = useTranslation();
	const overrides: SkillRoles = values.skillRoles ?? {};
	const seen = new Set<string>();
	const rows = (skills ?? []).filter((s) => s.scope !== "tallpine" && !seen.has(s.name) && seen.add(s.name));
	const note = sharedNote(rows.map((s) => effectNote(s.roles, t)));
	const { shown, field, empty } = useSkillsFilter(rows);
	return <ProjectSettingsSection title={t("settings.instructions.skills")} grouped>
		<p className="px-(--settings-row-inset) pt-2.5 text-xs leading-4 text-settings-muted">{t("settings.instructions.skillsNote")}</p>
		{note && <p className="px-(--settings-row-inset) pt-1 text-xs leading-4 text-settings-muted" data-testid="skills-effect-note">{note}</p>}
		{field}
		{skills === undefined
			? <p className="px-(--settings-row-inset) py-2.5 text-sm text-settings-muted">{t("settings.project.skills.loading")}</p>
			: rows.length === 0
				? <p className="px-(--settings-row-inset) py-2.5 text-sm text-settings-muted">{t("settings.project.skills.empty")}</p>
				: <><SkillRoleHeader withAccess />{empty}{shown.map((skill) => <SkillRoleRow key={skill.name} id={`skill-${skill.name}`} name={skill.name} description={skill.description} tooltip={skill.path}
					note={rowNote(skill.roles, note, t)}
					disabledRoles={skill.scope === "reviewer-repo" ? { worker: t("settings.instructions.reviewerOnly"), orchestrator: t("settings.instructions.reviewerOnly") } : undefined}
					values={roleValues(skill.roles, (role) => overrides[role]?.[skill.name])}
					access={accessValues(skill.access, (key) => overrides[key]?.[skill.name])}
					onAccessChange={(key, on) => onChange({ skillRoles: withSkillSwitch(overrides, key, skill.name, on) })}
					onChange={(role, on) => onChange({ skillRoles: withSkillSwitch(overrides, role, skill.name, on) })}
					onReset={() => onChange({ skillRoles: withoutSkillSwitches(overrides, skill.name) })} />)}</>}
	</ProjectSettingsSection>;
}
