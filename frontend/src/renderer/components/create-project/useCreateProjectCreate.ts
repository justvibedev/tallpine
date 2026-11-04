import { useEffect, useRef } from "react";
import { apiErrorCode, apiErrorMessage } from "../../lib/api-client";
import { tallpineBridge } from "../../lib/bridge";
import { useUiStore } from "../../stores/ui-store";
import { type CreateProjectAgentSelection } from "../CreateProjectAgentSheet";
import { type CloneRepositorySelection } from "../CloneRepositoryDialog";
import type { CreateProjectFlowProps } from "./create-project-props";
import { useCreateProjectState } from "./useCreateProjectState";
import { safeProjectCreationError, isRepositorySetupRecoveryCode, shouldScanCreateFailure, validateImportFolder, importValidationMessage } from "./create-project-helpers";

/** Open-signal handling and project creation for the create-project flow. */
export function useCreateProjectCreate(props: CreateProjectFlowProps, state: ReturnType<typeof useCreateProjectState>) {
	const {
		connected = true,
		droppedPath,
		hostId,
		onCreateProject,
		onInitializeProject,
		openSignal,
		sourceSignal,
	} = props;
	const {
		abandonPreparedClone,
		cloneDialogOpen,
		cloneSelection,
		folderPickerOpen,
		hasModePicker,
		isBusy,
		isCreating,
		isInitializing,
		modePickerOpen,
		preparedClone,
		reportProjectError,
		repositorySetup,
		resetProjectImportState,
		selectSource,
		selectedKind,
		selectedPath,
		setCloneActivity,
		setCloneDialogOpen,
		setCloneRegistrationFailed,
		setCloneSelection,
		setError,
		setFolderPickerOpen,
		setIsCreating,
		setIsInitializing,
		setIsPreparingGit,
		setModePickerOpen,
		setProjectApprovedActions,
		setProjectImportStep,
		setProjectPrepEvents,
		setProjectRemoteUrl,
		setProjectValidation,
		setRepositorySetup,
		setRepositorySetupWarning,
		setSelectedKind,
		setSelectedPath,
		setValidationScan,
		startFlow,
		t,
	} = state;
	const lastOpenSignal = useRef(openSignal);
	useEffect(() => {
		if (openSignal === undefined || openSignal === lastOpenSignal.current) return;
		lastOpenSignal.current = openSignal;
		startFlow();
	}, [openSignal]);

	// A folder was dropped on the app window. Ignored while the flow already has
	// UI on screen so an in-progress manual selection is never silently discarded.
	const lastDropNonce = useRef(droppedPath?.nonce);
	useEffect(() => {
		if (!droppedPath || droppedPath.nonce === lastDropNonce.current) return;
		lastDropNonce.current = droppedPath.nonce;
		if (isBusy || modePickerOpen || cloneDialogOpen || folderPickerOpen || selectedPath !== null) return;
		startFlow(droppedPath.path);
	}, [droppedPath]);

	const lastSourceNonce = useRef(sourceSignal?.nonce);
	useEffect(() => {
		if (!sourceSignal || sourceSignal.nonce === lastSourceNonce.current) return;
		lastSourceNonce.current = sourceSignal.nonce;
		if (isBusy || modePickerOpen || cloneDialogOpen || folderPickerOpen || selectedPath !== null) return;
		void selectSource(sourceSignal.source);
	}, [sourceSignal]);

	const createProject = async (selection: CreateProjectAgentSelection) => {
		if (!selectedPath || isCreating || isInitializing) return;
		setError(null);
		setIsCreating(true);
		useUiStore.getState().setProjectCreationPending(true);
		try {
			if (cloneSelection) {
				const prepared = preparedClone.current();
				if (!prepared) throw new Error(t("createProject.couldNotAdd"));
				for (let attempt = 0; ; attempt++) {
					try {
						await onCreateProject({ path: selectedPath, clonePreparationId: prepared.preparationId, ...selection });
						break;
					} catch (err: unknown) {
						if (attempt === 2 || !(err instanceof TypeError || apiErrorCode(err) === "SERVICE_UNAVAILABLE")) throw err;
						await new Promise((resolve) => window.setTimeout(resolve, (attempt + 1) * 1000));
					}
				}
				setSelectedPath(null);
				setCloneSelection(null);
				preparedClone.complete();
				return;
			}
			if (selectedKind === "single_repo" && repositorySetup) {
				setIsCreating(false);
				setIsInitializing(true);
				await onInitializeProject(selectedPath);
				setRepositorySetup(null);
				setRepositorySetupWarning(null);
				setIsInitializing(false);
				setIsCreating(true);
			}
		// Workspace imports can adopt an existing local Git root. Preserve its
		// checked-out branch as the workspace default (child defaults stay
		// separate); the daemon resolves it at spawn time. Single-repo imports
		// skip this lookup entirely — the daemon resolves their base branch
		// itself, saving a blocking IPC round-trip on the critical path.
		const defaultBranch =
			selectedKind === "workspace" && !hostId ? await tallpineBridge.app.getRepositoryBranch(selectedPath) : undefined;
		await onCreateProject({
			path: selectedPath,
			asWorkspace: selectedKind === "workspace",
			...(defaultBranch ? { defaultBranch } : {}),
			...selection,
		});
			setSelectedPath(null);
		} catch (err) {
			const code = err instanceof Error && "code" in err ? (err.code as string | undefined) : undefined;
			const message = cloneSelection
				? t("createProject.cloneRegistrationFailed", {
					path: selectedPath,
					message: safeProjectCreationError(apiErrorMessage(err), t("createProject.createFailedBody", { defaultValue: "{{brand}} could not create this project. Try again." })),
				})
				: apiErrorMessage(err, t("createProject.couldNotAdd"));
			if (!cloneSelection && selectedKind === "single_repo" && isRepositorySetupRecoveryCode(code)) {
				setRepositorySetup(code);
			}
			if (cloneSelection) {
				setCloneRegistrationFailed(true);
				reportProjectError(message);
			}
			else if (selectedKind === "single_repo") reportProjectError(safeProjectCreationError(
				message,
				t("createProject.createFailedBody", { defaultValue: "{{brand}} could not create this project. Try again." }),
				code,
			));
			else reportProjectError(message);
			if (hasModePicker && !cloneSelection && selectedKind !== "single_repo") {
				if (shouldScanCreateFailure(message) && !hostId) {
					try {
						const scan = await tallpineBridge.app.scanImportFolder({
							path: selectedPath,
							mode: selectedKind === "workspace" ? "workspace" : "project",
						});
						setValidationScan(scan);
					} catch {
						setValidationScan({ path: selectedPath, repos: [] });
					}
					setFolderPickerOpen(true);
				} else {
					setValidationScan(null);
					setFolderPickerOpen(false);
					setModePickerOpen(true);
				}
				setSelectedPath(null);
			}
		} finally {
			useUiStore.getState().setProjectCreationPending(false);
			setIsCreating(false);
			setIsInitializing(false);
		}
	};

	const prepareClone = async (next: CloneRepositorySelection) => {
		if (hostId && !connected) {
			reportProjectError(t("remote.connectBeforeStart"));
			return;
		}
		const previous = preparedClone.current();
		if (previous && !preparedClone.matches(next) && !(await abandonPreparedClone())) return;
		setError(null);
		setCloneRegistrationFailed(false);
		setIsPreparingGit(true);
		setCloneActivity(preparedClone.current() ? "validating" : "cloning");
		let clonedPath: string | null = null;
		try {
			const data = preparedClone.current() ?? await preparedClone.prepare(next.remoteUrl, next.destinationParent);
			clonedPath = data.path;
			setCloneActivity("validating");
			const validation = await validateImportFolder(data.path, "project", hostId);
			setCloneDialogOpen(false);
			setCloneSelection(next);
			setSelectedKind("single_repo");
			setModePickerOpen(false);
			setProjectValidation(validation);
			setProjectPrepEvents([]);
			setProjectApprovedActions(validation.root.requiredActions);
			setProjectRemoteUrl(validation.root.requiredActions.includes("set_remote") ? next.remoteUrl : "");
			if (!validation.isValid || validation.nextStep === "error") {
				reportProjectError(importValidationMessage(validation));
				setProjectImportStep("blocked");
				return;
			}
			if (validation.nextStep === "prepare_git") {
				setProjectImportStep("prepare_git");
				return;
			}
			setSelectedPath(data.path);
		} catch (err) {
			if (err instanceof DOMException && err.name === "AbortError") {
				setError(t("createProject.cloneCancelled"));
			} else {
				reportProjectError(clonedPath
					? t("createProject.cloneValidationKept", { path: clonedPath })
					: apiErrorCode(err) === "GIT_CLONE_TIMED_OUT"
						? t("createProject.cloneTimedOut")
						: safeProjectCreationError(apiErrorMessage(err), t("createProject.cloneFailedGuidance", { defaultValue: "Could not clone this repository. Check the URL, your Git credentials, and your network connection." })));
			}
			setCloneDialogOpen(true);
		} finally {
			setCloneActivity(null);
			setIsPreparingGit(false);
		}
	};

	const reopenSourcePicker = async () => {
		if (!(await abandonPreparedClone())) return;
		setCloneSelection(null);
		resetProjectImportState();
		if (hasModePicker) {
			setModePickerOpen(true);
			return;
		}
		setError(null);
	};

	return {
		createProject,
		prepareClone,
		reopenSourcePicker,
	};
}
