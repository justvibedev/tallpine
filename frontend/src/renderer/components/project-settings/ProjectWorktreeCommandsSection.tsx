import { useTranslation } from "react-i18next";
import type { ProjectLocalFieldsDraft } from "../../lib/project-local-fields";
import { ProjectToggleRow } from "../settings/ProjectAgentRoleControls";
import { SettingsRow } from "../settings/SettingsRow";
import { ProjectTextArea } from "./ProjectTextArea";

type Patch = (patch: Partial<ProjectLocalFieldsDraft>) => void;

/** Repository & worktrees: what each session workspace links and how it is seeded. */
export function ProjectWorktreeCommandsSection({ values, onChange }: { values: ProjectLocalFieldsDraft; onChange: Patch }) {
	const { t } = useTranslation();
	return (
		<>
			<SettingsRow label={t("settings.project.symlinks")} description={t("settings.project.symlinksDescription")}>
				<ProjectTextArea id="project-symlinks" label={t("settings.project.symlinks")} rows={3} value={values.symlinks ?? ""} placeholder={t("settings.project.symlinksPlaceholder")} onChange={(symlinks) => onChange({ symlinks })} />
			</SettingsRow>
			<ProjectToggleRow id="project-clone-worktree-include" label={t("settings.project.cloneWorktreeInclude")} description={t("settings.project.cloneWorktreeIncludeDescription")} checked={values.cloneWorktreeInclude ?? false} onCheckedChange={(cloneWorktreeInclude) => onChange({ cloneWorktreeInclude })} />
		</>
	);
}
