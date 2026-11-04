import { useTranslation } from "react-i18next";
import { useReducer, useState } from "react";
import type { ImportFolderScan } from "../../../preload";
import { usePreparedClone } from "../../hooks/usePreparedClone";
import { apiErrorMessage } from "../../lib/api-client";
import { tallpineBridge } from "../../lib/bridge";
import { useUiStore } from "../../stores/ui-store";
import type { ProjectKind } from "../../types/workspace";
import { type CloneRepositoryDetails, type CloneRepositorySelection } from "../CloneRepositoryDialog";
import type { CreateProjectFlowProps } from "./create-project-props";
import { createProjectViewReducer, initialCloneDetails, RemoteBrowseRequest, ImportValidationResult, GitPreparationEvent, ProjectGitHubRepository, ProjectImportStep, ProjectSource } from "./create-project-types";
import { sameProjectPath, validateImportFolder, importNeedsRemoteSetup, defaultProjectGitHubRepository, githubRepositoryRemoteUrl, importValidationMessage } from "./create-project-helpers";

/** View, selection and source-selection state for the create-project flow. */
export function useCreateProjectState(props: CreateProjectFlowProps) {
	const {
		connected = true,
		existingProjectPaths = [],
		idleLabel,
		hostId,
		initialOpen = false,
		mode = "single_repo",
		onOpenExistingProject,
	} = props;
	const { t } = useTranslation();
	const resolvedIdleLabel = idleLabel ?? t("createProject.newProject");
	const [error, setError] = useState<string | null>(null);
	const [activeView, dispatchView] = useReducer(createProjectViewReducer, initialOpen ? "source" : null);
	const modePickerOpen = activeView === "source";
	const cloneDialogOpen = activeView === "clone";
	const [cloneDetails, setCloneDetails] = useState<CloneRepositoryDetails>(() => initialCloneDetails(hostId));
	const [cloneSelection, setCloneSelection] = useState<CloneRepositorySelection | null>(null);
	const [cloneActivity, setCloneActivity] = useState<"cloning" | "validating" | null>(null);
	const [cloneRegistrationFailed, setCloneRegistrationFailed] = useState(false);
	const preparedClone = usePreparedClone(hostId);
	const [remoteBrowse, setRemoteBrowse] = useState<RemoteBrowseRequest | null>(null);
	const folderPickerOpen = activeView === "folder";
	const [childTransitioning, setChildTransitioning] = useState(false);
	const [selectedKind, setSelectedKind] = useState<ProjectKind>(mode === "workspace" ? "workspace" : "single_repo");
	const [selectedPath, setSelectedPath] = useState<string | null>(null);
	const [validationScan, setValidationScan] = useState<ImportFolderScan | null>(null);
	const [projectValidation, setProjectValidation] = useState<ImportValidationResult | null>(null);
	const projectImportStep = activeView === "blocked" || activeView === "prepare_git" ? activeView : null;
	const [projectPrepEvents, setProjectPrepEvents] = useState<GitPreparationEvent[]>([]);
	const [projectApprovedActions, setProjectApprovedActions] = useState<string[]>([]);
	const [projectRemoteUrl, setProjectRemoteUrl] = useState("");
	const [projectGitHubRepo, setProjectGitHubRepo] = useState<ProjectGitHubRepository | null>(null);
	const [projectImportShake, setProjectImportShake] = useState(false);
	const [isChoosingPath, setIsChoosingPath] = useState(false);
	const [isCreating, setIsCreating] = useState(false);
	const [isInitializing, setIsInitializing] = useState(false);
	const [isPreparingGit, setIsPreparingGit] = useState(false);
	const [repositorySetup, setRepositorySetup] = useState<"NOT_A_GIT_REPO" | "PROJECT_UNBORN" | null>(null);
	const [repositorySetupWarning, setRepositorySetupWarning] = useState<string | null>(null);
	// A path that arrived via droppedPath, staged until the user confirms
	// Workspace vs Project. Consumed exactly once by openFolderStep.
	const [pendingDropPath, setPendingDropPath] = useState<string | null>(null);

	const hasModePicker = mode === "choose";
	const projectImportOpen = projectImportStep !== null && projectValidation !== null;
	const folderDialogOpen = folderPickerOpen && validationScan !== null;
	const isBusy = isChoosingPath || isCreating || isInitializing || isPreparingGit || preparedClone.isCleaning;
	const setModePickerOpen = (open: boolean) => dispatchView(open ? { type: "open", view: "source" } : { type: "close", view: "source" });
	const setCloneDialogOpen = (open: boolean) => dispatchView(open ? { type: "open", view: "clone" } : { type: "close", view: "clone" });
	const setFolderPickerOpen = (open: boolean) => dispatchView(open ? { type: "open", view: "folder" } : { type: "close", view: "folder" });
	const setProjectImportStep = (step: ProjectImportStep | null) => dispatchView(step ? { type: "open", view: step } : { type: "closeProjectImport" });

	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	// Settings opens as a page in the center pane; this flow stays mounted under it,
	// so its modals and backdrop step aside instead of covering the page.
	const settingsOpen = useUiStore((state) => state.settingsModal?.scope === "global");
	const resetProjectImportState = () => {
		setCloneRegistrationFailed(false);
		setProjectValidation(null);
		setProjectImportStep(null);
		setProjectPrepEvents([]);
		setProjectApprovedActions([]);
		setProjectRemoteUrl("");
		setProjectGitHubRepo(null);
		setProjectImportShake(false);
	};

	const reportProjectError = (message: string) => {
		setError(message);
		showGlobalToast(t("createProject.setupFailedToastTitle", { defaultValue: "Project setup failed" }), message, "error");
		setProjectImportShake(false);
		window.requestAnimationFrame(() => setProjectImportShake(true));
		window.setTimeout(() => setProjectImportShake(false), 320);
	};

	const abandonPreparedClone = async (): Promise<boolean> => {
		try {
			await preparedClone.cleanup();
			return true;
		} catch (err) {
			reportProjectError(apiErrorMessage(err, t("createProject.couldNotAdd")));
			return false;
		}
	};

	const transitionToChild = (open: () => void) => {
		setChildTransitioning(true);
		window.setTimeout(() => {
			open();
			setChildTransitioning(false);
		}, 80);
	};
	const transitionFromFolder = (open: () => void) => {
		setFolderPickerOpen(false);
		if (cloneDialogOpen) setCloneDialogOpen(false);
		transitionToChild(open);
	};

	const selectSource = async (source: ProjectSource) => {
		if (preparedClone.current() && !(await abandonPreparedClone())) return;
		const presetPath = pendingDropPath;
		setPendingDropPath(null);
		setError(null);
		setValidationScan(null);
		resetProjectImportState();
		if (source === "clone") {
			setCloneDetails(initialCloneDetails(hostId));
			setCloneSelection(null);
			transitionToChild(() => setCloneDialogOpen(true));
			return;
		}
		setCloneSelection(null);
		preparedClone.complete();
		// Keep the selector mounted behind the native picker. Closing it first
		// exposes a blank compositor frame on Windows before Explorer takes focus.
		void chooseDirectory(source === "workspace" ? "workspace" : "single_repo", presetPath ?? undefined);
	};

	const chooseDirectory = async (kind: ProjectKind, presetPath?: string, preserveCurrentDialog = false) => {
		if (hostId && !presetPath) {
			setRemoteBrowse({ kind, preserveCurrentDialog });
			return;
		}
		if (!preserveCurrentDialog) {
			setError(null);
			setValidationScan(null);
			resetProjectImportState();
			setRepositorySetup(null);
			setRepositorySetupWarning(null);
			setSelectedKind(kind);
		}
		setIsChoosingPath(true);
		try {
			if (hostId && !connected) throw new Error(t("remote.connectBeforeStart"));
			const path =
				presetPath ??
				(await tallpineBridge.app.chooseDirectory(
					kind === "workspace" ? t("createProject.chooseWorkspace") : t("createProject.chooseRepo"),
				));
			if (path && kind === "single_repo") {
				const registeredPath = existingProjectPaths.find((candidate) => sameProjectPath(candidate, path));
				if (registeredPath && onOpenExistingProject) {
					setModePickerOpen(false);
					setFolderPickerOpen(false);
					showGlobalToast("Project already added", "Opened the registered project for this folder.");
					await onOpenExistingProject(registeredPath);
					if (cloneDialogOpen) setCloneDialogOpen(false);
					return;
				}
				const validation = await validateImportFolder(path, "project", hostId);
				const applyProjectValidation = () => {
					setError(null);
					setValidationScan(null);
					resetProjectImportState();
					setRepositorySetup(null);
					setRepositorySetupWarning(null);
					setSelectedKind(kind);
					setProjectValidation(validation);
					setProjectPrepEvents([]);
					setProjectApprovedActions(validation.root.requiredActions);
					const needsRemoteSetup = importNeedsRemoteSetup(validation.root.requiredActions);
					const githubRepo = needsRemoteSetup ? defaultProjectGitHubRepository(validation.root.repoPath) : null;
					setProjectGitHubRepo(githubRepo);
					setProjectRemoteUrl(needsRemoteSetup && githubRepo ? githubRepositoryRemoteUrl(githubRepo) : "");
				};
				if (!preserveCurrentDialog) applyProjectValidation();
				const openProjectStep = (step: ProjectImportStep) => {
					if (preserveCurrentDialog) transitionFromFolder(() => {
						applyProjectValidation();
						setProjectImportStep(step);
					});
					else setProjectImportStep(step);
				};
				if (!validation.isValid || validation.nextStep === "error") {
					reportProjectError(importValidationMessage(validation));
					openProjectStep("blocked");
					return;
				}
				if (validation.nextStep === "choose_import_kind") {
					openProjectStep("blocked");
					return;
				}
				if (validation.nextStep === "prepare_git") {
					openProjectStep("prepare_git");
					return;
				}
				if (validation.warning) {
					openProjectStep("blocked");
					return;
				}
				if (preserveCurrentDialog) {
					setModePickerOpen(false);
					transitionFromFolder(() => {
						applyProjectValidation();
						setSelectedPath(path);
					});
					return;
				}
			}
			if (path && kind === "workspace") {
				try {
					const [validation, scan, ancestorWarning] = await Promise.all([
						validateImportFolder(path, "workspace", hostId),
						hostId ? Promise.resolve(null) : tallpineBridge.app.scanImportFolder({ path, mode: "workspace" }),
						hostId ? Promise.resolve(undefined) : tallpineBridge.app.checkAncestorRepo(path).catch(() => undefined),
					]);
					const resolvedScan: ImportFolderScan = scan ?? { path: validation.root.repoPath, repos: [] };
					setValidationScan(resolvedScan);
					setRepositorySetupWarning(ancestorWarning ?? resolvedScan.setupWarning ?? null);
					if (ancestorWarning ?? resolvedScan.setupWarning) setRepositorySetup("NOT_A_GIT_REPO");
					setProjectValidation(validation);
					setProjectPrepEvents([]);
					setProjectApprovedActions(validation.root.requiredActions);
					setProjectRemoteUrl("");
					setProjectGitHubRepo(null);
					if ((!validation.isValid || validation.nextStep === "error") && !validation.blockingErrors.includes("WORKSPACE_CHILD_REPO_REQUIRED")) {
						reportProjectError(importValidationMessage(validation));
					}
					setFolderPickerOpen(true);
					return;
				} catch (err) {
					setValidationScan({ path, repos: [] });
					setError(err instanceof Error ? err.message : t("createProject.couldNotAdd"));
					setFolderPickerOpen(true);
					return;
				}
			}
			if (path) {
				setModePickerOpen(false);
				if (preserveCurrentDialog) transitionFromFolder(() => setSelectedPath(path));
				else {
					setSelectedPath(path);
					setFolderPickerOpen(false);
				}
			}
		} catch (err) {
			reportProjectError(err instanceof Error ? err.message : t("createProject.couldNotAdd"));
		} finally {
			setIsChoosingPath(false);
		}
	};

	const startFlow = (presetPath?: string) => {
		setPendingDropPath(presetPath ?? null);
		resetProjectImportState();
		setCloneDetails(initialCloneDetails(hostId));
		if (hasModePicker) {
			setError(null);
			setCloneSelection(null);
			setModePickerOpen(true);
			return;
		}
		void chooseDirectory(mode, presetPath);
	};

	// Seed with the current value so we never open on mount; open when it changes.
	return {
		abandonPreparedClone,
		childTransitioning,
		chooseDirectory,
		cloneActivity,
		cloneDetails,
		cloneDialogOpen,
		cloneRegistrationFailed,
		cloneSelection,
		error,
		folderDialogOpen,
		folderPickerOpen,
		hasModePicker,
		isBusy,
		isCreating,
		isInitializing,
		isPreparingGit,
		modePickerOpen,
		preparedClone,
		projectApprovedActions,
		projectGitHubRepo,
		projectImportOpen,
		projectImportShake,
		projectImportStep,
		projectPrepEvents,
		projectRemoteUrl,
		projectValidation,
		remoteBrowse,
		reportProjectError,
		repositorySetup,
		repositorySetupWarning,
		resetProjectImportState,
		resolvedIdleLabel,
		selectSource,
		selectedKind,
		selectedPath,
		setCloneActivity,
		setCloneDetails,
		setCloneDialogOpen,
		setCloneRegistrationFailed,
		setCloneSelection,
		setError,
		setFolderPickerOpen,
		setIsCreating,
		setIsInitializing,
		setIsPreparingGit,
		setModePickerOpen,
		setPendingDropPath,
		setProjectApprovedActions,
		setProjectGitHubRepo,
		setProjectImportStep,
		setProjectPrepEvents,
		setProjectRemoteUrl,
		setProjectValidation,
		setRemoteBrowse,
		setRepositorySetup,
		setRepositorySetupWarning,
		setSelectedKind,
		setSelectedPath,
		setValidationScan,
		settingsOpen,
		startFlow,
		t,
		validationScan,
	};
}
