import { useCallback, useEffect } from "react";
import { cacheAgentReadiness, ensureAgentReadiness } from "../../hooks/useAgentReadinessQuery";
import { agentModelsQueryKey, refreshAgentModels } from "../../hooks/useAgentModelsQuery";
import { useUiStore } from "../../stores/ui-store";
import { ModelCatalogNotice } from "../ModelCatalogNotice";
import { rememberTaskComposerPreference, type TaskComposerAgentPreference } from "../../lib/task-composer-preferences";
import { useTaskComposerState } from "./useTaskComposerState";
import { TaskComposerProps, CreateTaskInput, cancelTaskPreparation, TaskCreateError, hasErrorDetail, CHAT_PREFLIGHT_CODES } from "./task-composer-model";

/** Side effects, dirty/submitting reporting and submit handling for the TaskComposer. */
export function useTaskComposerActions(props: TaskComposerProps, state: ReturnType<typeof useTaskComposerState>) {
	const {
		projectId,
		hostId,
		onCreated,
		onDirtyChange,
		onSubmittingChange,
		backlog,
	} = props;
	const {
		agentDrafts,
		agentTouched,
		attachments,
		canSubmit,
		clearAttachments,
		createTask,
		defaultEffortForSelectedAgent,
		defaultModeForSelectedAgent,
		defaultModelForSelectedAgent,
		defaultWorkerAgent,
		effortTouched,
		isPromptDirty,
		isStandalone,
		isSubmitting,
		modelAuthIssue,
		modelScope,
		modelTouched,
		modelWarningText,
		preferenceContext,
		queryClient,
		requestRef,
		requestedEffort,
		requestedModel,
		selectedAgent,
		selectedAgentLabel,
		selectedMode,
		selectedModel,
		setAgent,
		setEffort,
		setError,
		setFallbackAction,
		setIsPromptDirty,
		setIsSubmitting,
		setMode,
		setModel,
		t,
		taskPreparationRef,
		toSettledPayload,
	} = state;
	const refreshSelectedModels = useCallback(async () => {
		const refreshed = await refreshAgentModels(selectedAgent, modelScope, hostId);
		queryClient.setQueryData(agentModelsQueryKey(selectedAgent, modelScope, hostId), refreshed);
	}, [hostId, modelScope, queryClient, selectedAgent]);
	const openGlobalSettings = useUiStore((state) => state.openGlobalSettings);
	const startAgentLogin = useCallback(() => {
		openGlobalSettings("harness", {
			focusAgentId: selectedAgent,
			...(hostId ? { hostId } : {}),
			startLogin: true,
			preserveProject: true,
		});
	}, [hostId, openGlobalSettings, selectedAgent]);
	// An expired login whose refresh token is still in date needs nothing from
	// the user on this computer: its daemon lets the task start and the agent
	// renews the token as it launches. A remote host may run an older daemon
	// that still rejects the spawn, so keep the notice there.
	const loginRenewsItself = modelAuthIssue === "expired" && !hostId;
	const modelWarning = modelAuthIssue && !loginRenewsItself ? (
		<ModelCatalogNotice
			agentLabel={selectedAgentLabel}
			issue={modelAuthIssue}
			detail={modelWarningText}
			onLogin={startAgentLogin}
			onRetry={refreshSelectedModels}
		/>
	) : modelAuthIssue ? undefined : modelWarningText;
	useEffect(() => {
		if (!agentTouched) setAgent(defaultWorkerAgent);
	}, [agentTouched, defaultWorkerAgent]);
	useEffect(() => {
		if (!modelTouched) {
			setModel(defaultModelForSelectedAgent);
			setMode(defaultModeForSelectedAgent);
		}
	}, [defaultModelForSelectedAgent, defaultModeForSelectedAgent, modelTouched]);
	useEffect(() => {
		if (!effortTouched) setEffort(defaultEffortForSelectedAgent);
	}, [defaultEffortForSelectedAgent, effortTouched]);

	const isDirty = isPromptDirty || modelTouched || effortTouched || attachments.length > 0;
	const handlePromptChange = useCallback((value: string) => {
		const nextDirty = value.trim() !== "";
		setIsPromptDirty((wasDirty) => (wasDirty === nextDirty ? wasDirty : nextDirty));
	}, []);
	useEffect(() => {
		onDirtyChange?.(isDirty);
	}, [isDirty, onDirtyChange]);
	useEffect(() => () => onDirtyChange?.(false), [onDirtyChange]);

	useEffect(() => {
		onSubmittingChange?.(isSubmitting);
	}, [isSubmitting, onSubmittingChange]);
	useEffect(() => () => onSubmittingChange?.(false), [onSubmittingChange]);
	useEffect(() => () => clearAttachments(), [clearAttachments]);

	const submitTask = async (
		brief: string,
		interfaceMode?: "chat" | "tui",
		approvalMode?: "bypass-permissions",
	) => {
		if (!projectId || !canSubmit || isSubmitting) return;

		setIsSubmitting(true);
		setError(undefined);
		setFallbackAction(undefined);
		try {
			if (selectedAgent) {
				try {
					cacheAgentReadiness(queryClient, await ensureAgentReadiness([selectedAgent], "launch", hostId), hostId);
				} catch {
					// This check lacks the selected project's cwd and environment, so it
					// is advisory. The project-aware launch path remains authoritative.
				}
			}
			const attachmentPayloads = await toSettledPayload();
			const submittedPreparation = taskPreparationRef.current;
			const request: CreateTaskInput = {
				projectId,
				brief,
				agent: selectedAgent ? (selectedAgent as CreateTaskInput["agent"]) : undefined,
				model: requestedModel,
				// Only explicit Codex picks set this; agent changes reset it, and TUI retries preserve it.
				effort: requestedEffort,
				// The local daemon applies the default session mode itself.
				mode: interfaceMode,
				approvalMode,
				attachments: attachmentPayloads.length > 0 ? attachmentPayloads : undefined,
				taskPreparation: submittedPreparation || undefined,
				...(backlog && !isStandalone ? { backlogRef: backlog.ref, ...(backlog.force ? { backlogForce: true } : {}) } : {}),
			};
			const { taskPreparation: _, attachments: _attachments, ...requestPayload } = request;
			const payload = JSON.stringify({ ...requestPayload, attachmentIds: attachments.map((attachment) => attachment.id) });
			if (requestRef.current?.payload !== payload) {
				requestRef.current = { payload, id: globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random()}` };
			}
			const sessionId = await createTask({ ...request, clientRequestId: requestRef.current.id });
			const preparationAfterSubmit = taskPreparationRef.current;
			taskPreparationRef.current = "";
			// DELETE is intentionally idempotent after a successful claim. It also
			// reclaims a preparation that resolved after this submission captured its
			// token, instead of leaving that unused worktree until TTL expiry.
			cancelTaskPreparation(submittedPreparation, hostId);
			cancelTaskPreparation(preparationAfterSubmit, hostId);
			if (selectedAgent) {
				const preference: TaskComposerAgentPreference = {
					model: selectedModel ? requestedModel ?? "" : "",
					mode: selectedMode ? requestedModel ?? "" : "",
					...(requestedEffort !== undefined ? { effort: requestedEffort } : {}),
				};
				agentDrafts[selectedAgent] = preference;
				rememberTaskComposerPreference(preferenceContext, selectedAgent, preference);
			}
			onCreated(sessionId);
		} catch (err) {
			const canBypassApprovals =
				err instanceof TaskCreateError &&
				err.code === "SESSION_MODE_UNSUPPORTED" &&
				hasErrorDetail(err.details, "missingCapabilities", "approvals") &&
				hasErrorDetail(err.details, "allowedApprovalModes", "bypass-permissions");
			setFallbackAction(
				canBypassApprovals
					? "bypass-permissions"
					: interfaceMode !== "tui" &&
							err instanceof TaskCreateError &&
							Boolean(err.code && CHAT_PREFLIGHT_CODES.has(err.code))
						? "tui"
						: undefined,
			);
			setError(err instanceof Error ? err.message : t("newTask.unableToStart"));
		} finally {
			setIsSubmitting(false);
		}
	};

	return {
		handlePromptChange,
		modelWarning,
		refreshSelectedModels,
		submitTask,
	};
}
