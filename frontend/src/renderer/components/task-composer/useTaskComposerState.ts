import { useQuery, useQueryClient } from "@tanstack/react-query";
import { type TaskComposerModelCatalog } from "@tallpine/product-ui";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../../api/schema";
import { apiClient, apiErrorCode, apiErrorMessage } from "../../lib/api-client";
import { clientForHost } from "../../lib/host-clients";
import { useConnectedHosts } from "../../hooks/useHostConnection";
import { cacheAgentReadiness, ensureAgentReadiness, useAgentReadinessQuery } from "../../hooks/useAgentReadinessQuery";
import { useFileAttachments } from "../../hooks/useFileAttachments";
import { useSettings } from "../../hooks/useSettings";
import { useOfferedModels } from "../../hooks/useOfferedModels";
import { isConcreteModelID, isModelSelectable, modelChoiceLabel, splitClaudeModels } from "../../lib/agent-model-choices";
import { collapseModelVariants, findModelVariant, listsModelVariants, pickModelVariant } from "../../lib/model-variants";
import { fallbackEffort } from "../../lib/effort";
import { buildRankedAgentOptions, DEFAULT_AGENT_PRIORITY_RANK, isLaunchableAgent, isReadyAgent } from "../../lib/agent-select-options";
import { agentModelsQueryKey, agentModelsQueryOptions, modelCatalogAuthIssue, revalidateAgentModels } from "../../hooks/useAgentModelsQuery";
import { useModelCatalogAuthRecovery } from "../../hooks/useModelCatalogAuthRecovery";
import { useUiStore } from "../../stores/ui-store";
import { STANDALONE_WORKSPACE_ID } from "../../types/workspace";
import { type EffortAvailability } from "../settings/EffortPicker";
import { useModelTuning } from "../settings/ModelTuningControls";
import { readTaskComposerPreferences, type TaskComposerAgentPreference } from "../../lib/task-composer-preferences";
import { TaskComposerProps, FallbackAction, CreateTaskInput, TaskCreateError, READINESS_RECONCILE_CODES, Project, cancelTaskPreparation } from "./task-composer-model";

/** Agent, model and effort selection state, queries and task creation for the TaskComposer. */
export function useTaskComposerState(props: TaskComposerProps) {
	const {
		projectId,
		hostId,
	} = props;
	const { t } = useTranslation();
	const taskPlaceholder = useMemo(() => {
		const placeholders = t("newTask.taskPlaceholders" as never, { returnObjects: true }) as string[];
		return Array.isArray(placeholders)
			? (placeholders[Math.floor(Math.random() * placeholders.length)] ?? "")
			: "";
	}, [t]);
	const queryClient = useQueryClient();
	const remoteConnections = useConnectedHosts();
	const hostConnected = !hostId || remoteConnections.includes(hostId);
	const [isPromptDirty, setIsPromptDirty] = useState(false);
	const [model, setModel] = useState("");
	const [mode, setMode] = useState("");
	const [effort, setEffort] = useState("");
	const [agent, setAgent] = useState("");
	const [agentTouched, setAgentTouched] = useState(false);
	const [modelTouched, setModelTouched] = useState(false);
	const [effortTouched, setEffortTouched] = useState(false);
	const [isSubmitting, setIsSubmitting] = useState(false);
	const [error, setError] = useState<string | undefined>();
	const [fallbackAction, setFallbackAction] = useState<FallbackAction>();
	const taskPreparationRef = useRef("");
	const requestRef = useRef<{ payload: string; id: string } | undefined>(undefined);
	const {
		attachments,
		error: attachmentError,
		addFiles,
		remove: removeAttachment,
		clear: clearAttachments,
		toSettledPayload,
	} = useFileAttachments();
	const isStandalone = projectId === STANDALONE_WORKSPACE_ID;
	const preferenceContext = hostId ? `${hostId}:${projectId ?? ""}` : projectId ?? "";
	const persistedPreferences = useMemo(
		() => readTaskComposerPreferences(preferenceContext),
		[preferenceContext],
	);
	const agentDrafts = useRef<Record<string, TaskComposerAgentPreference>>({
		...persistedPreferences?.agents,
	}).current;
	const createDaemonTask = useCallback(
		async (input: CreateTaskInput): Promise<string> => {
			try {
				const { data, error } = await (hostId ? clientForHost(hostId) : apiClient).POST("/api/v1/orchestrators/delegate", {
					headers: input.attachments?.length ? { "X-AO-Attachment-Upload": "1" } : undefined,
					body: {
						clientRequestId: input.clientRequestId,
						projectId: input.projectId,
						brief: input.brief,
						agent: input.agent,
						...(input.model ? { model: input.model } : {}),
						...(input.effort !== undefined ? { effort: input.effort } : {}),
						...(input.mode ? { mode: input.mode } : {}),
						...(input.approvalMode ? { approvalMode: input.approvalMode } : {}),
						...(input.attachments && input.attachments.length > 0 ? { attachments: input.attachments } : {}),
						...(input.taskPreparation ? { taskPreparation: input.taskPreparation } : {}),
						...(input.backlogRef ? { backlogRef: input.backlogRef, ...(input.backlogForce ? { backlogForce: true } : {}) } : {}),
					},
				});
				if (error) {
					throw new TaskCreateError(
						apiErrorMessage(error, t("newTask.unableToStart")),
						apiErrorCode(error),
						error.details,
					);
				}
				if (!data?.workerId) throw new Error(t("newTask.noSession"));
				// Non-fatal notices (a backlog item with open blockers, a link that did not stick).
				for (const warning of data.warnings ?? []) useUiStore.getState().showGlobalToast(warning, undefined, "info");
				return data.workerId;
			} catch (err) {
				if (
					err instanceof TaskCreateError &&
					err.code &&
					READINESS_RECONCILE_CODES.has(err.code) &&
					input.agent
				) {
					try {
						cacheAgentReadiness(queryClient, await ensureAgentReadiness([input.agent], "launch", hostId), hostId);
					} catch {
						// Preserve the launch error when opportunistic reconciliation fails.
					}
				}
				throw err instanceof Error ? err : new Error(t("newTask.unableToStart"));
			}
		},
		[hostId, queryClient, t],
	);

	const createStandaloneTask = useCallback(
		async (input: CreateTaskInput): Promise<string> => {
			try {
			const displayName = input.brief.trim().slice(0, 100) || input.agent || "Standalone agent";
			const { data, error } = await (hostId ? clientForHost(hostId) : apiClient).POST("/api/v1/sessions", {
				headers: input.attachments?.length ? { "X-AO-Attachment-Upload": "1" } : undefined,
				body: {
					clientRequestId: input.clientRequestId,
					kind: "worker",
					harness: input.agent as components["schemas"]["SpawnSessionRequest"]["harness"],
					prompt: input.brief,
					displayName,
					model: input.model,
					...(input.effort ? { effort: input.effort } : {}),
					...(input.mode ? { mode: input.mode } : {}),
					...(input.approvalMode ? { approvalMode: input.approvalMode } : {}),
					...(input.attachments && input.attachments.length > 0 ? { attachments: input.attachments } : {}),
				},
			});
			if (error) {
				throw new TaskCreateError(apiErrorMessage(error, t("newTask.unableToStart")), apiErrorCode(error), error.details);
			}
			if (!data?.session.id) throw new Error(t("newTask.noSession"));
			return data.session.id;
			} catch (err) {
				throw err instanceof Error ? err : new Error(t("newTask.unableToStart"));
			}
		},
		[hostId, t],
	);

	const createTask = useCallback(
		(input: CreateTaskInput): Promise<string> =>
			isStandalone ? createStandaloneTask(input) : createDaemonTask(input),
		[isStandalone, createStandaloneTask, createDaemonTask],
	);

	const projectQuery = useQuery({
		queryKey: hostId ? ["project", hostId, projectId] : ["project", projectId],
		enabled: Boolean(projectId) && !isStandalone,
		queryFn: async () => {
			const { data, error: apiError } = await (hostId ? clientForHost(hostId) : apiClient).GET("/api/v1/projects/{id}", {
				params: { path: { id: projectId ?? "" } },
			});
			if (apiError) throw new Error(apiErrorMessage(apiError));
			if (data?.status !== "ok") throw new Error(t("newTask.configUnavailable"));
			return data.project as Project;
		},
	});
	useEffect(() => {
		const id = projectQuery.data?.id;
		if (!id) return;
		let client;
		try {
			client = hostId ? clientForHost(hostId) : apiClient;
		} catch {
			return;
		}
		let disposed = false;
		taskPreparationRef.current = "";
		void client.POST("/api/v1/projects/{id}/tasks/prepare", {
			params: { path: { id } },
		}).then(
			({ data }) => {
				const token = data?.taskPreparation ?? "";
				if (!token) return;
				if (disposed) {
					cancelTaskPreparation(token, hostId);
					return;
				}
				taskPreparationRef.current = token;
			},
			() => undefined,
		);
		return () => {
			disposed = true;
			const token = taskPreparationRef.current;
			taskPreparationRef.current = "";
			cancelTaskPreparation(token, hostId);
		};
	}, [hostId, projectQuery.data?.id]);
	const agentsQuery = useAgentReadinessQuery(true, hostId);
	const { settings, error: settingsError } = useSettings(hostId);
	// The composer preselects the agent and model a spawn would actually use
	// instead of parking the controls on a "default" label the user has to
	// remember. Both resolved values remain directly editable.
	const projectConfig = projectQuery.data?.config as
		| {
				worker?: { agent?: string; agentConfig?: { model?: string; mode?: string; effort?: string } };
				agentConfig?: { model?: string; mode?: string; effort?: string };
		  }
		| undefined;
	const projectWorkerAgent = projectConfig?.worker?.agent ?? "";
	const globalDefaultAgent = projectQuery.data?.agent ?? "";
	const configuredProjectAgent = projectWorkerAgent || globalDefaultAgent;
	const agentCatalog = agentsQuery.data;
	const standaloneDefaultAgent = useMemo(() => {
		if (!isStandalone || !agentCatalog) return "";
		return (
			buildRankedAgentOptions({
				agents: agentCatalog.agents,
				priorityRank: DEFAULT_AGENT_PRIORITY_RANK,
				fallbackAgents: [],
			}).find(isReadyAgent)?.id ?? ""
		);
	}, [agentCatalog, isStandalone]);
	const configuredDefaultAgent = isStandalone
		? standaloneDefaultAgent
		: configuredProjectAgent;
	const rememberedAgent = persistedPreferences?.lastAgent ?? "";
	const availableAgents = agentCatalog?.agents;
	const rememberedAgentIsAvailable = Boolean(
		availableAgents?.some((candidate) => candidate.id === rememberedAgent && (hostId ? isLaunchableAgent(candidate) : isReadyAgent(candidate))),
	);
	const defaultWorkerAgent = rememberedAgentIsAvailable ? rememberedAgent : configuredDefaultAgent;
	const selectedAgent = agent || defaultWorkerAgent;
	// Model catalogs are device-wide, so every project reads the one catalog per agent.
	const modelScope = "";
	const defaultWorkerModel =
		projectConfig?.worker?.agentConfig?.model ?? projectConfig?.agentConfig?.model ?? "";
	const defaultWorkerMode = projectConfig?.worker?.agentConfig?.mode ?? projectConfig?.agentConfig?.mode ?? "";
	const defaultWorkerEffort =
		projectConfig?.worker?.agentConfig?.effort ?? projectConfig?.agentConfig?.effort ?? "";
	const projectModelForSelectedAgent = selectedAgent === configuredProjectAgent
		? defaultWorkerModel
		: "";
	const projectModeForSelectedAgent = selectedAgent === configuredProjectAgent
		? defaultWorkerMode
		: "";
	// Shares the picker's query key, so this is the same fetch, not a second one.
	const modelCatalogQuery = useQuery(agentModelsQueryOptions(selectedAgent, modelScope, hostId));
	const revalidationQuery = useQuery({
		queryKey: [
			"agent-model-revalidation",
			hostId ?? "",
			selectedAgent,
			modelScope,
			modelCatalogQuery.data?.validatedAt ?? "",
		],
		queryFn: () => revalidateAgentModels(selectedAgent, modelScope, hostId),
		enabled: selectedAgent !== "" && modelCatalogQuery.data?.refreshRecommended === true,
		staleTime: Number.POSITIVE_INFINITY,
		retry: false,
	});
	useEffect(() => {
		if (revalidationQuery.data) {
			queryClient.setQueryData(
				agentModelsQueryKey(selectedAgent, modelScope, hostId),
				revalidationQuery.data,
			);
		}
	}, [hostId, modelScope, queryClient, revalidationQuery.data, selectedAgent]);
	const revalidationWarning = revalidationQuery.isError
		? revalidationQuery.error instanceof Error
			? revalidationQuery.error.message
			: t("settings.models.validateFailed")
		: undefined;
	const modelWarningText =
		revalidationWarning ??
		modelCatalogQuery.data?.warning ??
		(modelCatalogQuery.isError
			? modelCatalogQuery.error instanceof Error
				? modelCatalogQuery.error.message
				: t("settings.models.loadFailed")
			: undefined);
	// A login problem behind the catalog warning gets a plain-language notice
	// with the fix in place. 
	const modelAuthIssue = revalidationWarning === undefined ? modelCatalogAuthIssue(modelCatalogQuery.data) : undefined;
	useModelCatalogAuthRecovery(selectedAgent, hostId, modelAuthIssue);
	// Settings → Harness → Models picks the models this machine offers.
	const { models: offeredModels } = useOfferedModels(selectedAgent, modelCatalogQuery.data?.models, {
		hostId,
		keep: [model, mode, projectModelForSelectedAgent, projectModeForSelectedAgent],
	});
	const modelCatalog: TaskComposerModelCatalog | undefined = modelCatalogQuery.data
		? {
				allowCustom: modelCatalogQuery.data.allowCustom,
				customModelEntry: modelCatalogQuery.data.customModelEntry,
				models: offeredModels,
				refreshError: modelCatalogQuery.data.refreshError,
				refreshState: modelCatalogQuery.data.refreshState,
				retryAt: modelCatalogQuery.data.retryAt,
				selectionMode: modelCatalogQuery.data.selectionMode,
			}
		: undefined;
	// An unmarked first row is not evidence of what the provider will run.
	const catalogModels = offeredModels.filter((item) => isConcreteModelID(item.id));
	// Automatic choices never land on a model the picker shows as unavailable.
	const selectableModels = catalogModels.filter(isModelSelectable);
	const catalogDefaultOption =
		selectableModels.find((item) => item.isDefault)?.id ?? "";
	const catalogDefaultHidden = Boolean(modelCatalogQuery.data?.models?.some(
		(item) => item.isDefault && isConcreteModelID(item.id) && !catalogModels.some((offered) => offered.id === item.id),
	));
	const catalogUsesModes = modelCatalogQuery.data?.selectionMode === "mode";
	// The model this user ran most recently.
	const lastUsedOption =
		selectableModels
			.filter((item) => item.lastUsedAt)
			.sort((a, b) => Date.parse(b.lastUsedAt ?? "") - Date.parse(a.lastUsedAt ?? ""))[0]?.id ?? "";
	// With nothing remembered, run, or configured, Claude Code opens on the newest Opus.
	const claudeFallbackOption =
		selectedAgent === "claude-code" && !catalogUsesModes
			? (splitClaudeModels(selectableModels).current.find((item) => /opus/i.test(modelChoiceLabel(item)))?.id ?? "")
			: "";
	const rememberedConfigForSelectedAgent = agentDrafts[selectedAgent];
	const rememberedModel = rememberedConfigForSelectedAgent?.model ?? "";
	const rememberedMode = rememberedConfigForSelectedAgent?.mode ?? "";
	const rememberedModelIsValid =
		isConcreteModelID(rememberedModel) &&
		!catalogModels.some((item) => item.id === rememberedModel && !isModelSelectable(item)) &&
		Boolean(
			modelCatalogQuery.data &&
				(modelCatalogQuery.data.allowCustom || catalogModels.some((item) => item.id === rememberedModel)),
		);
	const rememberedModeIsValid =
		isConcreteModelID(rememberedMode) && catalogModels.some((item) => item.id === rememberedMode);
	const defaultModelForSelectedAgent =
		(rememberedModelIsValid ? rememberedModel : "") ||
		(isConcreteModelID(projectModelForSelectedAgent) ? projectModelForSelectedAgent : "") ||
		(!catalogUsesModes && isConcreteModelID(lastUsedOption) ? lastUsedOption : "") ||
		(catalogUsesModes ? "" : catalogDefaultOption) ||
		claudeFallbackOption ||
		// Only replace a default the catalog identified and Settings hid. An
		// unreported default must keep the native model/provider selection.
		(!catalogUsesModes && catalogDefaultHidden ? selectableModels[0]?.id ?? "" : "");
	const defaultModeForSelectedAgent =
		(rememberedModeIsValid ? rememberedMode : "") ||
		(isConcreteModelID(projectModeForSelectedAgent) ? projectModeForSelectedAgent : "") ||
		(catalogUsesModes && isConcreteModelID(lastUsedOption) ? lastUsedOption : "") ||
		(catalogUsesModes ? catalogDefaultOption : "");
	const selectedModel = model || (modelTouched ? (catalogUsesModes ? "" : catalogDefaultOption) : defaultModelForSelectedAgent);
	const selectedMode = mode || (modelTouched ? (catalogUsesModes ? catalogDefaultOption : "") : defaultModeForSelectedAgent);
	const selectedModelOrMode = (selectedModel || selectedMode).trim();
	const projectModelOrMode = projectModelForSelectedAgent || projectModeForSelectedAgent;
	const requestedModel = isStandalone
		// Standalone sessions have no project config to inherit. If the UI shows a
		// concrete catalog default, send it explicitly; otherwise the native agent
		// may launch with a different harness-owned default than the one displayed.
		? (isConcreteModelID(selectedModelOrMode) ? selectedModelOrMode : undefined)
		: selectedModelOrMode && selectedModelOrMode !== projectModelOrMode && (
				selectedModelOrMode !== catalogDefaultOption || isConcreteModelID(projectModelOrMode) ||
				(!catalogUsesModes && !projectModelOrMode)
			) ? selectedModelOrMode : undefined;
	const rememberedEffortIsExplicit = Boolean(
		rememberedConfigForSelectedAgent &&
			Object.prototype.hasOwnProperty.call(rememberedConfigForSelectedAgent, "effort"),
	);
	const defaultEffortForSelectedAgent = rememberedEffortIsExplicit
		? (rememberedConfigForSelectedAgent?.effort ?? "")
		: selectedAgent === configuredProjectAgent
			? defaultWorkerEffort
			: "";
	const { selected: effortModel } = useModelTuning({
		models: catalogModels,
		model: selectedModel,
		effort,
		onEffortChange: setEffort,
		onEffortReset: setEffort,
	});
	const effortOptions = effortModel?.efforts?.filter((option) => option && option.toLowerCase() !== "default") ?? [];
	const inheritedEffort = selectedAgent === configuredProjectAgent ? defaultWorkerEffort : "";
	const implicitEffort = inheritedEffort || effortModel?.defaultEffort || "";
	const selectedAgentLabel = agentCatalog?.agents.find((item) => item.id === selectedAgent)?.label || selectedAgent;
	const chatHarnesses = settings?.chatHarnesses;
	const requiresTuiFallback =
		selectedAgent !== "" &&
		settings?.defaultSessionMode === "chat" &&
		!(chatHarnesses ?? []).includes(selectedAgent);
	// With no provider default for the reported levels Tallpine picks one, shows it as
	// selected, and sends it, so the picker matches what the task runs with.
	const tallpineDefaultEffort = requiresTuiFallback ? undefined : fallbackEffort(effortOptions, implicitEffort);
	const effectiveEffort = effort && effort !== "default"
		? effort
		: tallpineDefaultEffort ?? (isStandalone && effortOptions.includes(implicitEffort) ? implicitEffort : "");
	// Cursor's effort and thinking are part of its model id; its effort button swaps the id.
	const cursorFamilies = listsModelVariants(selectedAgent)
		? collapseModelVariants((modelCatalog?.models ?? []).filter((item) => isConcreteModelID(item.id)))
		: undefined;
	const cursorChoice = cursorFamilies ? findModelVariant(cursorFamilies, selectedModel) : undefined;
	const pickCursorVariant = (want: { effort?: string; thinking?: boolean }) => {
		if (!cursorChoice) return;
		setModel(pickModelVariant(cursorChoice.family, { ...cursorChoice.variant, ...want }).id);
		setModelTouched(true);
	};
	const requestedEffort = isStandalone
		// Standalone sessions have no project role to inherit from. Send the
		// concrete effort shown by the picker even when the UI is retrying in TUI
		// mode, otherwise the native harness may use a different default.
		? (effectiveEffort || undefined)
		: effortTouched || rememberedEffortIsExplicit
			? !effectiveEffort || effectiveEffort === implicitEffort ? undefined : effectiveEffort
			: tallpineDefaultEffort;
	const effortAvailability: EffortAvailability = requiresTuiFallback
		? "launch-unavailable"
		: !effortModel || effortModel.efforts === undefined
			? "unknown"
			: effortOptions.length > 0
				? "supported"
				: "unsupported";
	const canSubmit =
		hostConnected &&
		Boolean(projectId) &&
		(!isStandalone || selectedAgent !== "") &&
		(!hostId || Boolean(agentCatalog?.agents.some((candidate) => candidate.id === selectedAgent && isLaunchableAgent(candidate)))) &&
		(isStandalone || projectQuery.data !== undefined) &&
		(!hostId || (agentsQuery.isSuccess && settings !== undefined));
	const remoteLoadError = !hostId ? undefined : !hostConnected ? t("remote.hostOffline") :
		[projectQuery.error, agentsQuery.error].find((cause): cause is Error => cause instanceof Error)?.message ?? settingsError ??
			(agentsQuery.isSuccess && !agentCatalog?.agents.some(isLaunchableAgent) ? t("remote.noReadyAgent") : undefined);
	return {
		addFiles,
		agent,
		agentCatalog,
		agentDrafts,
		agentTouched,
		agentsQuery,
		attachmentError,
		attachments,
		canSubmit,
		catalogDefaultOption,
		clearAttachments,
		createTask,
		cursorChoice,
		defaultEffortForSelectedAgent,
		defaultModeForSelectedAgent,
		defaultModelForSelectedAgent,
		defaultWorkerAgent,
		effectiveEffort,
		effort,
		effortAvailability,
		effortModel,
		effortOptions,
		effortTouched,
		error,
		fallbackAction,
		inheritedEffort,
		isPromptDirty,
		isStandalone,
		isSubmitting,
		mode,
		model,
		modelAuthIssue,
		modelCatalog,
		modelCatalogQuery,
		modelScope,
		modelTouched,
		modelWarningText,
		pickCursorVariant,
		preferenceContext,
		projectModelOrMode,
		queryClient,
		remoteLoadError,
		removeAttachment,
		requestRef,
		requestedEffort,
		requestedModel,
		requiresTuiFallback,
		selectedAgent,
		selectedAgentLabel,
		selectedMode,
		selectedModel,
		setAgent,
		setAgentTouched,
		setEffort,
		setEffortTouched,
		setError,
		setFallbackAction,
		setIsPromptDirty,
		setIsSubmitting,
		setMode,
		setModel,
		setModelTouched,
		settings,
		t,
		taskPlaceholder,
		taskPreparationRef,
		toSettledPayload,
	};
}
