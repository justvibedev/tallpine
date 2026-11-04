import { clientForSessionHost } from "../../lib/host-clients";
import { apiClient, apiErrorMessage } from "../../lib/api-client";
import { tallpineBridge } from "../../lib/bridge";
import type { CreateProjectFlowProps } from "./create-project-props";
import { useCreateProjectState } from "./useCreateProjectState";
import { initialCloneDetails, GitPreparationEvent } from "./create-project-types";
import { importNeedsRemoteSetup, normalizeGitHubRepositoryName, githubRepositoryRemoteUrl, isValidProjectRemote, mergePreparationEvents, projectRequestedActionEvents, orderedProjectActions, projectPreparationFailureMessage, importValidationMessage, persistSuggestedProjectRemoteUrl } from "./create-project-helpers";

/** Clone dialog exit, workspace fallback and git preparation for the create-project flow. */
export function useCreateProjectClone(props: CreateProjectFlowProps, state: ReturnType<typeof useCreateProjectState>) {
	const {
		connected = true,
		hostId,
		onDismiss,
	} = props;
	const {
		abandonPreparedClone,
		chooseDirectory,
		hasModePicker,
		isCreating,
		isInitializing,
		isPreparingGit,
		projectApprovedActions,
		projectGitHubRepo,
		projectRemoteUrl,
		projectValidation,
		reportProjectError,
		resolvedIdleLabel,
		setCloneDetails,
		setCloneDialogOpen,
		setCloneSelection,
		setError,
		setIsPreparingGit,
		setModePickerOpen,
		setPendingDropPath,
		setProjectApprovedActions,
		setProjectImportStep,
		setProjectPrepEvents,
		setProjectValidation,
		setSelectedPath,
		t,
	} = state;
	const leaveCloneDialog = async (back: boolean) => {
		if (!(await abandonPreparedClone())) return;
		setError(null);
		setCloneDialogOpen(false);
		setCloneSelection(null);
		setCloneDetails(initialCloneDetails(hostId));
		if (back) setModePickerOpen(true);
		else onDismiss?.();
	};

	const tryProjectAsWorkspace = () => {
		if (!projectValidation) return;
		setPendingDropPath(null);
		void chooseDirectory("workspace", projectValidation.root.repoPath);
	};

	const prepareProjectGit = async () => {
		if (!projectValidation) return;
		if (hostId && !connected) {
			reportProjectError(t("remote.connectBeforeStart"));
			return;
		}
		setError(null);
		const needsRemoteSetup = importNeedsRemoteSetup(projectValidation.root.requiredActions) || projectApprovedActions.includes("create_remote_repository");
		const githubRepository = needsRemoteSetup && projectGitHubRepo
			? { ...projectGitHubRepo, name: normalizeGitHubRepositoryName(projectGitHubRepo.name) }
			: null;
		const remoteUrl = githubRepository ? githubRepositoryRemoteUrl(githubRepository) : projectRemoteUrl.trim();
		if (remoteUrl !== "" && !isValidProjectRemote(remoteUrl)) {
			reportProjectError(t("createProject.cloneInvalidUrl"));
			return;
		}
		if (!hostId && projectApprovedActions.includes("set_remote") && remoteUrl !== "") {
			setIsPreparingGit(true);
			try {
				if (!(await tallpineBridge.app.checkGitRepository(remoteUrl))) {
					reportProjectError(t("createProject.cloneRepositoryUnavailable", {
						defaultValue: "This isn't a repository or you don't have access",
					}));
					return;
				}
			} catch {
				reportProjectError(t("createProject.cloneRepositoryUnavailable", {
					defaultValue: "This isn't a repository or you don't have access",
				}));
				return;
			} finally {
				setIsPreparingGit(false);
			}
		}
		setProjectPrepEvents((current) => mergePreparationEvents(current, projectRequestedActionEvents(
			projectValidation.root.repoPath,
			projectValidation.root.requiredActions,
		)));
		setIsPreparingGit(true);
		try {
			let currentValidation = projectValidation;
			while (currentValidation.nextStep === "prepare_git") {
				const activeAction = orderedProjectActions(currentValidation.root.requiredActions)[0];
				if (!activeAction) throw new Error(t("createProject.couldNotAdd"));
				setProjectPrepEvents((current) => mergePreparationEvents(current, [{
					repoPath: currentValidation.root.repoPath,
					action: activeAction as GitPreparationEvent["action"],
					state: "running",
				}]));
				const { data, error: apiError } = await (hostId ? clientForSessionHost(hostId) : apiClient).POST("/api/v1/imports/prepare-git", {
					body: {
						importKind: "project",
						path: currentValidation.root.repoPath,
							approvedActions: projectApprovedActions,
							remoteUrl: remoteUrl || undefined,
							githubRepository: githubRepository ?? undefined,
							stepwise: true,
						},
				});
				if (apiError || !data) throw new Error(apiErrorMessage(apiError, t("createProject.couldNotAdd")));
				setProjectPrepEvents((current) => mergePreparationEvents(current, data.events));
				setProjectValidation(data.validation);
				setProjectApprovedActions(data.validation.root.requiredActions);
				const failed = data.events.find((event) => event.state === "error");
				if (failed) {
					reportProjectError(projectPreparationFailureMessage(failed));
					return;
				}
				if (!data.validation.isValid || data.validation.nextStep === "error") {
					reportProjectError(importValidationMessage(data.validation));
					setProjectImportStep("blocked");
					return;
				}
				if (data.validation.nextStep !== "prepare_git" && data.validation.nextStep !== "continue") {
					throw new Error(t("createProject.couldNotAdd"));
				}
				if (data.validation.nextStep === "prepare_git" && orderedProjectActions(data.validation.root.requiredActions)[0] === activeAction) {
					throw new Error(t("createProject.couldNotAdd"));
				}
				currentValidation = data.validation;
			}
			if (remoteUrl !== "" && !hostId) persistSuggestedProjectRemoteUrl(remoteUrl);
			setModePickerOpen(false);
			setProjectImportStep(null);
			setSelectedPath(currentValidation.root.repoPath);
		} catch (err) {
			reportProjectError(err instanceof Error ? err.message : t("createProject.couldNotAdd"));
		} finally {
			setIsPreparingGit(false);
		}
	};

	const label = isInitializing
		? hasModePicker
			? t("createProject.initializing")
			: t("createProject.settingUp")
		: isPreparingGit
			? t("createProject.settingUp")
		: isCreating
			? t("createProject.creating")
			: resolvedIdleLabel;

	return {
		label,
		leaveCloneDialog,
		prepareProjectGit,
		tryProjectAsWorkspace,
	};
}
