import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import type { components } from "../../api/schema";
import { projectSkillsQueryKey } from "../lib/skills-offer";
import { removeBridgedSkill, setSkillBridged, skillRelativePath } from "../lib/skills-selection";
import { Button } from "./ui/button";

type Skill = components["schemas"]["ProjectSkill"];

/**
 * Per-skill control for a repo skill. A source skill has a checkbox: checked
 * means it is offered to other agents, unchecked is remembered in the project
 * config. A link or copy the bridge made has "Remove link", which deletes only
 * that link/copy and stops offering the skill again.
 */
export function ProjectSkillControl({ projectId, workspace, skill, onRemoved }: { projectId: string; workspace: string; skill: Skill; onRemoved?: (name: string, localOnly: boolean, branch?: string) => void }) {
	const { t } = useTranslation();
	const client = useQueryClient();
	const refresh = () => client.invalidateQueries({ queryKey: projectSkillsQueryKey(projectId) });
	const select = useMutation({ mutationFn: (bridged: boolean) => setSkillBridged(projectId, skill.name, bridged), onSettled: refresh });
	const remove = useMutation({
		mutationFn: async () => {
			const done = await removeBridgedSkill(projectId, skillRelativePath(workspace, skill.path));
			onRemoved?.(skill.name, done.localOnly, done.branch);
			await setSkillBridged(projectId, skill.name, false);
		},
		onSettled: refresh,
	});
	if (skill.scope !== "repo") return null;
	const error = select.error ?? remove.error;
	return <div className="flex flex-col gap-1">
		{skill.removable
			? <div><Button size="sm" variant="ghost" type="button" disabled={remove.isPending} onClick={() => remove.mutate()}>{t("settings.project.skills.removeLink")}</Button></div>
			: <label className="flex items-center gap-2 text-xs leading-4 text-settings-muted">
				<input type="checkbox" checked={!skill.deselected} disabled={select.isPending} onChange={(e) => select.mutate(e.target.checked)} />
				{t("settings.project.skills.bridge", { name: skill.name })}
			</label>}
		{error && <p role="alert" className="text-xs text-error">{error.message}</p>}
	</div>;
}
