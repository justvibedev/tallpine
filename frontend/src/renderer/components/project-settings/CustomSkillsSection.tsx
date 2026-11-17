import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { ProjectSettingsSection } from "@tallpine/product-ui";
import type { components } from "../../../api/schema";
import { createCustomSkill, customSkillsQueryKeys, customSkillTemplate, deleteCustomSkill, fetchCustomSkill, importCustomSkill, updateCustomSkill, type CustomSkillOwner } from "../../lib/custom-skills";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { ProjectTextArea } from "./ProjectTextArea";

type Skill = components["schemas"]["ProjectSkill"];

/** An example skill name: a path segment, the same in every language. */
const EXAMPLE_NAME = "my-skill";

/** What the editor is doing: nothing, creating, importing, or editing one skill. */
type Mode = { kind: "idle" } | { kind: "new" } | { kind: "import" } | { kind: "edit"; name: string };

/**
 * The custom skills of a project, or with a null projectId the app-wide ones
 * every project sees: SKILL.md files the owner keeps in Tallpine, not in the
 * repository. New, import (text-only copy of a local skill folder), edit and
 * delete; which roles get each is switched on the skills list above.
 */
export function CustomSkillsSection({ projectId, skills }: { projectId: CustomSkillOwner; skills: readonly Skill[] }) {
	const { t } = useTranslation();
	const client = useQueryClient();
	const [mode, setMode] = useState<Mode>({ kind: "idle" });
	const [name, setName] = useState("");
	const [text, setText] = useState("");
	const [skipped, setSkipped] = useState<string[]>([]);
	const [confirm, setConfirm] = useState<string | null>(null);
	const refresh = () => {
		for (const queryKey of customSkillsQueryKeys(projectId)) void client.invalidateQueries({ queryKey });
	};
	const done = () => {
		setMode({ kind: "idle" });
		setName("");
		setText("");
		refresh();
	};
	const save = useMutation({
		mutationFn: async () => {
			if (mode.kind === "new") return createCustomSkill(projectId, name.trim(), text);
			if (mode.kind === "import") return importCustomSkill(projectId, name.trim(), text.trim());
			if (mode.kind === "edit") return updateCustomSkill(projectId, mode.name, text);
			return undefined;
		},
		onSuccess: (skill) => {
			setSkipped(skill?.skipped ?? []);
			done();
		},
	});
	const edit = useMutation({
		mutationFn: (skillName: string) => fetchCustomSkill(projectId, skillName),
		onSuccess: (skill) => {
			setMode({ kind: "edit", name: skill.name });
			setText(skill.content);
			setSkipped([]);
		},
	});
	const remove = useMutation({
		mutationFn: (skillName: string) => deleteCustomSkill(projectId, skillName),
		onSettled: () => {
			setConfirm(null);
			refresh();
		},
	});
	const start = (kind: "new" | "import") => {
		save.reset();
		setSkipped([]);
		setMode({ kind });
		setName("");
		setText(kind === "new" ? customSkillTemplate(EXAMPLE_NAME) : "");
	};
	const editing = mode.kind !== "idle";
	return <ProjectSettingsSection title={t(projectId === null ? "settings.instructions.app.custom" : "settings.project.skills.group.custom")} grouped>
		<div className="settings-row-bar flex-col items-stretch gap-2 py-2.5">
			<div className="flex items-baseline justify-between gap-3">
				<p className="text-xs leading-4 text-settings-muted">{t(projectId === null ? "settings.instructions.app.customNote" : "settings.project.skills.custom.note")}</p>
				<span className="flex shrink-0 gap-1">
					<Button size="sm" variant="ghost" type="button" disabled={editing} onClick={() => start("new")}>{t("settings.project.skills.custom.new")}</Button>
					<Button size="sm" variant="ghost" type="button" disabled={editing} onClick={() => start("import")}>{t("settings.project.skills.custom.import")}</Button>
				</span>
			</div>
			{editing && <form className="flex flex-col gap-2" aria-label={t(`settings.project.skills.custom.${mode.kind}`)} onSubmit={(e) => { e.preventDefault(); save.mutate(); }}>
				{mode.kind === "edit"
					? <span className="text-sm leading-5 text-settings-label">{mode.name}</span>
					: <Input aria-label={t("settings.project.skills.custom.name")} placeholder={EXAMPLE_NAME} value={name} onChange={(e) => {
						const next = e.target.value;
						// An untouched template follows the typed name.
						if (mode.kind === "new" && text === customSkillTemplate(name.trim() || EXAMPLE_NAME)) setText(customSkillTemplate(next.trim() || EXAMPLE_NAME));
						setName(next);
					}} />}
				{mode.kind === "import"
					? <Input aria-label={t("settings.project.skills.custom.source")} placeholder={t("settings.project.skills.custom.source")} value={text} onChange={(e) => setText(e.target.value)} />
					: <ProjectTextArea id="custom-skill-content" label={t("settings.project.skills.custom.content")} rows={10} value={text} onChange={setText} />}
				<span className="flex justify-end gap-1">
					<Button size="sm" variant="ghost" type="button" onClick={() => setMode({ kind: "idle" })}>{t("settings.project.skills.custom.cancel")}</Button>
					<Button size="sm" type="submit" disabled={save.isPending || (mode.kind !== "edit" && name.trim() === "") || text.trim() === ""}>{t("settings.project.skills.custom.save")}</Button>
				</span>
				{save.isError && <p role="alert" className="text-xs text-error">{save.error.message}</p>}
			</form>}
			{skipped.length > 0 && <p role="status" className="text-xs leading-4 text-settings-muted">{t("settings.project.skills.custom.skipped", { files: skipped.join("; ") })}</p>}
			{(edit.isError || remove.isError) && <p role="alert" className="text-xs text-error">{(edit.error ?? remove.error)?.message}</p>}
		</div>
		{skills.length === 0
			? <p className="px-(--settings-row-inset) py-2.5 text-sm text-settings-muted">{t("settings.project.skills.empty")}</p>
			: skills.map((skill) => <div className="settings-row-bar flex-col items-stretch gap-1 py-2.5" key={skill.name} data-testid={`custom-skill-${skill.name}`}>
				<div className="flex items-baseline justify-between gap-3">
					<span className="text-sm leading-5 text-settings-label">{skill.name}</span>
					<span className="flex shrink-0 gap-1">
						<Button size="sm" variant="ghost" type="button" disabled={editing || edit.isPending} onClick={() => edit.mutate(skill.name)}>{t("settings.project.skills.custom.edit")}</Button>
						{confirm === skill.name
							? <Button size="sm" variant="ghost" type="button" disabled={remove.isPending} onClick={() => remove.mutate(skill.name)}>{t("settings.project.skills.custom.confirmDelete")}</Button>
							: <Button size="sm" variant="ghost" type="button" disabled={editing} onClick={() => setConfirm(skill.name)}>{t("settings.project.skills.custom.delete")}</Button>}
					</span>
				</div>
				{skill.description && <p className="line-clamp-2 text-xs leading-4 text-settings-muted">{skill.description}</p>}
				<p className="truncate font-mono text-xs leading-4 text-settings-muted" title={skill.path}>{skill.path}</p>
			</div>)}
	</ProjectSettingsSection>;
}
