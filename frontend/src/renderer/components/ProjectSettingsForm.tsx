import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../api/schema";
import { lockedEchoPatch } from "../../shared/project-managed-keys";
import { useAgentReadinessQuery, useEnsureAgentReadiness } from "../hooks/useAgentReadinessQuery";
import { useConnectedHosts } from "../hooks/useHostConnection";
import { useManagedLocks } from "../hooks/useManagedLocks";
import { useProjectSettings } from "../hooks/useProjectSettings";
import { useSettings } from "../hooks/useSettings";
import { settingsChangesQueryKey } from "../hooks/useSettingsChanges";
import { useRemoteProjectQuery, workspaceQueryKeyForHost, workspaceQueryOptions } from "../hooks/useWorkspaceQuery";
import { isLaunchableAgent } from "../lib/agent-select-options";
import { LOCAL_HOST, refKey } from "../lib/hosts";
import { projectQueryKey, putProject, type Project, type ProjectConfig } from "../lib/project-settings-api";
import { openRemoteOrchestrator } from "../lib/remote-orchestrator";
import { WORKER_DEFAULT_REVIEWERS } from "../lib/reviewer-harnesses";
import { OrchestratorSpawnError, spawnOrchestrator } from "../lib/spawn-orchestrator";
import { useUiStore, type OrchestratorReplacementFailure } from "../stores/ui-store";
import { newestActiveOrchestrator } from "../types/workspace";
import { RequiredAgentField } from "./CreateProjectAgentSheet";
import { DefaultBranchNotice } from "./DefaultBranchNotice";
import { ProjectGitHubAccountCheck } from "./ProjectGitHubAccountCheck";
import { githubAccountCheckKey } from "../lib/github-account-check";
import { buildIntake } from "./IntakeFields";
import { reviewDefaultsConfig, reviewDefaultsDraft } from "../lib/project-review-defaults";
import { ProjectSettingsEditor, type ProjectAgentPickerProps, type ProjectSettingsDraft } from "./ProjectSettingsEditor";
import { ReviewerSelect } from "./ReviewerSelect";
import { projectLocalFieldsConfig, projectLocalFieldsDraft } from "../lib/project-local-fields";
import { ProjectSecuritySection } from "./project-settings/ProjectSecuritySection";
import { ProjectSettingsChanges } from "./settings/ProjectSettingsChanges";

type TrackerIntakeConfig = components["schemas"]["TrackerIntakeConfig"];

const DEFAULT_BRANCH_AUTO = "auto";


type SettingsSaveResult = {
	savedKey: string;
	replacementError: string | null;
	replacementSessionId: string | null;
	replacementFailure: OrchestratorReplacementFailure | null;
	spawnError: unknown;
};

/** "general" and "agents" are the remote host pages; the rest are the local project pages. */
export type ProjectSettingsSection = "general" | "agents" | "details" | "repository" | "reviews" | "workflow" | "sessions" | "security" | "advanced";
export type ProjectSettingsSaveState = {
	phase: "idle" | "pending" | "saving" | "saved" | "failed";
	dirty?: boolean;
	requestPending?: boolean;
	error?: string;
	replacementError?: string;
	/** The draft is dirty but cannot be saved as it stands (e.g. an invalid name). */
	unsaveable?: boolean;
	/** The current draft is the exact one whose save failed (not edited since). */
	draftFailed?: boolean;
	retry?: () => void;
};

type ProjectSettingsFormProps = {
	projectId: string;
	/** Installation ID of the self-hosted daemon that owns this project. */
	hostId?: string;
	section?: ProjectSettingsSection;
	onSaveState?: (state: ProjectSettingsSaveState) => void;
};

export function ProjectSettingsForm(props: ProjectSettingsFormProps) {
	return <LocalProjectSettingsAdapter {...props} />;
}

function LocalProjectSettingsAdapter({
	projectId,
	hostId,
	section = "general",
	onSaveState,
}: ProjectSettingsFormProps) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const connected = useConnectedHosts();
	const hostConnected = !hostId || connected.includes(hostId);
	useEffect(() => {
		if (!hostConnected) onSaveState?.({ phase: "idle" });
	}, [hostConnected, onSaveState]);

	const query = useProjectSettings(projectId, { hostId, enabled: hostConnected });

	return (
		<>
			{!hostConnected ? <p role="alert" className="text-pretty text-sm text-error">{t("remote.hostOffline")}</p> : null}
			{!query.data && hostConnected && query.isLoading ? (
				<p className="text-sm text-settings-muted">{t("settings.project.loading")}</p>
			) : !query.data && hostConnected ? (
				<p className="text-sm text-error">{query.error instanceof Error ? query.error.message : t("settings.project.loadFailed")}</p>
			) : query.data ? (
				<div hidden={!hostConnected}>
				<SettingsBody
					key={refKey({ host: hostId ?? LOCAL_HOST, id: projectId })}
					project={query.data}
					onSaved={() =>
						Promise.all([
							queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) }),
							queryClient.invalidateQueries({ queryKey: settingsChangesQueryKey(projectId) }),
						]).then(() => undefined, () => {
							// Saving succeeds even if the cache refresh fails.
						})
					}
					projectId={projectId}
					hostId={hostId}
					hostConnected={hostConnected}
					section={section}
					onSaveState={onSaveState}
				/>
				</div>
			) : null}
		</>
	);
}

function SettingsBody({ project, projectId, hostId, hostConnected, onSaved, section = "general", onSaveState }: {
	project: Project;
	projectId: string;
	hostId?: string;
	hostConnected: boolean;
	onSaved: () => Promise<void>;
	section?: ProjectSettingsSection;
	onSaveState?: (state: ProjectSettingsSaveState) => void;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const setOrchestratorReplacementError = useUiStore((state) => state.setOrchestratorReplacementError);
	const workspaceQuery = useQuery({ ...workspaceQueryOptions, enabled: !hostId });
	const remoteProjectQuery = useRemoteProjectQuery(hostId ?? "", projectId);
	const managedKeys = useManagedLocks(projectId, !hostId);
	const config = project.config ?? {};
	const isScratchProject = project.kind === "scratch";
	const { settings } = useSettings(hostId);
	const intakeVisible = !isScratchProject && !!settings?.trackerIntakeEnabled;
	const workspace = hostId ? remoteProjectQuery.data : workspaceQuery.data?.find((item) => item.id === projectId);
	const activeOrchestrator = newestActiveOrchestrator(workspace?.sessions ?? []);
	const intake: TrackerIntakeConfig = config.trackerIntake ?? {};
	const initialValues: ProjectSettingsDraft = {
		displayName: project.name,
		defaultBranch: config.defaultBranch ?? DEFAULT_BRANCH_AUTO,
		sessionPrefix: config.sessionPrefix ?? "",
		githubUser: config.githubUser ?? "",
		canonicalRepoURL: config.canonicalRepoURL ?? "",
		workerAgent: config.worker?.agent ?? "",
		orchestratorAgent: config.orchestrator?.agent ?? "",
		workerModel: config.worker?.agentConfig?.model ?? config.agentConfig?.model ?? "",
		workerEffort: config.worker?.agentConfig?.effort ?? config.agentConfig?.effort ?? "",
		workerPermissions: config.worker?.agentConfig?.permissions ?? config.agentConfig?.permissions ?? "",
		orchestratorModel: config.orchestrator?.agentConfig?.model ?? config.agentConfig?.model ?? "",
		orchestratorEffort: config.orchestrator?.agentConfig?.effort ?? config.agentConfig?.effort ?? "",
		orchestratorPermissions: config.orchestrator?.agentConfig?.permissions ?? config.agentConfig?.permissions ?? "",
		workerMode: config.worker?.agentConfig?.mode ?? config.agentConfig?.mode ?? "",
		orchestratorMode: config.orchestrator?.agentConfig?.mode ?? config.agentConfig?.mode ?? "",
		reviewerHarness: config.reviewers?.[0]?.harness ?? "",
		reviewerModel: config.reviewers?.[0]?.agentConfig?.model ?? config.agentConfig?.model ?? "",
		reviewerMode: config.reviewers?.[0]?.agentConfig?.mode ?? config.agentConfig?.mode ?? "",
		reviewerEffort: config.reviewers?.[0]?.agentConfig?.effort ?? config.agentConfig?.effort ?? "",
		reviewerPermissions: config.reviewers?.[0]?.agentConfig?.permissions ?? config.agentConfig?.permissions ?? "",
		...reviewDefaultsDraft(config),
			...projectLocalFieldsDraft(config),
		noCI: config.noCI ?? false,
		planAutoApprove: config.planAutoApprove ?? null,
		chatHibernation: !config.chatHibernation?.disabled,
		intakeEnabled: intake.enabled ?? false,
		intakeRepo: intake.repo ?? "",
		intakeAssignee: intake.assignee ?? "",
	};
	const lastOrchestratorRef = useRef(config.orchestrator?.agent ?? "");
	const replacementAttemptedRef = useRef(false);
	const replacementFailedRef = useRef(false);
	const agentsQuery = useAgentReadinessQuery(true, hostId);
	useEnsureAgentReadiness({ hostId });
	const persist = async (values: ProjectSettingsDraft): Promise<SettingsSaveResult> => {
		const savedKey = JSON.stringify(values);
		const displayName = values.displayName.trim();
		const { model: _legacyModel, mode: _legacyMode, effort: _legacyEffort, permissions: _legacyPermissions, ...sharedAgentConfig } = config.agentConfig ?? {};
		const existingReviewer = config.reviewers?.[0];
		const existingReviewerAgentConfig = existingReviewer?.harness === values.reviewerHarness ? existingReviewer.agentConfig : undefined;
		const next: ProjectConfig = isScratchProject
			? {
					...scratchSupportedConfig(config),
					...(hostId ? {} : projectLocalFieldsConfig(values)),
					worker: {
						...config.worker,
						agent: values.workerAgent,
						agentConfig: buildRoleAgentConfig(config.worker?.agentConfig, values.workerModel, values.workerMode, values.workerEffort, values.workerPermissions),
					},
					orchestrator: {
						...config.orchestrator,
						agent: values.orchestratorAgent,
						agentConfig: buildRoleAgentConfig(
							config.orchestrator?.agentConfig,
							values.orchestratorModel,
							values.orchestratorMode,
							values.orchestratorEffort,
							values.orchestratorPermissions,
						),
					},
					agentConfig: blankToUndefined({
						...sharedAgentConfig,
						permissions: undefined,
					}),
				}
			: {
					...withoutLegacyKeys(config),
					defaultBranch: values.defaultBranch.trim() === DEFAULT_BRANCH_AUTO ? undefined : values.defaultBranch || undefined,
					sessionPrefix: values.sessionPrefix || undefined,
					githubUser: values.githubUser?.trim() || undefined,
					canonicalRepoURL: values.canonicalRepoURL?.trim() || undefined,
					worker: {
						...config.worker,
						agent: values.workerAgent,
						agentConfig: buildRoleAgentConfig(config.worker?.agentConfig, values.workerModel, values.workerMode, values.workerEffort, values.workerPermissions),
					},
					orchestrator: {
						...config.orchestrator,
						agent: values.orchestratorAgent,
						agentConfig: buildRoleAgentConfig(
							config.orchestrator?.agentConfig,
							values.orchestratorModel,
							values.orchestratorMode,
							values.orchestratorEffort,
							values.orchestratorPermissions,
						),
					},
					agentConfig: blankToUndefined({
						...sharedAgentConfig,
						permissions: undefined,
					}),
					reviewers: values.reviewerHarness
						? [
								{
									harness: values.reviewerHarness,
									agentConfig: buildRoleAgentConfig(
										existingReviewerAgentConfig,
										values.reviewerModel,
										values.reviewerMode,
										values.reviewerEffort,
										values.reviewerPermissions,
									),
								},
							]
						: undefined,
					trackerIntake: buildIntake(
						{
							enabled: values.intakeEnabled,
							repo: values.intakeRepo,
							assignee: values.intakeAssignee,
						},
						config.trackerIntake,
					),
					...reviewDefaultsConfig(config, values),
					...(hostId ? {} : projectLocalFieldsConfig(values)),
					noCI: values.noCI || undefined,
					planAutoApprove: values.planAutoApprove ?? undefined,
				};
		// A key the organization locks is sent back exactly as loaded: the draft rebuilds
		// it with fallbacks to the top-level agent config, which would read as a change.
		Object.assign(next, lockedEchoPatch(config, managedKeys));
		// The opt-out is the only stored state: an enabled project carries none.
		next.chatHibernation = values.chatHibernation === false ? { disabled: true } : undefined;
		await putProject(projectId, displayName, next, hostId);
		const replaceOrchestrator = replacementFailedRef.current || values.orchestratorAgent !== lastOrchestratorRef.current ||
			(Boolean(activeOrchestrator && activeOrchestrator.provider !== values.orchestratorAgent) && !replacementAttemptedRef.current);
		lastOrchestratorRef.current = values.orchestratorAgent;
		if (replaceOrchestrator) {
			replacementAttemptedRef.current = true;
			try {
				const sessionId = hostId
					? await openRemoteOrchestrator(hostId, projectId, undefined, undefined, true, "settings")
					: await spawnOrchestrator(projectId, "settings", true);
				replacementFailedRef.current = false;
				return {
					replacementError: null,
					replacementSessionId: sessionId,
					replacementFailure: null,
					spawnError: null,
					savedKey,
				} satisfies SettingsSaveResult;
			} catch (error) {
				replacementFailedRef.current = true;
				const replacementFailure: OrchestratorReplacementFailure = {
					message: error instanceof Error ? error.message : t("settings.project.replaceOrchestratorFailed"),
					...(error instanceof OrchestratorSpawnError
						? {
								code: error.code,
								requestId: error.requestId,
								details: error.details,
							}
						: {}),
				};
				return {
					replacementError: replacementFailure.message,
					replacementSessionId: null,
					replacementFailure,
					spawnError: error,
					savedKey,
				} satisfies SettingsSaveResult;
			}
		}
		return {
			replacementError: null,
			replacementSessionId: null,
			replacementFailure: null,
			spawnError: null,
			savedKey,
		} satisfies SettingsSaveResult;
	};
	const save = async (values: ProjectSettingsDraft) => {
		try {
			const result = await persist(values);
			void queryClient.invalidateQueries({ queryKey: projectQueryKey(projectId, hostId) });
			void queryClient.invalidateQueries({ queryKey: hostId ? ["project-config", hostId, projectId] : ["project-config", projectId] });
			void queryClient.invalidateQueries({ queryKey: githubAccountCheckKey(projectId) });
			void onSaved();
			if (result.replacementFailure) {
				if (!hostId) setOrchestratorReplacementError(projectId, result.replacementFailure);
			}
			return { replacementError: result.replacementError };
		} catch (error) {
			throw error;
		}
	};
	return <ProjectSettingsEditor initialValues={initialValues} section={section} disabled={!hostConnected} managedKeys={managedKeys}
		githubAccountExtra={hostId || isScratchProject ? undefined : <ProjectGitHubAccountCheck projectId={projectId} />}
		generalExtra={hostId ? undefined : <ProjectSettingsChanges projectId={projectId} />}
		securityExtra={hostId ? undefined : <ProjectSecuritySection projectId={projectId} />}
		generalTop={hostId || isScratchProject ? undefined : ({ setDefaultBranch }) => <DefaultBranchNotice projectId={projectId} onSwitch={setDefaultBranch} />}
		capabilities={{ workflow: !isScratchProject, sessionPrefix: !isScratchProject, githubUser: !isScratchProject, intake: intakeVisible, reviewer: !isScratchProject, requiredAgents: true, noCI: !isScratchProject, planAutoApprove: true, chatHibernation: true, localFields: !hostId }}
		details={[{ label: t("settings.project.path"), value: project.path, href: hostId ? undefined : `file://${encodeURI(project.path)}` }, { label: t("settings.project.repo"), value: project.repo || "—", href: project.repo ? repositoryHref(project.repo) : undefined }, ...(isScratchProject ? [] : [{ label: t("settings.project.githubAccount"), value: project.githubAccount || t("settings.project.githubAccountDefault") }])]}
		workspaceRepos={project.kind === "workspace" ? project.workspaceRepos ?? [] : undefined} repository={project.repo}
		modelScope={() => ""} defaultReviewer={(draft) => WORKER_DEFAULT_REVIEWERS[draft.workerAgent] ?? "claude-code"}
		save={save} saveUnchanged onSaveState={onSaveState}
		modelHostId={hostId}
		renderAgent={(props) => <LocalAgentPicker {...props} projectId={projectId} hostId={hostId} agentsQuery={agentsQuery} />} />;
}

function LocalAgentPicker({ role, draft, value, invalid, onChange, label, projectId, hostId, agentsQuery }: ProjectAgentPickerProps & { projectId: string; hostId?: string; agentsQuery: ReturnType<typeof useAgentReadinessQuery> }) {
	const { t } = useTranslation();
	useEnsureAgentReadiness({ agentIds: [draft.workerAgent, draft.orchestratorAgent, draft.reviewerHarness], enabled: role === "worker" && Boolean(draft.workerAgent || draft.orchestratorAgent || draft.reviewerHarness), hostId, purpose: hostId ? "launch" : "display" });
	const disabled = agentsQuery.isFetching && agentsQuery.data === undefined;
	const agents = hostId ? agentsQuery.data?.agents.filter(isLaunchableAgent) : agentsQuery.data?.agents;
	return role === "reviewer"
		? <ReviewerSelect value={value} model={draft.reviewerModel} mode={draft.reviewerMode} projectId={projectId} hostId={hostId} harnessOnly defaultHarness={WORKER_DEFAULT_REVIEWERS[draft.workerAgent] ?? "claude-code"} triggerClassName="w-full" onChange={onChange} ariaLabel={label ?? t("settings.project.defaultReviewer")} agents={agents} disabled={disabled} />
		: <RequiredAgentField id={`${role}Agent`} variant="settings-control" value={value} placeholder={t(role === "worker" ? "settings.project.selectWorker" : "settings.project.selectOrchestrator")} label={t(role === "worker" ? "settings.project.defaultWorker" : "settings.project.defaultOrchestrator")} agents={agents} hostId={hostId} disabled={disabled} invalid={invalid} onChange={onChange} />;
}

function repositoryHref(repository: string): string | undefined {
	if (/^https?:\/\//i.test(repository)) return repository;
	if (repository.startsWith("git@")) {
		const [host, path] = repository.slice(4).split(":", 2);
		return `https://${host}/${path.replace(/\.git$/, "")}`;
	}
	if (repository.startsWith("ssh://")) {
		try {
			const parsed = new URL(repository);
			return `https://${parsed.hostname}${parsed.pathname.replace(/\.git$/, "")}`;
		} catch {
			return undefined;
		}
	}
	return undefined;
}

// workersRequestReview was removed; old configs may still carry it, so drop it on save.
function withoutLegacyKeys(config: ProjectConfig): ProjectConfig {
	const { workersRequestReview: _legacy, ...rest } = config as ProjectConfig & { workersRequestReview?: boolean };
	return rest;
}

function scratchSupportedConfig(config: ProjectConfig): ProjectConfig {
	const {
		defaultBranch: _defaultBranch,
		reviewers: _reviewers,
		autoReview: _legacyAutoReview,
		noCI: _noCI,
		trackerIntake: _trackerIntake,
		...supported
	} = config as ProjectConfig;
	return supported;
}

function blankToUndefined<T extends object>(obj: T): T | undefined {
	return Object.values(obj).some((v) => v !== undefined) ? obj : undefined;
}

function buildRoleAgentConfig(
	existing: components["schemas"]["AgentConfig"] | undefined,
	model: string,
	mode: string,
	effort: string,
	permissions: string,
): components["schemas"]["AgentConfig"] | undefined {
	const next = { ...existing };
	if (model) next.model = model;
	else delete next.model;
	if (mode) next.mode = mode;
	else delete next.mode;
	if (effort) next.effort = effort;
	else delete next.effort;
	if (permissions) next.permissions = permissions as components["schemas"]["AgentConfig"]["permissions"];
	else delete next.permissions;
	return Object.keys(next).length > 0 ? next : undefined;
}
