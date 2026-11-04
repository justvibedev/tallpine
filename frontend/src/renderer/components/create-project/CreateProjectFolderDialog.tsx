import * as Dialog from "@radix-ui/react-dialog";
/** Folder dialog for the create-project flow. */
import { useTranslation } from "react-i18next";
import { CircleDashed, ChevronRight, Folder, X } from "lucide-react";
import type { ImportFolderScan } from "../../../preload";
import { cn } from "../../lib/utils";
import type { ProjectKind } from "../../types/workspace";
import { PathRow } from "../PathRow";
import { Button } from "../ui/button";
import { Spinner } from "../ui/spinner";
import { ImportValidationResult } from "./create-project-types";
import { mergeWorkspaceImportRepos, normalizeImportRepos, displayImportPath, ImportRepoRow, WorkspaceImportRepoList } from "./ImportRepoList";

export function CreateProjectFolderDialog({
	disabled,
	error,
	kind,
	onBack,
	onChooseFolder,
	onContinue,
	onContinueAsProject,
	onOpenChange,
	open,
	scan,
	validation,
	isPreparingGit,
	shake,
}: {
	disabled: boolean;
	error: string | null;
	kind: ProjectKind;
	onBack: () => void;
	onChooseFolder: () => void;
	onContinue: () => void;
	onContinueAsProject: () => void;
	onOpenChange: (open: boolean) => void;
	open: boolean;
	scan: ImportFolderScan | null;
	validation: ImportValidationResult | null;
	isPreparingGit: boolean;
	shake: boolean;
}) {
	const { t } = useTranslation();
	const isWorkspace = kind === "workspace";
	const displayRepos = isWorkspace ? mergeWorkspaceImportRepos(scan, validation) : normalizeImportRepos(scan?.repos ?? []);
	const workspaceHasNoChildGitRepos = isWorkspace && validation?.blockingErrors.includes("WORKSPACE_CHILD_REPO_REQUIRED");
	const workspaceRootIsProject = isWorkspace && validation?.root.isRepo === true && validation.root.hasOrigin === true;
	const workspaceValidationBlocked = isWorkspace && validation !== null && (!validation.isValid || validation.nextStep === "error") && !workspaceHasNoChildGitRepos;
	const workspaceExcludedRepos = isWorkspace ? displayRepos.filter((repo) => repo.isRepo && repo.requiredActions.length > 0) : [];
	// The daemon registers children without a commit or origin as not set up and
	// leaves them out of sessions, so only block when no child repo is ready.
	const workspaceHasReadyRepo = displayRepos.some((repo) => repo.isRepo && repo.requiredActions.length === 0 && repo.status !== "error" && repo.blockingErrors.length === 0);
	const workspaceReposNeedSetup = workspaceExcludedRepos.length > 0 && !workspaceHasReadyRepo;
	const workspaceReposExcluded = workspaceExcludedRepos.length > 0 && workspaceHasReadyRepo;
	const workspaceSetupReady = !isWorkspace || (!workspaceHasNoChildGitRepos && !workspaceReposNeedSetup);
	const failedRepos =
		displayRepos.filter(
			(repo) =>
				(repo.status === "error" || !repo.hasRemote) &&
				repo.requiredActions.length === 0 &&
				!repo.needsGitInit &&
				repo.reason !== "Repository must have at least one commit.",
		) ?? [];
	const hasScan = scan !== null;
	return (
		<Dialog.Root open={open} onOpenChange={onOpenChange}>
			<Dialog.Portal>
				<Dialog.Content className={cn("fixed left-1/2 top-1/2 z-overlay flex max-h-[min(640px,calc(100svh-24px))] w-[min(640px,calc(100vw-24px))] -translate-x-1/2 -translate-y-1/2 flex-col overflow-hidden rounded-lg border border-border bg-popover p-0 text-popover-foreground shadow-xl data-[state=open]:animate-modal-in data-[state=closed]:animate-modal-out motion-reduce:animate-none", shake && "modal-shake")}>
					<div className="relative flex shrink-0 items-start gap-3 px-4 pt-3">
						<Button
							type="button"
							variant="outline"
							size="icon"
							aria-label={t("createProject.backToType")}
							disabled={disabled}
							onClick={onBack}
						>
							<ChevronRight className="size-4 rotate-180" aria-hidden="true" />
						</Button>
						<div className="min-w-0 flex-1 pr-8">
							<Dialog.Title className="text-[18px] font-semibold text-[var(--color-text-import-title)]">
								{isWorkspace ? t("createProject.importWorkspace") : t("createProject.importProject")}
							</Dialog.Title>
							<Dialog.Description className="sr-only">
								{isWorkspace ? t("createProject.importWorkspaceDesc") : t("createProject.importProjectDesc")}
							</Dialog.Description>
						</div>
						<Dialog.Close asChild>
							<button
								type="button"
								className="settings-close-button"
								aria-label={t("createProject.closeImport")}
								disabled={disabled}
							>
								<X className="size-4" aria-hidden="true" />
							</button>
						</Dialog.Close>
					</div>
					<div className="min-h-0 flex-1 overflow-y-auto px-4 pb-1 pt-3">
						{hasScan ? (
							<div className="space-y-3">
								{!workspaceHasNoChildGitRepos ? <PathRow
									action={t("createProject.change")}
									disabled={disabled}
									icon={<Folder className="size-4 shrink-0 text-[var(--color-text-import-muted)]" aria-hidden="true" />}
									onClick={onChooseFolder}
								>
									{displayImportPath(scan.path)}
								</PathRow> : null}

								{error && !isWorkspace && (
									<div className="rounded-lg border border-destructive/40 bg-destructive/10">
										<div className="border-b border-destructive/30 px-3 py-2 font-mono text-[11px] font-semibold uppercase tracking-[0.12em] text-destructive">
											<span className="mr-2 inline-block size-2 rounded-full bg-destructive" aria-hidden="true" />
											{isWorkspace ? t("createProject.importFailedWorkspace") : t("createProject.importFailedProject")}
										</div>
						<div className="px-3 py-2 text-[12px] leading-5 text-destructive">{error}</div>
						<div className="border-t border-destructive/30 px-3 py-2 text-[12px] text-[var(--color-text-import-muted)]">
							{t("createProject.footerReview")}
						</div>
										{failedRepos.length > 0 && (
											<div className="border-t border-destructive/30">
									{failedRepos.map((repo) => (
										<ImportRepoRow key={repo.path} repo={repo} failed />
									))}
									<div className="border-t border-destructive/30 px-3 py-2 text-[12px] text-[var(--color-text-import-muted)]">
										{t("createProject.footerResolve", { count: failedRepos.length })}
									</div>
								</div>
										)}
									</div>
								)}
								{workspaceHasNoChildGitRepos && !error ? <p className="text-[14px] leading-6 text-[var(--color-text-import-muted)]">{t("createProject.workspaceRequiresInitializedChildRepo")}</p> : null}
								{workspaceRootIsProject && !error ? <p className="text-[14px] leading-6 text-[var(--color-text-import-muted)]">{t("createProject.workspaceRootIsProject")}</p> : null}
								{workspaceReposNeedSetup && !error ? <p className="text-[13px] leading-5 text-destructive">{t("createProject.workspaceRemoteSetupRequired")}</p> : null}
								{workspaceReposExcluded && !error ? <p className="text-[13px] leading-5 text-[var(--color-text-import-muted)]">{t("createProject.workspaceReposExcluded", { count: workspaceExcludedRepos.length })}</p> : null}

							{workspaceRootIsProject || workspaceHasNoChildGitRepos ? null : isWorkspace ? <WorkspaceImportRepoList excludeUnready={workspaceReposExcluded} repos={displayRepos} /> : displayRepos.length > 0 ? <div className="divide-y divide-border/50 overflow-hidden rounded-sm bg-[var(--color-bg-import-card)]">
								{displayRepos.map((repo) => <ImportRepoRow key={repo.path} repo={repo} />)}
							</div> : null}

								{displayRepos.length === 0 && !workspaceHasNoChildGitRepos && !workspaceRootIsProject && (
									<div className="rounded-md border border-[var(--color-border-import-modal)] bg-[var(--color-bg-import-card)] p-3 text-[12px] text-[var(--color-text-import-muted)]">
										{t("createProject.noRepos")}
									</div>
								)}
							</div>
						) : null}
					</div>
					<div className="flex shrink-0 justify-end gap-2 px-4 pb-4 pt-3">
						<div className="flex flex-wrap items-center justify-end gap-3">
							<Button type="button" variant="outline" disabled={disabled} onClick={workspaceHasNoChildGitRepos ? onBack : () => onOpenChange(false)}>
								{workspaceHasNoChildGitRepos ? "Go Back" : t("createProject.cancel")}
							</Button>
							{hasScan && workspaceRootIsProject && !error ? (
								<Button type="button" variant="primary" disabled={disabled} onClick={onContinueAsProject}>
									{t("createProject.importAsProject")}
								</Button>
							) : hasScan && workspaceHasNoChildGitRepos && !error ? (
								<Button type="button" variant="outline" disabled={disabled} onClick={onContinueAsProject}>
									{t("createProject.importAsProject")}
								</Button>
							) : hasScan && !workspaceValidationBlocked && failedRepos.length === 0 && (!error || isWorkspace) ? (
								<Button type="button" variant="primary" disabled={disabled || !workspaceSetupReady} onClick={onContinue}>
									{isPreparingGit ? <><Spinner icon={CircleDashed} className="size-4" />{t("createProject.settingUp")}</> : t("createProject.cloneContinue")}
								</Button>
							) : null}
						</div>
					</div>
				</Dialog.Content>
			</Dialog.Portal>
		</Dialog.Root>
	);
}
