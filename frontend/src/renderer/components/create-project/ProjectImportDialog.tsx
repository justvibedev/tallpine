import * as Dialog from "@radix-ui/react-dialog";
/** Project import dialog. */
import { AnimatePresence, motion } from "motion/react";
import { useTranslation } from "react-i18next";
import { CheckCircle2, CircleDashed, ChevronRight, Folder, Globe, LoaderCircle, Lock, X, XCircle } from "lucide-react";
import { useEffect, useState } from "react";
import { tallpineBridge } from "../../lib/bridge";
import { cn } from "../../lib/utils";
import { PathRow } from "../PathRow";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../ui/select";
import { Switch } from "../ui/switch";
import { RemoteImg } from "../RemoteImg";
import { Spinner } from "../ui/spinner";
import { GitPreparationEvent, ProjectGitHubRepository, ProjectImportStep, ImportValidationResult, GitHubOwner, GitHubRepositoryAvailability, GitHubIcon } from "./create-project-types";
import { defaultProjectGitHubRepository, importNeedsRemoteSetup, normalizeGitHubRepositoryName, githubRepositoryRemoteUrl, mergePreparationEvents, projectNameFromPath, gitActionLabel } from "./create-project-helpers";
import { displayImportPath, GitSetupFields } from "./ImportRepoList";

export function ProjectImportDialog({
	approvedActions,
	disabled,
	events,
	githubRepository,
	remote = false,
	onBack,
	onChangeApprovedActions,
	onChangeFolder,
	onChangeGitHubRepository,
	onChangeRemote,
	onContinue,
	onContinueProject,
	onOpenChange,
	onTryWorkspace,
	open,
	remoteUrl,
	shake,
	step,
	isPreparingGit,
	validation,
}: {
	approvedActions: string[];
	disabled: boolean;
	events: GitPreparationEvent[];
	githubRepository: ProjectGitHubRepository | null;
	remote?: boolean;
	onBack: () => void;
	onChangeApprovedActions: (actions: string[]) => void;
	onChangeFolder: () => void;
	onChangeGitHubRepository: (repository: ProjectGitHubRepository) => void;
	onChangeRemote: (value: string) => void;
	onContinue: () => void;
	onContinueProject: () => void;
	onOpenChange: (open: boolean) => void;
	onTryWorkspace: () => void;
	open: boolean;
	remoteUrl: string;
	shake: boolean;
	step: ProjectImportStep | null;
	isPreparingGit: boolean;
	validation: ImportValidationResult | null;
}) {
	const { t } = useTranslation();
	const requiredActions = validation?.root.requiredActions ?? [];
	const requiredRemote = importNeedsRemoteSetup(requiredActions);
	// A remote is never required; the user may opt in on the prepare step.
	const optedIntoRemote = approvedActions.includes("create_remote_repository");
	const needsRemote = requiredRemote || optedIntoRemote;
	const offerOptionalRemote = step === "prepare_git" && !requiredRemote && validation?.root.hasOrigin === false;
	const toggleOptionalRemote = (on: boolean) => {
		const repo = githubRepository ?? defaultProjectGitHubRepository(validation?.root.repoPath ?? "");
		onChangeApprovedActions(on ? [...approvedActions, "create_remote_repository"] : approvedActions.filter((action) => action !== "create_remote_repository"));
		onChangeGitHubRepository(repo);
		onChangeRemote(on ? githubRepositoryRemoteUrl(repo) : "");
	};
	const githubOwner = githubRepository?.owner.trim() ?? "";
	const githubNameInput = githubRepository?.name ?? "";
	const githubName = normalizeGitHubRepositoryName(githubNameInput);
	const githubNameWasNormalized = githubName !== "" && githubName !== githubNameInput.trim();
	const isPrivate = githubRepository?.private ?? true;
	const visibilityLabel = isPrivate
		? t("createProject.privateRepository", { defaultValue: "Private repository" })
		: t("createProject.publicRepository", { defaultValue: "Public repository" });
	const visibilityHelper = isPrivate
		? t("createProject.privateRepositoryHelper", {
				defaultValue: "Only you and people you invite can see this repo",
		  })
		: t("createProject.publicRepositoryHelper", {
				defaultValue: "Anyone on the internet can see this repo",
		  });
	const [githubOwners, setGitHubOwners] = useState<GitHubOwner[]>([]);
	const [customGitHubOwner, setCustomGitHubOwner] = useState(remote);
	const selectedGitHubOwner = githubOwners.find((owner) => owner.login === githubOwner);
	const [availability, setAvailability] = useState<GitHubRepositoryAvailability>({ state: "idle" });
	const githubNameDescription = [
		availability.state === "unavailable" ? "githubRepoNameError" : null,
		githubNameWasNormalized ? "githubRepoNameNormalization" : null,
	].filter(Boolean).join(" ") || undefined;
	useEffect(() => {
		if (!open || !needsRemote || remote) return;
		let cancelled = false;
		const applyOwners = (owners: GitHubOwner[]) => {
			if (!cancelled) {
				setGitHubOwners(owners);
				const owner = owners[0]?.login ?? "";
				if (owner && githubRepository?.owner === "" && !customGitHubOwner) {
					const next = { ...githubRepository, owner };
					onChangeGitHubRepository(next);
					onChangeRemote(githubRepositoryRemoteUrl(next));
				}
			}
		};
		void tallpineBridge.app.getCachedGitHubOwners().then(applyOwners).catch(() => undefined);
		void tallpineBridge.app.refreshGitHubOwners().then(applyOwners).catch(() => undefined);
		return () => { cancelled = true; };
	}, [customGitHubOwner, githubRepository, needsRemote, onChangeGitHubRepository, onChangeRemote, open, remote]);
	useEffect(() => {
		if (remote) {
			setAvailability({ state: "idle" });
			return;
		}
		if (!open || !needsRemote || !githubOwner || !githubName) {
			setAvailability({ state: "idle" });
			return;
		}
		setAvailability({ state: "checking" });
		let cancelled = false;
		const timer = window.setTimeout(() => {
			void tallpineBridge.app.checkGitHubRepositoryAvailability({ owner: githubOwner, name: githubName })
				.then((result) => {
					if (cancelled) return;
					setAvailability(result.available ? { state: "available" } : { state: "unavailable", message: result.message });
				})
				.catch(() => {
					if (!cancelled) setAvailability({ state: "unavailable", message: t("createProject.githubRepoUnavailable") });
				});
		}, 250);
		return () => {
			cancelled = true;
			window.clearTimeout(timer);
		};
	}, [githubName, githubOwner, needsRemote, open, remote, t]);
	if (!validation || !step) return null;
	const hasChildRepos = (validation.childRepos?.length ?? 0) > 0;
	const mustImportAsWorkspace = step === "blocked" && validation.nextStep === "choose_import_kind" && hasChildRepos;
	const hasFailedStep = events.some((event) => event.state === "error");
	const latestEvents = mergePreparationEvents([], events);
	const missingApprovals = validation.root.requiredActions.filter((action) => !approvedActions.includes(action));
	const continueDisabled = disabled || missingApprovals.length > 0 || (needsRemote && (!githubOwner || !githubName || (!remote && availability.state !== "available")));
	return (
		<Dialog.Root open={open} onOpenChange={onOpenChange}>
			<Dialog.Portal>
				<Dialog.Content
					className={cn("fixed left-1/2 top-1/2 z-overlay flex max-h-[min(640px,calc(100svh-24px))] w-[min(560px,calc(100vw-24px))] -translate-x-1/2 -translate-y-1/2 flex-col overflow-hidden rounded-lg border border-border bg-popover p-0 text-popover-foreground shadow-xl data-[state=open]:animate-modal-in data-[state=closed]:animate-modal-out motion-reduce:animate-none", shake && "modal-shake")}
					onInteractOutside={(event) => event.preventDefault()}
					onPointerDownOutside={(event) => event.preventDefault()}
				>
					<div className="relative flex shrink-0 items-center gap-3 px-4 pt-3">
						<Button type="button" variant="outline" size="icon" aria-label={t("createProject.backToSource")} disabled={disabled} onClick={onBack}>
							<ChevronRight className="size-4 rotate-180" aria-hidden="true" />
						</Button>
						<div className="min-w-0 flex-1 pr-8">
							<Dialog.Title className="text-[18px] font-semibold text-[var(--color-text-import-title)]">
								{step === "prepare_git"
									? t("createProject.prepareProjectTitle")
									: t("createProject.importProject")}
							</Dialog.Title>
							<Dialog.Description className="sr-only">
								{step === "blocked"
									? t("createProject.projectImportBlocked")
									: t("createProject.projectImportApproval")}
							</Dialog.Description>
						</div>
						<Dialog.Close asChild>
							<button type="button" className="settings-close-button" aria-label={t("createProject.closeProjectImport")} disabled={disabled}>
								<X className="size-4" aria-hidden="true" />
							</button>
						</Dialog.Close>
					</div>
					<div className="min-h-0 space-y-4 overflow-y-auto px-4 pb-1 pt-4">
						<div className="space-y-2">
							<Label htmlFor="projectImportFolder" className="text-[13px] font-semibold text-[var(--color-text-import-title)]">
								{t("createProject.projectFolder")}
							</Label>
							<PathRow
								action={t("createProject.change")}
								ariaLabel={t("createProject.change")}
								disabled={disabled}
								id="projectImportFolder"
								icon={<Folder className="size-4 shrink-0 text-[var(--color-text-import-muted)]" aria-hidden="true" />}
								onClick={onChangeFolder}
							>
								{displayImportPath(validation.root.repoPath)}
							</PathRow>
						</div>
						{mustImportAsWorkspace ? (
							<p className="text-[14px] leading-6 text-[var(--color-text-import-muted)]">
								{t("createProject.projectMustBeWorkspace", { defaultValue: "This folder contains child Git repositories. Import it as a workspace instead." })}
							</p>
						) : null}
						{validation.warning ? (
							<div className="border-l-2 border-amber-500/60 pl-3 text-[12px] leading-5 text-muted-foreground">
								{validation.warning}
							</div>
						) : null}
						{step === "prepare_git" ? (
							<section className="space-y-2">
								{requiredRemote ? (
									<p className="text-[14px] leading-5 text-[var(--color-text-import-muted)]">
										<code className="rounded bg-muted px-1.5 py-0.5 font-mono text-[0.9em] text-foreground">{projectNameFromPath(validation.root.repoPath)}</code>{" "}
										{t("createProject.noGitHubRemote")}
									</p>
								) : null}
								{isPreparingGit ? (
									<span className="text-[11px] text-muted-foreground" role="status">
										{t("createProject.projectSetupRunning")}
									</span>
								) : null}
								{offerOptionalRemote ? (
									<div className="flex items-center justify-between gap-3 rounded-md border border-border/70 bg-background/40 p-3">
										<Label htmlFor="optionalRemote" className="text-[12px] font-medium text-[var(--color-text-import-title)]">
											{t("createProject.optionalRemote", { defaultValue: "Also create a remote repository (optional)" })}
										</Label>
										<Switch id="optionalRemote" aria-label={t("createProject.optionalRemote", { defaultValue: "Also create a remote repository (optional)" })} checked={optedIntoRemote} disabled={disabled} onCheckedChange={toggleOptionalRemote} />
									</div>
								) : null}
								{needsRemote ? (
									<div className="space-y-3 pt-1">
											<div className="space-y-1.5">
												<Label htmlFor="githubRepoOwner" className="text-[12px] font-medium text-[var(--color-text-import-title)]">{t("createProject.githubOwner")}</Label>
												<Select
													value={customGitHubOwner ? "__custom__" : githubRepository?.owner ?? ""}
													disabled={disabled}
													onValueChange={(owner) => {
														if (owner === "__custom__") {
															setCustomGitHubOwner(true);
															return;
														}
														setCustomGitHubOwner(false);
														const next = { owner, name: githubRepository?.name ?? "", private: githubRepository?.private ?? true };
														onChangeGitHubRepository(next);
														onChangeRemote(githubRepositoryRemoteUrl(next));
													}}
												>
													<SelectTrigger id="githubRepoOwner" size="sm" className="h-8 w-full bg-[var(--color-bg-import-card)] font-mono text-[12px]" aria-label={t("createProject.githubOwner")}>
														<SelectValue placeholder={t("createProject.githubOwner")}>
															{customGitHubOwner ? (
																<span className="flex items-center gap-2">
															<GitHubIcon className="size-4" />
																	{t("createProject.otherGitHubOwner")}
																</span>
															) : selectedGitHubOwner ? (
																<span className="flex items-center gap-2">
																	<RemoteImg className="size-4 rounded-full" src={selectedGitHubOwner.avatarUrl} alt="" />
																	{selectedGitHubOwner.login}
																</span>
															) : null}
														</SelectValue>
													</SelectTrigger>
													<SelectContent position="popper" side="bottom" align="start" sideOffset={4}>
														{githubOwners.map((owner) => (
															<SelectItem key={owner.login} value={owner.login}>
																<span className="flex items-center gap-2">
																	<RemoteImg className="size-4 rounded-full" src={owner.avatarUrl} alt="" />
																	{owner.login}
																</span>
															</SelectItem>
														))}
														<SelectItem value="__custom__">
															<span className="flex items-center gap-2">
															<GitHubIcon className="size-4" />
																	{t("createProject.useDifferentGitHubOwner")}
															</span>
														</SelectItem>
													</SelectContent>
												</Select>
												{customGitHubOwner ? (
													<Input
														id="githubRepoCustomOwner"
														aria-label={t("createProject.githubOwner")}
														className="h-8 bg-[var(--color-bg-import-card)] font-mono text-[12px]"
														disabled={disabled}
														placeholder={t("createProject.githubOwner")}
														value={githubRepository?.owner ?? ""}
														onChange={(event) => {
															const next = { owner: event.target.value, name: githubRepository?.name ?? "", private: githubRepository?.private ?? true };
															onChangeGitHubRepository(next);
															onChangeRemote(githubRepositoryRemoteUrl(next));
														}}
													/>
												) : null}
											</div>
											<div className="space-y-1.5">
												<div className="relative">
													<Label htmlFor="githubRepoName" className="text-[12px] font-medium text-[var(--color-text-import-title)]">{t("createProject.githubRepositoryName")}</Label>
													<AnimatePresence initial={false}>
														{availability.state === "unavailable" ? (
															<motion.p
																id="githubRepoNameError"
																initial={{ opacity: 0, filter: "blur(2px)" }}
																animate={{ opacity: 1, filter: "blur(0px)" }}
																exit={{ opacity: 0, filter: "blur(2px)" }}
																transition={{ duration: 0.15, ease: "easeOut" }}
																className="absolute right-0 top-0 max-w-[65%] truncate overflow-hidden whitespace-nowrap text-right text-[12px] leading-5 text-destructive"
																role="alert"
															>
																{availability.message ?? t("createProject.githubRepoUnavailable")}
															</motion.p>
														) : null}
													</AnimatePresence>
												</div>
												<div className="relative">
													<Input
														id="githubRepoName"
														aria-label={t("createProject.githubRepositoryName")}
														aria-describedby={githubNameDescription}
														aria-invalid={availability.state === "unavailable" ? true : undefined}
														className="h-8 bg-[var(--color-bg-import-card)] pr-8 font-mono text-[12px]"
														disabled={disabled}
														value={githubRepository?.name ?? ""}
														onChange={(event) => {
															const next = { owner: githubRepository?.owner ?? "", name: event.target.value, private: githubRepository?.private ?? true };
															onChangeGitHubRepository(next);
															onChangeRemote(githubRepositoryRemoteUrl(next));
														}}
													/>
													<AnimatePresence initial={false}>
														{availability.state === "checking" ? (
															<motion.span
																initial={{ opacity: 0, filter: "blur(2px)" }}
																animate={{ opacity: 1, filter: "blur(0px)" }}
																exit={{ opacity: 0, filter: "blur(2px)" }}
																transition={{ duration: 0.15, ease: "easeOut" }}
																className="pointer-events-none absolute right-2.5 top-1/2 -translate-y-1/2"
																aria-label={t("createProject.githubRepoChecking")}
																role="status"
															>
																<Spinner icon={LoaderCircle} className="size-3.5 text-muted-foreground" />
															</motion.span>
														) : null}
													</AnimatePresence>
												</div>
												{githubNameWasNormalized ? (
													<p id="githubRepoNameNormalization" className="text-[11px] leading-4 text-muted-foreground">
														{t("createProject.githubRepoWillCreate", { repo: githubName })}
													</p>
												) : null}
											</div>
											<div className="flex items-center justify-between gap-3 py-1">
												<Label htmlFor="githubRepoPrivate" className="flex cursor-pointer items-center gap-2.5 min-w-0">
													{isPrivate ? (
														<Lock className="size-4 shrink-0 text-[var(--color-text-import-muted)]" aria-hidden="true" />
													) : (
														<Globe className="size-4 shrink-0 text-[var(--color-text-import-muted)]" aria-hidden="true" />
													)}
													<div className="min-w-0 space-y-0.5">
														<span className="block text-[12px] font-medium leading-4 text-[var(--color-text-import-title)]">
															{visibilityLabel}
														</span>
														<span id="githubRepoVisibilityHelp" className="block text-[11px] leading-4 text-[var(--color-text-import-muted)]">
															{visibilityHelper}
														</span>
													</div>
												</Label>
												<Switch
													id="githubRepoPrivate"
													aria-label={visibilityLabel}
													aria-describedby="githubRepoVisibilityHelp"
													checked={isPrivate}
													disabled={disabled}
													onCheckedChange={(privateRepository) =>
														onChangeGitHubRepository({
															owner: githubRepository?.owner ?? "",
															name: githubRepository?.name ?? "",
															private: privateRepository,
														})
													}
												/>
											</div>
									</div>
								) : (
										<div className="space-y-2 rounded-md border border-border/70 bg-background/40 p-3">
											<GitSetupFields
												actions={validation.root.requiredActions}
												approved={missingApprovals.length === 0}
												disabled={disabled}
												onApprovalChange={(approved) => onChangeApprovedActions(approved ? [...validation.root.requiredActions, ...(optedIntoRemote ? ["create_remote_repository"] : [])] : [])}
												onRemoteChange={onChangeRemote}
												remoteUrl={remoteUrl}
												showActionSummary={false}
											/>
										</div>
									)}
									{latestEvents.length > 0 ? (
										<div className="space-y-1.5 rounded-md border border-border/70 bg-background/30 p-3" aria-live="polite">
											{latestEvents.map((event) => (
												<div key={`${event.repoPath}:${event.action}`} className="flex items-center gap-2 text-[12px]">
													{event.state === "success" ? <CheckCircle2 className="size-3.5 text-emerald-500" aria-hidden="true" /> : event.state === "error" ? <XCircle className="size-3.5 text-destructive" aria-hidden="true" /> : event.state === "running" ? <Spinner icon={CircleDashed} className="size-3.5 text-primary" /> : <CircleDashed className="size-3.5 text-muted-foreground" aria-hidden="true" />}
													<span className="min-w-0 flex-1 truncate">{displayImportPath(event.repoPath)} · {gitActionLabel(event.action)}</span>
													<span className="text-muted-foreground">{event.state === "success" ? t("createProject.projectSetupComplete", { defaultValue: "Done" }) : event.state === "error" ? t("createProject.projectSetupError", { defaultValue: "Needs attention" }) : event.state === "running" ? t("createProject.projectSetupInProgress", { defaultValue: "In progress" }) : t("createProject.projectSetupQueued", { defaultValue: "Queued" })}</span>
												</div>
											))}
										</div>
									) : null}
									{missingApprovals.length > 0 ? (
										<p className="text-[11px] leading-4 text-muted-foreground">
											{t("createProject.projectSetupContinue")}
										</p>
									) : null}
							</section>
						) : null}
					</div>
					<div className="flex shrink-0 items-center justify-end gap-2 px-4 pb-4 pt-3">
						{mustImportAsWorkspace ? (
							<Button type="button" variant="primary" disabled={disabled} onClick={onTryWorkspace}>{t("createProject.importAsWorkspace")}</Button>
						) : step === "blocked" ? (
							<>
								<Button type="button" variant="outline" disabled={disabled} onClick={onBack}>
									{t("createProject.back")}
								</Button>
								{hasChildRepos || Boolean(validation.warning) ? <Button type="button" variant="primary" disabled={disabled} onClick={onContinueProject}>{t("createProject.cloneContinue")}</Button> : null}
							</>
						) : null}
						{step === "prepare_git" ? (
							<>
								<Button type="button" variant="outline" disabled={disabled} onClick={onBack}>
									{t("createProject.back")}
								</Button>
								<Button type="button" variant="primary" disabled={continueDisabled} onClick={onContinue}>
									{hasFailedStep
										? t("createProject.retry")
										: needsRemote
											? t("createProject.createRepositoryAndContinue")
											: t("createProject.cloneContinue")}
								</Button>
							</>
						) : null}
					</div>
				</Dialog.Content>
			</Dialog.Portal>
		</Dialog.Root>
	);
}
