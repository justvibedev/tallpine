import { useTranslation } from "react-i18next";
import { ProjectSettingsSection } from "@tallpine/product-ui";
import type { components } from "../../../api/schema";
import type { ClaudeAutoMemory, ClaudePlugins, ProjectLocalFieldsDraft } from "../../lib/project-local-fields";
import { useSkillsFilter } from "./SkillsFilter";
import { effectNote, roleValues, SKILL_ROLES, SkillRoleHeader, SkillRoleRow, type SkillRole, type SkillRoleValue } from "./SkillRoleRow";

type Plugin = components["schemas"]["ProjectPlugin"];
type Patch = (patch: Partial<ProjectLocalFieldsDraft>) => void;

function perRole(value: (role: SkillRole) => SkillRoleValue): Record<SkillRole, SkillRoleValue> {
	return Object.fromEntries(SKILL_ROLES.map((role) => [role, value(role)])) as Record<SkillRole, SkillRoleValue>;
}

/** The plugin map with one role's switch for one plugin set explicitly. */
export function withPluginSwitch(plugins: ClaudePlugins | undefined, role: SkillRole, id: string, on: boolean): ClaudePlugins {
	return { ...plugins, [role]: { ...plugins?.[role], [id]: on } };
}

/** Claude Code per-role options: auto-memory and the account's plugins. Saved with the page. */
export function InstructionsAndSkillsClaude({ values, plugins, onChange }: { values: ProjectLocalFieldsDraft; plugins: readonly Plugin[] | undefined; onChange: Patch }) {
	const { t } = useTranslation();
	const memory: ClaudeAutoMemory = values.claudeAutoMemory ?? {};
	const overrides: ClaudePlugins = values.claudePlugins ?? {};
	const { shown, field, empty } = useSkillsFilter((plugins ?? []).map((plugin) => ({ ...plugin, name: plugin.id })), "settings.instructions.filterPlugins");
	return <ProjectSettingsSection title={t("settings.instructions.claude")} grouped>
		<p className="px-(--settings-row-inset) pt-2.5 text-xs leading-4 text-settings-muted">{t("settings.instructions.claudeNote")}</p>
		{field}
		<SkillRoleHeader />
		<SkillRoleRow id="claude-auto-memory" name={t("settings.instructions.autoMemory")} description={t("settings.instructions.autoMemoryDescription")}
			values={perRole((role) => ({ on: memory[role] ?? true, inherited: !(role in memory) }))}
			onChange={(role, on) => onChange({ claudeAutoMemory: { ...memory, [role]: on } })} />
		{plugins === undefined
			? <p className="px-(--settings-row-inset) py-2.5 text-sm text-settings-muted">{t("settings.project.skills.loading")}</p>
			: plugins.length === 0
				? <p className="px-(--settings-row-inset) py-2.5 text-sm text-settings-muted">{t("settings.instructions.noPlugins")}</p>
				: <>{empty}{shown.map((plugin) => <SkillRoleRow key={plugin.id} id={plugin.id} name={plugin.id} description={plugin.description}
					detail={plugin.installed ? plugin.path : t("settings.instructions.pluginNotInstalled")}
					note={effectNote(plugin.roles, t)}
					values={roleValues(plugin.roles, (role) => overrides[role]?.[plugin.id], (role) => role !== "reviewer" && plugin.accountEnabled)}
					onChange={(role, on) => onChange({ claudePlugins: withPluginSwitch(overrides, role, plugin.id, on) })} />)}</>}
	</ProjectSettingsSection>;
}
