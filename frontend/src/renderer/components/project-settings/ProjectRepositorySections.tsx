import { ProjectSettingsSection, ProjectSettingsValueRow } from "@tallpine/product-ui";
import { useTranslation } from "react-i18next";
import type { ProjectLocalFieldsDraft } from "../../lib/project-local-fields";
import { SettingsInlineInput, SettingsRow } from "../settings/SettingsRow";
import { ProjectGitHubAccountRow } from "./ProjectGitHubAccountRow";
import { ProjectWorktreeCommandsSection } from "./ProjectWorktreeCommandsSection";

type Draft = ProjectLocalFieldsDraft & { defaultBranch: string; sessionPrefix: string; githubUser?: string; canonicalRepoURL?: string };

/** The repository itself: where it lives, which branch sessions start from, how sessions are named. */
export function ProjectRepositorySection({ values, repository, requiredBranch, showPrefix, onChange }: {
	values: Draft;
	repository?: string;
	requiredBranch?: boolean;
	showPrefix: boolean;
	onChange: (patch: Partial<Draft>) => void;
}) {
	const { t } = useTranslation();
	return <ProjectSettingsSection title={t("settings.project.repositorySection")} grouped>
		{repository !== undefined && <ProjectSettingsValueRow label={t("settings.project.origin")} value={repository || t("settings.project.originNone")} />}
		<SettingsRow label={t("settings.project.defaultBranch")} description={t("settings.project.defaultBranchDescription")}>
			<SettingsInlineInput id="defaultBranch" label={t("settings.project.defaultBranch")} value={values.defaultBranch} placeholder={requiredBranch ? undefined : "auto"} onChange={(defaultBranch) => onChange({ defaultBranch })} />
		</SettingsRow>
		{showPrefix && <SettingsRow label={t("settings.project.sessionPrefix")} description={t("settings.project.sessionPrefixDescription")}>
			<SettingsInlineInput id="sessionPrefix" label={t("settings.project.sessionPrefix")} value={values.sessionPrefix} placeholder={t("settings.project.sessionPrefixPlaceholder")} onChange={(sessionPrefix) => onChange({ sessionPrefix })} />
		</SettingsRow>}
	</ProjectSettingsSection>;
}

/** Which GitHub account sessions use, and the moved-repository URL. `check` is the live account check. */
export function ProjectGitHubAccessSection({ values, projectId, check, error, onChange }: {
	values: Draft;
	projectId?: string;
	check?: React.ReactNode;
	error?: string;
	onChange: (patch: Partial<Draft>) => void;
}) {
	const { t } = useTranslation();
	return <ProjectSettingsSection title={t("settings.project.githubAccess")} grouped>
		<ProjectGitHubAccountRow projectId={projectId} value={values.githubUser ?? ""} onChange={(githubUser) => onChange({ githubUser })} />
		{check}
		<SettingsRow label={t("settings.project.canonicalRepoUrl")} description={t("settings.project.canonicalRepoUrlDescription")}>
			<SettingsInlineInput id="canonicalRepoURL" label={t("settings.project.canonicalRepoUrl")} value={values.canonicalRepoURL ?? ""} emptyLabel={t("settings.project.canonicalRepoUrlEmpty")} placeholder={t("settings.project.canonicalRepoUrlPlaceholder")} onChange={(canonicalRepoURL) => onChange({ canonicalRepoURL })} />
		</SettingsRow>
		{error && /canonical repo/i.test(error) && <p role="alert" className="px-(--settings-row-inset) text-xs text-error">{error}</p>}
	</ProjectSettingsSection>;
}

/** What each session worktree links and how it is seeded; the Setup/Cleanup scripts follow below it. */
export function ProjectWorktreeSetupSection({ values, onChange }: { values: ProjectLocalFieldsDraft; onChange: (patch: Partial<ProjectLocalFieldsDraft>) => void }) {
	const { t } = useTranslation();
	return <ProjectSettingsSection title={t("settings.project.worktreeSetup")} grouped><ProjectWorktreeCommandsSection values={values} onChange={onChange} /></ProjectSettingsSection>;
}
