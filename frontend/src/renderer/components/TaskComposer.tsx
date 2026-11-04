import { DesktopAgentControl, TaskEffortPicker, TaskModelPicker } from "./task-composer/TaskComposerPickers";
import { useTaskComposerState } from "./task-composer/useTaskComposerState";
import { useTaskComposerActions } from "./task-composer/useTaskComposerActions";
import type { TaskComposerProps } from "./task-composer/task-composer-model";
import { TaskComposerView } from "@tallpine/product-ui";
import { isConcreteModelID } from "../lib/agent-model-choices";
import { ToggleRow } from "./chat/AgentSwitchPicker";

export function TaskComposer(props: TaskComposerProps) {
	const {
		projectId,
		hostId,
		autoFocusTitle,
		createLabel,
		backlog,
	} = props;
	const state = useTaskComposerState(props);
	const actions = useTaskComposerActions(props, state);
	const {
		addFiles,
		agentCatalog,
		agentDrafts,
		agentsQuery,
		attachmentError,
		attachments,
		canSubmit,
		catalogDefaultOption,
		cursorChoice,
		effectiveEffort,
		effort,
		effortAvailability,
		effortModel,
		effortOptions,
		error,
		fallbackAction,
		inheritedEffort,
		isStandalone,
		isSubmitting,
		modelCatalog,
		modelCatalogQuery,
		pickCursorVariant,
		projectModelOrMode,
		remoteLoadError,
		removeAttachment,
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
		setMode,
		setModel,
		setModelTouched,
		t,
		taskPlaceholder,
	} = state;
	const {
		handlePromptChange,
		modelWarning,
		refreshSelectedModels,
		submitTask,
	} = actions;

	return (
		<TaskComposerView
			autoFocusPrompt={autoFocusTitle}
			initialPrompt={backlog ? `${backlog.ref}: ${backlog.title}` : undefined}
			canSubmit={canSubmit}
			onPromptChange={handlePromptChange}
			labels={{
				addFile: t("newTask.addFile"),
				effort: t("settings.models.effort"),
				fallbackAction: fallbackAction === "bypass-permissions"
					? t("newTask.startWithoutApprovals", { defaultValue: "Start without approvals" })
					: t("newTask.createAsTui"),
				removeFile: (name) => t("newTask.removeFile", { name }),
				runsWith: t("newTask.runsWith"),
				start: createLabel ?? t("newTask.start"),
				starting: t("newTask.starting"),
				task: t("newTask.task"),
				taskPlaceholder,
			}}
			agent={{
				label: t("newTask.agent"),
				placeholder: t("newTask.selectAgent"),
				value: selectedAgent,
				agents: agentCatalog?.agents,
				disabled:
				isSubmitting || (agentsQuery.isFetching && agentCatalog === undefined),
				onChange: (value) => {
					if (selectedAgent) {
						agentDrafts[selectedAgent] = {
							model: selectedModel ? requestedModel ?? "" : "",
							mode: selectedMode ? requestedModel ?? "" : "",
					...(requestedEffort !== undefined ? { effort: requestedEffort } : {}),
						};
					}
					setAgent(value);
					setAgentTouched(true);
					setModel("");
					setMode("");
					setEffort("");
					setModelTouched(false);
					setEffortTouched(false);
				},
			}}
			model={{
				agentId: selectedAgent,
				agentLabel: selectedAgentLabel,
				projectId: isStandalone ? "" : (projectId ?? ""),
				disabled: isSubmitting,
				value: selectedModel,
				mode: selectedMode,
				catalog: modelCatalog,
				fetching: modelCatalogQuery.isFetching,
				onModelChange: (value) => {
					setModel(value);
					setMode("");
					setModelTouched(true);
					// Effort levels are per-model, so a level the newly chosen model
					// does not advertise has to be dropped rather than carried over.
					const nextEfforts =
						modelCatalog?.models?.find((item) => item.id === value)?.efforts ?? [];
					setEffort((current) => (current !== "" && !nextEfforts.includes(current) ? "" : current));
				},
				onModeChange: (value) => {
					setMode(value);
					setModel("");
					setModelTouched(true);
					// A mode replaces the model entirely, so no model vouches for a
					// previously chosen level any more.
					setEffort("");
				},
			}}
			effort={cursorChoice ? {
				disabled: isSubmitting,
				options: cursorChoice.family.efforts,
				value: cursorChoice.variant.effort ?? "",
				onChange: (value) => pickCursorVariant({ effort: value }),
			} : {
				disabled: isSubmitting,
				options: effortOptions,
				value: effectiveEffort,
				onChange: (value) => {
					setEffort(value);
					setEffortTouched(true);
				},
			}}
			attachments={{
				items: attachments.map(({ id, name, dataUrl }) => ({ id, name, previewUrl: dataUrl })),
				error: attachmentError,
				onAddFiles: (files) => void addFiles(files),
				onRemove: removeAttachment,
			}}
			submission={{
				showFallbackAction: fallbackAction !== undefined,
				error: error ?? remoteLoadError ?? undefined,
				isSubmitting,
				modelWarning,
				onFallbackAction: (brief) =>
					void (fallbackAction === "bypass-permissions"
						? submitTask(brief, undefined, "bypass-permissions")
						: submitTask(brief, "tui")),
				onSubmit: (brief) => void submitTask(brief, requiresTuiFallback ? "tui" : undefined),
			}}
			renderAgentControl={(control) => <DesktopAgentControl {...control} hostId={hostId} />}
			renderEffortControl={(control) => <TaskEffortPicker {...control} value={control.value} defaultEffort={cursorChoice ? undefined : inheritedEffort || effortModel?.defaultEffort} availability={cursorChoice ? "supported" : effortAvailability}
				footer={cursorChoice?.family.hasThinkingChoice ? <ToggleRow label={t("agentSwitch.thinking")} checked={cursorChoice.variant.thinking} onChange={(thinking) => pickCursorVariant({ thinking })} /> : undefined} />}
			renderModelControl={(control) => <TaskModelPicker {...control} onRefresh={refreshSelectedModels}
				showFollowAgentAction={Boolean(catalogDefaultOption || !isConcreteModelID(projectModelOrMode))} />}
			showEffort={cursorChoice ? cursorChoice.family.efforts.length > 1 : !requiresTuiFallback && (effortOptions.length > 0 || Boolean(effort && effort !== "default"))}
		/>
	);
}
