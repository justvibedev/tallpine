/** Import repository rows, git setup fields and repository avatars. */
import { AppLink } from "../AppLink";
import { useTranslation } from "react-i18next";
import { Folder, GitBranch, XCircle } from "lucide-react";
import { useState } from "react";
import type { ImportFolderScan } from "../../../preload";
import { cn } from "../../lib/utils";
import { Checkbox } from "../ui/checkbox";
import { Input } from "../ui/input";
import { RemoteImg } from "../RemoteImg";
import { DisplayImportRepo, ImportValidationResult, GIT_PREPARATION_ACTIONS } from "./create-project-types";
import { gitActionLabel } from "./create-project-helpers";

export function ImportRepoRow({ excluded = false, failed = false, onSetup, repo, setupExpanded = false }: { excluded?: boolean; failed?: boolean; onSetup?: () => void; repo: DisplayImportRepo; setupExpanded?: boolean }) {
	const { t } = useTranslation();
	const repositoryAvatar = repo.hasRemote ? repositoryAvatarFromGitUrl(repo.remote) : null;
	const needsSetup = repo.requiredActions.length > 0;
	const isPlainFolder = repo.needsGitInit && !repo.isRepo && !needsSetup;
	const repositoryUrl = !failed && !needsSetup && !isPlainFolder ? repositoryWebUrl(repo.remote) : null;
	return (
		<div className={cn("flex shrink-0 items-center gap-2.5 py-1.5 pl-3", isPlainFolder ? "pr-1.5" : "pr-3")}>
			<div className="flex size-4 shrink-0 items-center justify-center">
				{failed ? <XCircle className="size-4 text-destructive" aria-hidden="true" /> : isPlainFolder ? <Folder className="size-4 text-[var(--color-text-import-muted)]" aria-hidden="true" /> : repositoryAvatar ? <ImportRepositoryAvatar owner={repositoryAvatar.owner} url={repositoryAvatar.url} /> : <Folder className="size-4 text-[var(--color-text-import-muted)]" aria-hidden="true" />}
			</div>
			<div className="min-w-0 flex-1 truncate text-[13px] font-semibold text-[var(--color-text-import-title)]">{repo.name}</div>
			<div className="flex max-w-[220px] shrink-0 items-center gap-1 truncate text-right text-[11px] text-[var(--color-text-import-muted)]">
				{needsSetup ? onSetup ? <button type="button" aria-expanded={setupExpanded} className="rounded-sm border border-orange-400/40 bg-orange-500/15 px-2 py-0.5 text-orange-300 hover:bg-orange-500/25" onClick={onSetup}>{setupExpanded ? "Hide setup" : `${workspaceSetupLabel(repo)} · Set up`}</button> : <span className="rounded-sm border border-orange-400/40 bg-orange-500/15 px-2 py-0.5 text-orange-300">{excluded ? t("createProject.repoNotIncluded", { reason: workspaceSetupLabel(repo) }) : t("createProject.setupRequired")}</span> : repositoryUrl ? <><GitBranch className="size-3.5 shrink-0" aria-hidden="true" /><AppLink className="truncate underline decoration-border underline-offset-2 hover:text-foreground" href={repositoryUrl} rel="noreferrer" target="_blank">{repo.branch}</AppLink></> : <><span className={cn("truncate", isPlainFolder && "rounded-sm bg-orange-500/15 px-2 py-0.5 text-orange-300")}>{isPlainFolder ? "Needs git init" : failed ? (repo.reason ?? t("createProject.repoCannotImport")) : repo.branch}</span></>}
			</div>
		</div>
	);
}

export function workspaceSetupLabel(repo: DisplayImportRepo): string {
	const actions = new Set(repo.requiredActions);
	if (actions.size === 1 && actions.has("set_remote")) return "No remote";
	if (actions.size === 1 && actions.has("git_commit")) return "No commits";
	if (actions.has("git_init")) return "Not a Git repo";
	return "Git setup needed";
}

export function normalizeImportRepos(repos: ImportFolderScan["repos"]): DisplayImportRepo[] {
	return repos.map((repo) => ({ ...repo, requiredActions: [], blockingErrors: [] }));
}

export function WorkspaceImportRepoList({ excludeUnready, repos }: { excludeUnready: boolean; repos: DisplayImportRepo[] }) {
	const orderedRepos = [...repos].sort((left, right) => (Number(right.requiredActions.length > 0) - Number(left.requiredActions.length > 0)) || left.name.localeCompare(right.name));
	return <div className="divide-y divide-border/50 overflow-hidden rounded-sm bg-[var(--color-bg-import-card)]">
		{orderedRepos.map((repo) => <ImportRepoRow excluded={excludeUnready && repo.requiredActions.length > 0} key={repo.path} repo={repo} />)}
	</div>;
}

export function GitSetupFields({ actions, approved, disabled, onApprovalChange, onRemoteChange, remoteAriaLabel = "Origin remote URL", remotePlaceholder = "https://github.com/owner/repository.git", remoteUrl, showActionSummary = true }: {
	actions: string[];
	approved: boolean;
	disabled: boolean;
	onApprovalChange: (approved: boolean) => void;
	onRemoteChange: (remoteUrl: string) => void;
	remoteAriaLabel?: string;
	remotePlaceholder?: string;
	remoteUrl: string;
	showActionSummary?: boolean;
}) {
	const { t } = useTranslation();
	return <div className="space-y-2">
		<label className="flex items-start gap-2 text-[12px] text-[var(--color-text-import-title)]">
			<Checkbox checked={approved} className="mt-0.5" disabled={disabled} onCheckedChange={(checked) => onApprovalChange(checked === true)} />
			<span className="min-w-0 flex-1"><span className="block font-medium">{t("createProject.setupGitProject")}</span>{showActionSummary ? <span className="block text-[11px] leading-4 text-[var(--color-text-import-muted)]">{actions.map(gitActionLabel).join(", ")}</span> : null}</span>
		</label>
		{actions.includes("set_remote") ? <Input aria-label={remoteAriaLabel} className="h-8 bg-[var(--color-bg-import-card)] font-mono text-[12px]" disabled={disabled} placeholder={remotePlaceholder} value={remoteUrl} onChange={(event) => onRemoteChange(event.target.value)} /> : null}
	</div>;
}

export function mergeWorkspaceImportRepos(scan: ImportFolderScan | null, validation: ImportValidationResult | null): DisplayImportRepo[] {
	const metadata = new Map((scan?.repos ?? []).map((repo) => [repo.path, repo]));
	const statuses = new Map((validation?.childRepos ?? []).map((repo) => [repo.repoPath, repo]));
	const paths = new Set([...metadata.keys(), ...statuses.keys()]);
	return [...paths].map((path) => {
		const repo = metadata.get(path);
		const status = statuses.get(path);
		return {
			name: repo?.name ?? path.split(/[\\/]/).pop() ?? path,
			path,
			relativePath: repo?.relativePath ?? ".",
			branch: repo?.branch ?? "",
			remote: repo?.remote ?? "",
			hasRemote: status?.hasOrigin ?? repo?.hasRemote ?? false,
			status: repo?.status ?? (status?.blockingErrors.length ? "error" : "ok"),
			reason: repo?.reason ?? status?.blockingErrors[0],
			needsGitInit: status?.needsGitInit ?? repo?.needsGitInit,
			requiredActions: repo?.isRepo !== undefined ? scanRequiredActions(repo) : status?.requiredActions ?? [],
			blockingErrors: status?.blockingErrors ?? [],
			isRepo: status?.isRepo ?? repo?.isRepo ?? false,
			hasCommit: status?.hasCommit ?? repo?.hasCommit ?? false,
			hasOrigin: status?.hasOrigin ?? repo?.hasRemote,
		};
	}).filter((repo) => repo.isRepo).sort((left, right) => left.name.localeCompare(right.name));
}

export function scanRequiredActions(repo: ImportFolderScan["repos"][number] | undefined): string[] {
	if (!repo || repo.status === "error") return [];
	if (!repo.isRepo) return [...GIT_PREPARATION_ACTIONS];
	return [...(!repo.hasCommit ? ["git_commit"] : []), ...(!repo.hasRemote ? ["set_remote"] : [])];
}

export function repositoryAvatarFromGitUrl(remote: string): { owner: string; url: string } | null {
	const webUrl = repositoryWebUrl(remote);
	if (!webUrl) return null;
	try {
		const parsed = new URL(webUrl);
		const [owner] = parsed.pathname.split("/").filter(Boolean);
		if (!owner) return null;
		if (parsed.hostname === "github.com") return { owner, url: `https://github.com/${owner}.png?size=64` };
		if (parsed.hostname === "gitlab.com") return { owner, url: `https://gitlab.com/${owner}/-/avatar` };
		return null;
	} catch {
		return null;
	}
}

export function repositoryWebUrl(remote: string): string | null {
	const value = remote.trim();
	const scp = value.match(/^[^/@:\s]+@([^/:\s]+):(.+)$/);
	if (scp?.[1] && scp[2]) return `https://${scp[1]}/${scp[2].replace(/^\/+|\/+$/g, "").replace(/\.git$/, "")}`;
	try {
		const parsed = new URL(value);
		const path = parsed.pathname.replace(/^\/+|\/+$/g, "").replace(/\.git$/, "");
		return parsed.hostname && path ? `https://${parsed.hostname}/${path}` : null;
	} catch {
		return null;
	}
}

export function ImportRepositoryAvatar({ owner, url }: { owner: string; url: string }) {
	const [state, setState] = useState<"loading" | "loaded" | "failed">("loading");
	return <span className="relative block size-4" aria-hidden="true"><RemoteImg alt="" className={cn("absolute inset-0 size-4 rounded-full object-cover", state === "loaded" ? "opacity-100" : "opacity-0")} onError={() => setState("failed")} onLoad={() => setState("loaded")} referrerPolicy="no-referrer" src={url} />{state === "loading" ? <span className="absolute inset-0 size-4 animate-pulse rounded-full bg-muted-foreground/40" /> : null}{state === "failed" ? <span className="absolute inset-0 size-4 rounded-full bg-muted text-center text-[7px] font-semibold leading-4 text-muted-foreground">{owner.slice(0, 2).toUpperCase()}</span> : null}</span>;
}

export function displayImportPath(value: string) {
	return value.replace(/^\/Users\/[^/]+/, "~");
}
