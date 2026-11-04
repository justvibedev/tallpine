import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { ProjectSettingsSection } from "@tallpine/product-ui";
import type { components } from "../../api/schema";
import { applyProjectSkillOffer, fetchProjectSkills, offerItemLine, projectSkillsQueryKey, setSkillsOfferOff } from "../lib/skills-offer";
import { cn } from "../lib/utils";
import { ProjectSkillControl } from "./ProjectSkillRow";
import { SkillsCommitPanel } from "./SkillsCommitPanel";
import { Button } from "./ui/button";

type Skills = components["schemas"]["ProjectSkillsResponse"];
type Offer = components["schemas"]["ProjectSkillOffer"];
type Status = components["schemas"]["ProjectSkillHarness"]["status"];

export { projectSkillsQueryKey };

// Only these two are checked; the rest are listed once as "not verified".
const CHECKED = [["claude-code", "Claude Code"], ["codex", "Codex"]] as const;

function StatusChip({ name, status }: { name: string; status: Status | undefined }) {
	const { t } = useTranslation();
	return <span className={cn("rounded-md px-1.5 py-0.5 text-xs leading-4", status === "native" ? "bg-settings-menu-selected text-settings-title" : "text-settings-muted")}>
		{name}: {t(`settings.project.skills.status.${status ?? "none"}`)}
	</span>;
}

/** i18n key for the line shown after an offer is applied, by what was created. */
export function appliedMessageKey(created: readonly { mode: string }[] | undefined): "settings.project.skills.offer.createdCopy" | "settings.project.skills.offer.created" | "settings.project.skills.offer.createdFile" {
	if (created?.some((c) => c.mode === "copy")) return "settings.project.skills.offer.createdCopy";
	if (created?.some((c) => c.mode === "symlink")) return "settings.project.skills.offer.created";
	return "settings.project.skills.offer.createdFile";
}

export function useApplyOffer(projectId: string, offer: Offer) {
	const client = useQueryClient();
	return useMutation({
		mutationFn: () => applyProjectSkillOffer(projectId, offer.id),
		onSettled: () => client.invalidateQueries({ queryKey: projectSkillsQueryKey(projectId) }),
	});
}

function OfferRow({ projectId, offer }: { projectId: string; offer: Offer }) {
	const { t } = useTranslation();
	const apply = useApplyOffer(projectId, offer);
	return <div className="settings-row-bar flex-col items-stretch gap-2 py-2.5">
		<div className="flex items-center justify-between gap-3">
			<span className="text-sm leading-5 text-settings-label">{t(`settings.project.skills.offer.${offer.id}`)}</span>
			<Button size="sm" variant="ghost" type="button" disabled={apply.isPending || apply.isSuccess} onClick={() => apply.mutate()}>{t("settings.project.skills.offer.create")}</Button>
		</div>
		<ul aria-label={t("settings.project.skills.offer.willCreate")} className="flex flex-col gap-0.5 font-mono text-xs leading-4 text-settings-muted">
			{offer.items.map((item) => <li key={item.path}>{offerItemLine(item, t)}</li>)}
		</ul>
		{apply.isSuccess && <p role="status" className="text-xs text-settings-muted">{t(appliedMessageKey(apply.data?.created))}</p>}
		{apply.isError && <p role="alert" className="text-xs text-error">{apply.error.message}</p>}
	</div>;
}

export function ProjectSkillsSettings({ projectId }: { projectId: string }) {
	const { t } = useTranslation();
	const query = useQuery({
		queryKey: projectSkillsQueryKey(projectId),
		queryFn: (): Promise<Skills> => fetchProjectSkills(projectId),
	});
	const client = useQueryClient();
	// Once a commit starts, keep its panel (result, restart) after the refetch empties the lists.
	const [commitUsed, setCommitUsed] = useState(false);
	// Removed links that are still on the branch sessions start from: the row is gone, so say so here.
	const [removed, setRemoved] = useState<{ name: string; branch: string }[]>([]);
	const toggle = useMutation({
		mutationFn: (enabled: boolean) => setSkillsOfferOff(projectId, enabled),
		onSettled: () => client.invalidateQueries({ queryKey: projectSkillsQueryKey(projectId) }),
	});
	if (query.isLoading) return <p className="px-(--settings-row-inset) text-sm text-settings-muted">{t("settings.project.skills.loading")}</p>;
	if (query.isError || !query.data) return <p role="alert" className="px-(--settings-row-inset) text-sm text-error">{query.error instanceof Error ? query.error.message : t("settings.project.skills.loadFailed")}</p>;
	const { skills, offers, instructions, collisions, harnesses, offersEnabled, pendingCommit, commitTarget } = query.data;
	// Create and commit: create each offer in turn, stop at the first failure
	// (no partial commit), then commit what was created plus the listed
	// uncommitted files, nothing else.
	const createForCommit = async (): Promise<string[]> => {
		setCommitUsed(true);
		const created: string[] = [];
		for (const offer of offers) {
			const data = await applyProjectSkillOffer(projectId, offer.id);
			created.push(...(data?.created ?? []).map((c) => c.path));
		}
		return [...new Set([...created, ...pendingCommit])];
	};
	const commitPreview = [...new Set([...offers.flatMap((o) => o.items.map((i) => i.path)), ...pendingCommit])];
	const groups = [
		{ id: "project", title: t("settings.project.skills.group.project"), items: skills.filter((s) => s.scope === "repo") },
		{ id: "user", title: t("settings.project.skills.group.user"), items: skills.filter((s) => s.scope !== "repo") },
	];
	const used = CHECKED.filter(([id]) => harnesses.includes(id));
	return <div className="flex min-h-full flex-col gap-(--size-settings-section-inner-gap)">
		{used.length > 0 && <ProjectSettingsSection title={t("settings.project.skills.instructions")} grouped>
			{used.map(([id, name]) => <div className="settings-row-bar" key={id}>
				<span className="text-sm leading-5 text-settings-label">{name}</span>
				<StatusChip name={id === "claude-code" ? "CLAUDE.md" : "AGENTS.md"} status={instructions.find((i) => i.harness === id)?.status} />
			</div>)}
		</ProjectSettingsSection>}
		{removed.map((r) => <p key={r.name} role="status" className="px-(--settings-row-inset) text-xs leading-4 text-settings-muted">{t("settings.project.skills.removedLocal", { name: r.name, branch: r.branch })}</p>)}
		{pendingCommit.length > 0 && <p role="status" className="px-(--settings-row-inset) font-mono text-xs leading-4 text-settings-muted">{t("settings.project.skills.pending", { paths: pendingCommit.join(", ") })}</p>}
		{offers.length > 0 && <ProjectSettingsSection title={t("settings.project.skills.offers")} grouped>
			{offers.map((offer) => <OfferRow key={offer.id} projectId={projectId} offer={offer} />)}
			<p className="px-(--settings-row-inset) py-2.5 text-xs leading-4 text-settings-muted">{t("settings.project.skills.offer.note")}</p>
		</ProjectSettingsSection>}
		{(commitPreview.length > 0 || commitUsed) && <div className="px-(--settings-row-inset)">
			<SkillsCommitPanel projectId={projectId} target={commitTarget} preview={commitPreview} create={offers.length > 0} collect={createForCommit} muted="text-settings-muted" />
		</div>}
		<ProjectSettingsSection title={t("settings.project.skills.auto")} grouped>
			<div className="settings-row-bar">
				<span className="text-sm leading-5 text-settings-label">{t(offersEnabled ? "settings.project.skills.autoOn" : "settings.project.skills.autoOff")}</span>
				<Button size="sm" variant="ghost" type="button" disabled={toggle.isPending} onClick={() => toggle.mutate(offersEnabled)}>{t(offersEnabled ? "settings.project.skills.turnOff" : "settings.project.skills.turnOn")}</Button>
			</div>
			{toggle.isError && <p role="alert" className="px-(--settings-row-inset) pb-2 text-xs text-error">{toggle.error.message}</p>}
		</ProjectSettingsSection>
		{groups.map((group) => <ProjectSettingsSection key={group.id} title={group.title} grouped>
			{group.items.length === 0 ? <p className="px-(--settings-row-inset) py-2.5 text-sm text-settings-muted">{t("settings.project.skills.empty")}</p> : group.items.map((skill) => <div className="settings-row-bar flex-col items-stretch gap-1 py-2.5" key={`${skill.convention}:${skill.scope}:${skill.name}`}>
				<div className="flex items-baseline justify-between gap-3">
					<span className="text-sm leading-5 text-settings-label">{skill.name}</span>
					<span className="flex shrink-0 flex-wrap justify-end gap-1">
						{CHECKED.map(([id, name]) => <StatusChip key={id} name={name} status={skill.harnesses.find((h) => h.harness === id)?.status} />)}
					</span>
				</div>
				{skill.description && <p className="line-clamp-2 text-xs leading-4 text-settings-muted">{skill.description}</p>}
				<p className="truncate font-mono text-xs leading-4 text-settings-muted" title={skill.path}>{skill.path}</p>
				<ProjectSkillControl projectId={projectId} workspace={query.data.workspace} skill={skill} onRemoved={(name, localOnly, branch) => setRemoved((r) => localOnly ? [...r.filter((x) => x.name !== name), { name, branch: branch ?? "" }] : r)} />
			</div>)}
		</ProjectSettingsSection>)}
		{collisions.length > 0 && <ul className="flex flex-col gap-0.5 px-(--settings-row-inset) text-xs leading-4 text-settings-muted">
			{collisions.map((c) => <li key={`${c.winner}:${c.loser}`}>{t("settings.project.skills.collision", { name: c.name, winner: c.winner })}</li>)}
		</ul>}
		<p className="px-(--settings-row-inset) text-xs leading-4 text-settings-muted">{t("settings.project.skills.others")}</p>
	</div>;
}
