import { MAX_PROJECT_DISPLAY_NAME_LEN, ProjectSettingsFormView, ProjectSettingsInputRow, ProjectSettingsRow, ProjectSettingsSection, ProjectSettingsValueRow } from "@tallpine/product-ui";
import { useMutation } from "@tanstack/react-query";
import { Pencil } from "lucide-react";
import { Fragment, useEffect, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { isLocal } from "../lib/hosts";
import { usePlannerLocks } from "../hooks/usePlannerLocks";
import { useSettings } from "../hooks/useSettings";
import { PROJECT_LOCK_KEY } from "../../shared/project-managed-keys";
import { deriveRepoHost, deriveRepoPath, IntakeFields, intakeNeedsRule } from "./IntakeFields";
import { ProductExternalLink } from "./ProductExternalLink";
import type { ProjectSettingsSaveState, ProjectSettingsSection as SettingsSection } from "./ProjectSettingsForm";
import { AgentPolicyLock } from "./settings/AgentPolicyLock";
import type { ReviewDefaultsDraft } from "../lib/project-review-defaults";
import { ProjectAutoReviewRow, ProjectRiskPathsRows, ProjectTerminateOnMergeRow } from "./settings/ProjectReviewDefaultsRows";
import { StrongerReviewerControls } from "./settings/StrongerReviewerControls";
import { AgentModelField, ProjectChatHibernationToggle, ProjectNoCIToggle, ProjectPlanAutoApproveRow } from "./settings/ProjectAgentRoleControls";
import { SettingsOptionMenu } from "./settings/SettingsOptionMenu";
import { AGENT_EFFORTS } from "../../shared/effort";
import { Button } from "./ui/button";
import type { ProjectLocalFieldsDraft } from "../lib/project-local-fields";
import { ProjectAdvancedSection } from "./project-settings/ProjectAdvancedSection";
import { ProjectReviewLimitsRows } from "./project-settings/ProjectReviewLimitsRows";
import { ProjectSessionModeRow } from "./project-settings/ProjectSessionModeRow";
import { ProjectWorkflowSection } from "./project-settings/ProjectWorkflowSection";
import { ProjectGitHubAccessSection, ProjectRepositorySection, ProjectWorktreeSetupSection } from "./project-settings/ProjectRepositorySections";

export type ProjectSettingsDraft = ReviewDefaultsDraft & ProjectLocalFieldsDraft & {
	displayName: string;
	defaultBranch: string;
	sessionPrefix: string;
	githubUser?: string;
	canonicalRepoURL?: string;
	workerAgent: string;
	orchestratorAgent: string;
	reviewerHarness: string;
	workerModel: string;
	orchestratorModel: string;
	reviewerModel: string;
	workerMode: string;
	orchestratorMode: string;
	reviewerMode: string;
	workerEffort: string;
	orchestratorEffort: string;
	reviewerEffort: string;
	workerPermissions: string;
	orchestratorPermissions: string;
	reviewerPermissions: string;
	/** Local-only: workers may ask Tallpine to review their own PRs. */
	/** The project has no CI, so Tallpine review approval is the merge gate. */
	noCI?: boolean;
	/** Submitted plans are approved without the owner; a person alone can turn it on. */
	planAutoApprove?: boolean | null;
	/** Idle Chat agents of this project may sleep; false is the project opt-out. */
	chatHibernation?: boolean;
	intakeEnabled: boolean;
	intakeRepo: string;
	intakeAssignee: string;
};

const roles = ["worker", "orchestrator", "reviewer"] as const;
export type ProjectSettingsRole = typeof roles[number];

/** Registry key of the project setting that holds each role's agent and model. */
const roleManagedKey: Record<ProjectSettingsRole, string> = PROJECT_LOCK_KEY;

/** Makes everything inside read-only, with the organization note, when locked. */
function ManagedLock({ locked, children }: { locked: boolean; children: ReactNode }) {
	if (!locked) return <>{children}</>;
	return (
		<fieldset disabled className="contents">
			<div className="px-(--settings-row-inset)"><AgentPolicyLock source="managed" /></div>
			{children}
		</fieldset>
	);
}
export type ProjectAgentPickerProps = {
	role: ProjectSettingsRole;
	draft: ProjectSettingsDraft;
	value: string;
	invalid: boolean;
	disabled?: boolean;
	/** Accessible name when the picker is not the role's own agent picker. */
	label?: string;
	onChange: (agent: string) => void;
};
type Capabilities = {
	workflow: boolean;
	sessionPrefix: boolean;
	githubUser?: boolean;
	intake: boolean;
	reviewer: boolean;
	requiredAgents: boolean;
	noCI?: boolean;
	planAutoApprove?: boolean;
	chatHibernation?: boolean;
	/** Local projects: the regrouped pages and the fields that used to be CLI-only. */
	localFields?: boolean;
	nameLimit?: number;
	requiredBranch?: boolean;
	/** Cloud defaults and tuning belong to its runtime, rather than the local catalog. */
	runtimeDefaults?: boolean;
	/** Cloud keeps each role's agent fixed; its model, effort, and permissions stay editable. Local agents stay editable. */
	lockedAgents?: boolean;
};
type RoleFields = {
	agent: "workerAgent" | "orchestratorAgent" | "reviewerHarness";
	model: "workerModel" | "orchestratorModel" | "reviewerModel";
	mode: "workerMode" | "orchestratorMode" | "reviewerMode";
	effort: "workerEffort" | "orchestratorEffort" | "reviewerEffort";
	permissions: "workerPermissions" | "orchestratorPermissions" | "reviewerPermissions";
};
const roleFields: Record<ProjectSettingsRole, RoleFields> = {
	worker: { agent: "workerAgent", model: "workerModel", mode: "workerMode", effort: "workerEffort", permissions: "workerPermissions" },
	orchestrator: { agent: "orchestratorAgent", model: "orchestratorModel", mode: "orchestratorMode", effort: "orchestratorEffort", permissions: "orchestratorPermissions" },
	reviewer: { agent: "reviewerHarness", model: "reviewerModel", mode: "reviewerMode", effort: "reviewerEffort", permissions: "reviewerPermissions" },
};

// Both persistence adapters render this page. Drafts, validation, autosave and
// common controls live here so a UI change applies to local and Cloud projects.
export function ProjectSettingsEditor({ githubAccountExtra, githubProjectId, initialValues, section, capabilities, details, workspaceRepos, repository, renderAgent, defaultReviewer = () => "", modelScope, modelHostId, autoReviewDescription, save, onSaveState, saveUnchanged = false, disabled = false, managedKeys, generalExtra, generalTop, securityExtra }: {
	initialValues: ProjectSettingsDraft;
	section: SettingsSection;
	capabilities: Capabilities;
	details: Array<{ label: string; value: string; href?: string }>;
	workspaceRepos?: Array<{ name: string; relativePath: string; repo?: string }>;
	repository?: string;
	renderAgent: (props: ProjectAgentPickerProps) => ReactNode;
	defaultReviewer?: (draft: ProjectSettingsDraft) => string;
	modelScope: (agent: string) => string;
	/** Self-hosted daemon whose model catalog the role pickers read. */
	modelHostId?: string;
	autoReviewDescription?: string;
	save: (values: ProjectSettingsDraft) => Promise<{ values?: ProjectSettingsDraft; replacementError?: string | null }>;
	onSaveState?: (state: ProjectSettingsSaveState) => void;
	saveUnchanged?: boolean;
	/** Read-only sections appended to the General page (e.g. a Cloud project's Coder template). */
	generalExtra?: ReactNode;
	/** Rendered right under the GitHub account override (the live account check). */
	githubAccountExtra?: ReactNode;
	/** Project whose signed-in GitHub accounts the account picker lists. */
	githubProjectId?: string;
	/** The Security page body (the project's Security mode switch). */
	securityExtra?: ReactNode;
	/** Rendered first on the General page; setDefaultBranch edits the draft so autosave persists it like a typed change. */
	generalTop?: (api: { setDefaultBranch: (branch: string) => void }) => ReactNode;
	/** Pauses autosave and submit, e.g. while the owning host is offline. */
	disabled?: boolean;
	/** Registry keys the organization's managed config locks; their controls are read-only. */
	managedKeys?: ReadonlySet<string>;
}) {
	const { t } = useTranslation();
	const appDefaults = useSettings(modelHostId).settings?.workflowDefaults;
	const appAutoReview = appDefaults?.autoReview ?? false;
	const plannerLocks = usePlannerLocks(modelHostId === undefined || isLocal(modelHostId));
	const workflowInherit = { appPlanningMode: appDefaults?.planningMode ?? "auto", appPlanAutoApprove: appDefaults?.planAutoApprove ?? false, lockedPlanningMode: plannerLocks.mode, planAutoApproveLocked: plannerLocks.autoApproveOff };
	const [draft, setDraft] = useState(initialValues);
	const saved = useRef(JSON.stringify(initialValues));
	const failedKey = useRef<string | undefined>(undefined);
	const [error, setError] = useState<string>();
	const [replacementError, setReplacementError] = useState<string>();
	const [didSave, setDidSave] = useState(false);
	const [showSaving, setShowSaving] = useState(false);
	const [validity, setValidity] = useState({ worker: true, orchestrator: true, reviewer: true });
	const mutation = useMutation({
		mutationFn: (values: ProjectSettingsDraft) => save(values),
		onSuccess: (result, values) => {
			const normalized = result.values ?? values;
			saved.current = JSON.stringify(normalized);
			failedKey.current = undefined;
			setDraft((current) => JSON.stringify(current) === JSON.stringify(values) ? normalized : current);
			setReplacementError(result.replacementError ?? undefined);
			setDidSave(true);
		},
		onError: (cause, values) => {
			failedKey.current = JSON.stringify(values);
			setError(cause instanceof Error ? cause.message : t("settings.project.saveFailed"));
		},
	});
	const dirty = JSON.stringify(draft) !== saved.current;
	const intake = { enabled: draft.intakeEnabled, repo: draft.intakeRepo, assignee: draft.intakeAssignee };
	const intakeIncomplete = capabilities.intake && intakeNeedsRule(intake);
	const visibleRoles = capabilities.reviewer ? roles : roles.slice(0, 2);
	const nameLimit = capabilities.nameLimit ?? MAX_PROJECT_DISPLAY_NAME_LEN;
	const validate = () => {
		if (capabilities.requiredAgents && (!draft.workerAgent || !draft.orchestratorAgent)) return t("settings.project.agentsRequired");
		if (!draft.displayName.trim()) return t("settings.project.nameRequired");
		if ([...draft.displayName.trim()].length > nameLimit && draft.displayName.trim() !== initialValues.displayName.trim()) return t("settings.project.nameTooLong", { max: nameLimit });
		if (capabilities.requiredBranch && (!draft.defaultBranch.trim() || [...draft.defaultBranch.trim()].length > 255)) return t("settings.cloudProject.identityValidation");
		if (visibleRoles.some((role) => !validity[role])) return t("settings.project.tuningInvalid");
		return undefined;
	};
	const submit = () => {
		if (disabled || mutation.isPending || intakeIncomplete || (!dirty && !saveUnchanged)) return;
		const problem = validate();
		setError(problem);
		if (problem) return;
		setReplacementError(undefined);
		setDidSave(false);
		mutation.mutate(draft);
	};
	useEffect(() => {
		if (disabled || !dirty || mutation.isPending || JSON.stringify(draft) === failedKey.current) return;
		const timeout = window.setTimeout(submit, 650);
		return () => window.clearTimeout(timeout);
	}, [disabled, draft, dirty, mutation.isPending, validity]);
	useEffect(() => {
		if (!mutation.isPending) { setShowSaving(false); return; }
		const timeout = window.setTimeout(() => setShowSaving(true), 200);
		return () => window.clearTimeout(timeout);
	}, [mutation.isPending]);
	useEffect(() => {
		onSaveState?.({ phase: error ? "failed" : mutation.isPending ? showSaving ? "saving" : "pending" : dirty && !intakeIncomplete ? "pending" : didSave ? "saved" : "idle", dirty: dirty && !intakeIncomplete, requestPending: mutation.isPending, draftFailed: error !== undefined && JSON.stringify(draft) === failedKey.current, error, replacementError });
	}, [draft, dirty, didSave, error, intakeIncomplete, mutation.isPending, onSaveState, replacementError, showSaving]);
	useEffect(() => {
		if (!didSave) return;
		const timeout = window.setTimeout(() => setDidSave(false), 1800);
		return () => window.clearTimeout(timeout);
	}, [didSave]);
	const patch = (values: Partial<ProjectSettingsDraft>) => setDraft((current) => ({ ...current, ...values }));
	const detailsBlock = <>
				<ProjectSettingsSection title={t("settings.project.details")} grouped>
					<ProjectSettingsInputRow id="projectName" label={t("settings.project.name")} editLabel={t("settings.field.edit", { label: t("settings.project.name") })} editIcon={<Pencil className="settings-inline-edit-icon" aria-hidden="true" />} value={draft.displayName} onChange={(displayName) => patch({ displayName })} />
					{details.map((detail) => <ProjectSettingsValueRow key={detail.label} {...detail} externalLink={ProductExternalLink} />)}
				</ProjectSettingsSection>
				{workspaceRepos && <ProjectSettingsSection title={t("settings.project.workspaceRepos")} grouped>
					{workspaceRepos.length ? workspaceRepos.map((repo) => <ProjectSettingsRow key={repo.name} label={repo.name}><span className="settings-row-value">{repo.relativePath}{repo.repo ? ` · ${repo.repo}` : ""}</span></ProjectSettingsRow>) : <div className="settings-row-bar text-sm text-settings-muted">{t("settings.project.childReposEmpty")}</div>}
				</ProjectSettingsSection>}
	</>;
	const repositoryBlock = capabilities.workflow ? <>
					<ProjectRepositorySection values={draft} repository={section === "repository" ? repository : undefined} requiredBranch={capabilities.requiredBranch} showPrefix={capabilities.sessionPrefix} onChange={patch} />
					{capabilities.githubUser && <ProjectGitHubAccessSection values={draft} projectId={githubProjectId} check={githubAccountExtra} error={error} onChange={patch} />}
	</> : null;
	const prBlock = capabilities.workflow ? <>
					<ProjectSettingsSection title={t("settings.project.pullRequests")} grouped>
						<ProjectAutoReviewRow values={draft} onChange={patch} description={autoReviewDescription} appDefault={appAutoReview} locked={managedKeys?.has(PROJECT_LOCK_KEY.autoReview) === true} />
						<ManagedLock locked={managedKeys?.has(PROJECT_LOCK_KEY.terminateOnPrMerge) === true}><ProjectTerminateOnMergeRow values={draft} onChange={patch} /></ManagedLock>
						<ManagedLock locked={managedKeys?.has(PROJECT_LOCK_KEY.reviewRisk) === true}><ProjectRiskPathsRows values={draft} onChange={patch} /></ManagedLock>
						{capabilities.noCI && <ProjectNoCIToggle checked={draft.noCI ?? false} onCheckedChange={(noCI) => patch({ noCI })} />}
						{capabilities.planAutoApprove && !capabilities.localFields && <ProjectPlanAutoApproveRow value={draft.planAutoApprove} onChange={(planAutoApprove) => patch({ planAutoApprove })} appDefault={workflowInherit.appPlanAutoApprove} locked={workflowInherit.planAutoApproveLocked} />}
						{capabilities.localFields && <ProjectReviewLimitsRows values={draft} onChange={patch} />}
					</ProjectSettingsSection>
	</> : null;
	const sessionsBlock = <>
				{capabilities.chatHibernation && <ProjectSettingsSection title={t("settings.sessions")} grouped>
					<>{capabilities.localFields && <ProjectSessionModeRow values={draft} onChange={patch} />}<ProjectChatHibernationToggle checked={draft.chatHibernation ?? true} onCheckedChange={(chatHibernation) => patch({ chatHibernation })} /></>
				</ProjectSettingsSection>}
	</>;
	const roleBlocks = (list: readonly ProjectSettingsRole[]) => <>
				{list.map((role) => {
					const fields = roleFields[role];
					const selectedAgent = draft[fields.agent];
					const agent = selectedAgent || (role === "reviewer" ? defaultReviewer(draft) : "");
					// With no reviewer configured, review runs with the worker agent's own
					// model and effort when that agent is also the reviewer. Show that, and
					// keep it when the user pins the reviewer by editing one field.
					const inheritsWorker = role === "reviewer" && !selectedAgent && agent !== "" && agent === draft.workerAgent;
					const updateConfig = (key: RoleFields["model"] | RoleFields["mode"] | RoleFields["effort"] | RoleFields["permissions"], value: string) => setDraft((current) => ({
						...current,
						...(role === "reviewer" && !current.reviewerHarness && agent
							? { reviewerHarness: agent, ...(agent === current.workerAgent ? { reviewerModel: current.workerModel, reviewerEffort: current.workerEffort } : {}) }
							: {}),
						[key]: value,
					}));
					const updatePermissions = (value: string) => setDraft((current) => ({
						...current,
						...(role === "reviewer" && !current.reviewerHarness && agent
							? { reviewerHarness: agent, ...(agent === current.workerAgent ? { reviewerModel: current.workerModel, reviewerEffort: current.workerEffort } : {}) }
							: {}),
						[fields.permissions]: value,
					}));
					return <Fragment key={role}><ManagedLock locked={managedKeys?.has(roleManagedKey[role]) === true}><ProjectSettingsSection title={t(`settings.models.${role}Role`)} grouped>
						<ProjectSettingsRow label={t("settings.project.agent")}><div className="flex min-w-0 flex-wrap items-center justify-end gap-2"><div className="min-w-0">{renderAgent({ disabled: capabilities.lockedAgents, role, draft, value: selectedAgent, invalid: error !== undefined && !selectedAgent, onChange: (value) => setDraft((current) => ({ ...current, [fields.agent]: value, ...(value !== current[fields.agent] ? { [fields.model]: "", [fields.mode]: "", [fields.effort]: "", ...(role === "reviewer" || capabilities.runtimeDefaults ? { [fields.permissions]: "" } : {}) } : {}) })) })}</div>
						<div className="min-w-0 space-y-1.5"><AgentModelField role={role} agentId={agent} projectId={modelScope(agent)} hostId={modelHostId} model={inheritsWorker ? draft.workerModel : draft[fields.model]} mode={draft[fields.mode]} effort={inheritsWorker ? draft.workerEffort : draft[fields.effort]}
							allowCustomFallback={capabilities.runtimeDefaults} followCatalogDefaults={!capabilities.runtimeDefaults}
							supportedEfforts={capabilities.runtimeDefaults ? AGENT_EFFORTS[agent] : undefined}
							emptyLabel={capabilities.runtimeDefaults && agent === "" ? t("settings.cloudProject.sessionModel") : undefined}
							onModelChange={(value) => updateConfig(fields.model, value)} onModeChange={(value) => updateConfig(fields.mode, value)} onEffortChange={(value) => updateConfig(fields.effort, value)} onValidityChange={(valid) => setValidity((current) => current[role] === valid ? current : { ...current, [role]: valid })} />
						</div></div></ProjectSettingsRow>
						<ProjectSettingsRow label={t("settings.project.approval")}><div className="min-w-0"><ProjectRolePermissions role={role} agent={agent} value={draft[fields.permissions]} runtimeDefaults={capabilities.runtimeDefaults} onChange={updatePermissions} /></div></ProjectSettingsRow>
					</ProjectSettingsSection></ManagedLock>
					{role === "reviewer" && <ManagedLock locked={managedKeys?.has(PROJECT_LOCK_KEY.reviewRisk) === true}><ProjectSettingsSection title={t("settings.reviewDefaults.strongerReviewer")} grouped>
						<StrongerReviewerControls values={draft} onChange={patch} modelScope={modelScope} modelHostId={modelHostId}
							renderAgent={({ value, onChange }) => renderAgent({ role: "reviewer", draft, value, invalid: false, disabled: capabilities.lockedAgents, label: t("settings.reviewDefaults.strongerReviewer"), onChange })} />
					</ProjectSettingsSection></ManagedLock>}
					</Fragment>;
				})}
	</>;
	const agentsMissing = capabilities.requiredAgents && (!draft.workerAgent || !draft.orchestratorAgent);
	const agentsError = agentsMissing && <p className="px-(--settings-row-inset) text-xs text-error" role="alert">{t("settings.project.agentsRequired")}</p>;
	const topBlock = generalTop?.({ setDefaultBranch: (defaultBranch) => patch({ defaultBranch }) });
	const intakeBlock = capabilities.workflow ? <>
					{capabilities.intake && <ManagedLock locked={managedKeys?.has(PROJECT_LOCK_KEY.intake) === true}><ProjectSettingsSection title={t("settings.project.issues")} grouped><IntakeFields variant="settings" form={intake} onChange={(values) => patch({ intakeEnabled: values.enabled ?? draft.intakeEnabled, intakeRepo: values.repo ?? draft.intakeRepo, intakeAssignee: values.assignee ?? draft.intakeAssignee })} repoPreview={{ value: draft.intakeRepo.trim() || deriveRepoPath(repository ?? ""), host: deriveRepoHost(repository ?? "") }} /></ProjectSettingsSection></ManagedLock>}
	</> : null;
	const general = <>
		{topBlock}
		{detailsBlock}
		{capabilities.workflow && <>{repositoryBlock}{intakeBlock}{prBlock}</>}
		{capabilities.chatHibernation && sessionsBlock}
		{generalExtra}
	</>;
	const localPages: Record<string, ReactNode> = {
		details: <>{detailsBlock}{generalExtra}</>,
		repository: <>
			{topBlock}
			{repositoryBlock}
			{capabilities.workflow && <ProjectWorktreeSetupSection values={draft} onChange={patch} />}
			{intakeBlock}
		</>,
		agents: <>{roleBlocks(roles.slice(0, 2))}{agentsError}</>,
		reviews: <>{capabilities.reviewer && roleBlocks(["reviewer"])}{prBlock}</>,
		workflow: <ProjectSettingsSection title={t("settings.project.workflow")} grouped><ProjectWorkflowSection values={draft} planAutoApprove={draft.planAutoApprove} inherit={workflowInherit} showPlanAutoApprove={capabilities.workflow} onChange={patch} onPlanAutoApprove={(planAutoApprove) => patch({ planAutoApprove })} /></ProjectSettingsSection>,
		sessions: sessionsBlock,
		security: <ProjectSettingsSection title={t("settings.security")} grouped>{securityExtra}</ProjectSettingsSection>,
		advanced: <ProjectSettingsSection title={t("settings.advanced")} grouped><ProjectAdvancedSection values={draft} onChange={patch} /></ProjectSettingsSection>,
	};
	return <ProjectSettingsFormView id="project-settings-form" className={`project-settings-form${capabilities.runtimeDefaults ? " cloud-project-settings-form" : ""}`} onSubmit={submit}>
		<fieldset disabled={disabled || mutation.isPending} className="flex min-w-0 flex-col gap-(--size-settings-section-gap)">
			{capabilities.localFields && section in localPages ? localPages[section] : section === "general" ? general : <>
				{roleBlocks(visibleRoles)}
				{agentsError}
			</>}
		</fieldset>
		{error && !onSaveState && <p role="alert" className="px-(--settings-row-inset) text-xs text-error">{error}</p>}
		{!onSaveState && <div className="flex justify-end"><Button type="submit" disabled={!dirty || mutation.isPending}>{mutation.isPending ? t("settings.project.saving") : t("files.saveFile")}</Button></div>}
	</ProjectSettingsFormView>;
}

function ProjectRolePermissions({ role, agent, value, runtimeDefaults, onChange }: { role: ProjectSettingsRole; agent: string; value: string; runtimeDefaults?: boolean; onChange: (value: string) => void }) {
	const { t } = useTranslation();
	const label = t("settings.project.roleApproval", { role: t(`settings.models.${role}Role`) });
	const values = runtimeDefaults ? ["", "default", "auto", "accept-edits", "bypass-permissions"] : [...(agent === "codex" ? [] : ["default"]), "auto", "accept-edits", "bypass-permissions"];
	const options = values.map((permission) => ({ value: permission, label: permission === "" ? t("settings.cloudProject.sessionPolicy") : permission === "default" ? t(agent === "claude-code" ? "settings.project.permissionUseClaude" : "settings.project.permissionUseAgent") : t(permission === "accept-edits" ? "settings.project.permissionAcceptEdits" : permission === "auto" ? "settings.project.permissionAuto" : "settings.project.permissionBypass") }));
	return <SettingsOptionMenu aria-label={label} value={runtimeDefaults ? value : value === "default" && agent === "codex" ? "bypass-permissions" : value || "auto"} options={options} disabled={runtimeDefaults && !agent} placeholder={t("settings.project.permissionNotReported")} triggerClassName="w-fit" onChange={onChange} />;
}
