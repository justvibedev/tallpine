import { CreateProjectAgentSheet, type CreateProjectAgentSelection } from "./CreateProjectAgentSheet";
import { RemoteDirectoryPicker } from "./RemoteDirectoryPicker";
import CloneRepositoryDialog, { type CloneRepositorySelection } from "./CloneRepositoryDialog";
import { useCreateProjectState } from "./create-project/useCreateProjectState";
import { useCreateProjectCreate } from "./create-project/useCreateProjectCreate";
import { useCreateProjectClone } from "./create-project/useCreateProjectClone";
import type { CreateProjectFlowProps } from "./create-project/create-project-props";
import { CreateProjectFlowBackdrop, CreateProjectSourceDialog } from "./create-project/CreateProjectDialogs";
import { ImportSourcePicker } from "./create-project/ImportSourcePicker";
import { CreateProjectFolderDialog } from "./create-project/CreateProjectFolderDialog";
import { ProjectImportDialog } from "./create-project/ProjectImportDialog";
import { initialCloneDetails } from "./create-project/create-project-types";

export type CreateProjectInput = {
	path: string;
	asWorkspace?: boolean;
	defaultBranch?: string;
	clonePreparationId?: string;
} & CreateProjectAgentSelection;
export type CloneProjectInput = Pick<CloneRepositorySelection, "remoteUrl" | "destinationParent"> &
	CreateProjectAgentSelection;

// Shared create-project flow. The daemon owner determines the folder picker,
// Git preparation, and agent catalog; the screens stay the same.

export function CreateProjectFlow(props: CreateProjectFlowProps) {
	const {
		children,
		connected = true,
		embedded = false,
		existingProjectNames = [],
		existingProjectPaths = [],
		hostId,
		hostLabel,
		remoteHosts = [],
		onCreateStandaloneAgent,
		onDismiss,
		onSelectHost,
	} = props;
	const state = useCreateProjectState(props);
	const create = useCreateProjectCreate(props, state);
	const clone = useCreateProjectClone(props, state);
	const {
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
		selectSource,
		selectedKind,
		selectedPath,
		setCloneDetails,
		setCloneDialogOpen,
		setCloneRegistrationFailed,
		setCloneSelection,
		setError,
		setFolderPickerOpen,
		setModePickerOpen,
		setPendingDropPath,
		setProjectApprovedActions,
		setProjectGitHubRepo,
		setProjectImportStep,
		setProjectRemoteUrl,
		setRemoteBrowse,
		setSelectedPath,
		setValidationScan,
		settingsOpen,
		startFlow,
		validationScan,
	} = state;
	const {
		createProject,
		prepareClone,
		reopenSourcePicker,
	} = create;
	const {
		label,
		leaveCloneDialog,
		prepareProjectGit,
		tryProjectAsWorkspace,
	} = clone;

	return (
		<>
			{!embedded &&
				children?.({
					// Zero-arg wrapper: callers wire this directly to onClick, whose
					// SyntheticEvent would otherwise be forwarded as startFlow's
					// presetPath and get treated as a dropped path.
					choosePath: () => startFlow(),
					disabled: isBusy,
					error,
					label,
				})}
			{!isCreating && !isInitializing ? <CreateProjectFlowBackdrop open={!settingsOpen && (modePickerOpen || cloneDialogOpen || folderDialogOpen || selectedPath !== null || childTransitioning || projectImportOpen || remoteBrowse !== null)} /> : null}
			{hasModePicker && embedded && !modePickerOpen && !cloneDialogOpen && selectedPath === null && (
				<div className="flex w-full flex-col items-center gap-3">
					<ImportSourcePicker disabled={isBusy || !connected} onSelect={selectSource} onCreateStandaloneAgent={onCreateStandaloneAgent} hostId={hostId} hostLabel={hostLabel} remoteHosts={remoteHosts} onSelectHost={onSelectHost} />
					{error && !folderPickerOpen && selectedPath === null && (
						<p className="text-caption leading-body text-error" role="status">
							{error}
						</p>
					)}
				</div>
			)}
			{hasModePicker && (
				<>
					<CreateProjectSourceDialog
						childOpen={childTransitioning || cloneDialogOpen || folderDialogOpen || projectImportOpen || selectedPath !== null || remoteBrowse !== null}
						closeDisabled={isBusy}
						disabled={isBusy || !connected}
						hostLabel={hostLabel}
						hostId={hostId}
						remoteHosts={remoteHosts}
						onCreateStandaloneAgent={onCreateStandaloneAgent}
						open={modePickerOpen && !settingsOpen}
						onOpenChange={(open) => {
							if (isBusy) return;
							setModePickerOpen(open);
							// Dismissed without picking a kind — don't let a stale dropped
							// path hijack the next manual "New Project" click, and reopen
							// on the default Local choice.
							if (!open) {
								setPendingDropPath(null);
								onDismiss?.();
							}
						}}
						onSelect={selectSource}
						onSelectHost={(nextHostId) => {
							onSelectHost?.(nextHostId);
						}}
					/>
					{cloneDialogOpen ? (
						<CloneRepositoryDialog
							remote={Boolean(hostId)}
							onChooseRemoteDestination={() => setRemoteBrowse({ kind: "clone_destination" })}
							disabled={isBusy}
							cloning={cloneActivity === "cloning"}
							error={error}
							existingProjectNames={existingProjectNames}
							existingProjectPaths={existingProjectPaths}
							onBack={() => {
								void leaveCloneDialog(true);
							}}
							onChange={(next) => {
								setCloneDetails(next);
								setError(null);
							}}
							onClose={() => {
								void leaveCloneDialog(false);
							}}
							onCheckDestination={preparedClone.checkDestination}
							onUseExisting={(path) => {
								setError(null);
								setCloneSelection(null);
								void chooseDirectory("single_repo", path, true);
							}}
							onCancel={preparedClone.cancel}
							activity={cloneActivity}
							onContinue={(next) => void prepareClone(next)}
							onError={reportProjectError}
							open={cloneDialogOpen && !settingsOpen}
							shake={projectImportShake}
							value={cloneDetails}
						/>
					) : null}
					<CreateProjectFolderDialog
						disabled={isBusy}
						error={error}
						kind={selectedKind}
						open={folderDialogOpen && !settingsOpen}
						scan={validationScan}
						validation={projectValidation}
						isPreparingGit={isPreparingGit}
						shake={projectImportShake}
						onContinueAsProject={() => {
							if (projectValidation) void chooseDirectory("single_repo", projectValidation.root.repoPath, true);
						}}
						onContinue={() => {
							if (!validationScan) return;
							if (selectedKind === "workspace") {
								setFolderPickerOpen(false);
								setModePickerOpen(false);
								setSelectedPath(validationScan.path);
								return;
							}
							setFolderPickerOpen(false);
							setSelectedPath(validationScan.path);
							setModePickerOpen(false);
						}}
						onBack={() => {
							setError(null);
							setValidationScan(null);
							if (hasModePicker) setModePickerOpen(true);
							else setFolderPickerOpen(false);
						}}
						onChooseFolder={() => void chooseDirectory(selectedKind)}
						onOpenChange={(open) => {
							if (!isBusy) {
								setFolderPickerOpen(open);
								if (!open) {
									setError(null);
									setValidationScan(null);
									onDismiss?.();
								}
							}
						}}
					/>
				</>
			)}
			<ProjectImportDialog
				remote={Boolean(hostId)}
				disabled={isBusy}
				approvedActions={projectApprovedActions}
				onBack={() => void reopenSourcePicker()}
				onChangeApprovedActions={setProjectApprovedActions}
				onChangeFolder={() => void chooseDirectory("single_repo")}
				onChangeGitHubRepository={setProjectGitHubRepo}
				onChangeRemote={setProjectRemoteUrl}
				onContinue={() => void prepareProjectGit()}
				onContinueProject={() => {
					if (projectValidation?.root.requiredActions.length) {
						setProjectImportStep("prepare_git");
					} else if (projectValidation) {
						setModePickerOpen(false);
						setProjectImportStep(null);
						setSelectedPath(projectValidation.root.repoPath);
					}
				}}
				shake={projectImportShake}
				onOpenChange={(open) => {
					if (isBusy) return;
					if (!open) {
						void (async () => {
							if (!(await abandonPreparedClone())) return;
							setCloneSelection(null);
							resetProjectImportState();
							setError(null);
							onDismiss?.();
						})();
					}
				}}
				onTryWorkspace={tryProjectAsWorkspace}
				open={projectImportOpen && !settingsOpen}
				githubRepository={projectGitHubRepo}
				remoteUrl={projectRemoteUrl}
				step={projectImportStep}
				isPreparingGit={isPreparingGit}
				events={projectPrepEvents}
				validation={projectValidation}
			/>
			<CreateProjectAgentSheet
				preparedClonePath={cloneSelection ? selectedPath ?? undefined : undefined}
				onKeepCheckout={cloneRegistrationFailed ? async () => {
					if (!(await abandonPreparedClone())) return;
					setSelectedPath(null);
					setCloneSelection(null);
					setCloneRegistrationFailed(false);
					setError(null);
					onDismiss?.();
				} : undefined}
				connected={connected}
				error={error}
				hostId={hostId}
				isCreating={isCreating}
				isInitializing={isInitializing}
				kind={selectedKind}
				shake={projectImportShake}
				onOpenChange={(open) => {
					if (!open) {
						void (async () => {
							if (!(await abandonPreparedClone())) return;
							setSelectedPath(null);
							setCloneSelection(null);
							resetProjectImportState();
							if (!folderPickerOpen) setError(null);
							onDismiss?.();
						})();
					}
				}}
					onBack={
					cloneSelection
						? () => {
								void (async () => {
									if (!(await abandonPreparedClone())) return;
									setCloneSelection(null);
									setSelectedPath(null);
									setCloneDetails(initialCloneDetails(hostId));
									setCloneDialogOpen(true);
								})();
							}
						: undefined
				}
				onSubmit={createProject}
				open={selectedPath !== null}
				path={selectedPath}
				repositorySetupNeeded={repositorySetup !== null}
				repositorySetupWarning={repositorySetupWarning}
			/>
			{hostId && hostLabel && remoteBrowse ? <RemoteDirectoryPicker
				key={`${hostId}:${remoteBrowse.kind}`}
				hostId={hostId}
				hostLabel={hostLabel}
				connected={connected}
				initialPath={remoteBrowse.kind === "clone_destination" ? cloneDetails.destinationParent : ""}
				onClose={() => setRemoteBrowse(null)}
				onChoose={(path) => {
					setRemoteBrowse(null);
					if (remoteBrowse.kind === "clone_destination") setCloneDetails((current) => ({ ...current, destinationParent: path }));
					else void chooseDirectory(remoteBrowse.kind, path, remoteBrowse.preserveCurrentDialog);
				}}
			/> : null}

			{error && !hasModePicker && (
				<span className="sr-only" role="status">
					{error}
				</span>
			)}
		</>
	);
}
