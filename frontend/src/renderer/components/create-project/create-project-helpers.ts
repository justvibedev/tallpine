/** Pure helpers for project import and creation messages and actions. */
import { PRODUCT_NAME } from "../../../shared/brand";
import { clientForSessionHost } from "../../lib/host-clients";
import { apiClient, apiErrorMessage } from "../../lib/api-client";
import { ImportValidationResult, GIT_ACTION_LABELS, GIT_PREPARATION_ACTIONS, ProjectGitHubRepository, GitPreparationEvent, LAST_IMPORT_REMOTE_URL_KEY } from "./create-project-types";
import { displayImportPath } from "./ImportRepoList";

export function isRepositorySetupRecoveryCode(code: string | undefined): code is "NOT_A_GIT_REPO" | "PROJECT_UNBORN" {
	return code === "NOT_A_GIT_REPO" || code === "PROJECT_UNBORN";
}

export function sameProjectPath(left: string, right: string): boolean {
	const normalize = (path: string) => path.trim().replace(/[\\/]+$/, "").replaceAll("\\", "/");
	return normalize(left) === normalize(right);
}

export function safeProjectCreationError(message: string, fallback: string, code?: string): string {
	const cleaned = message
		.replace(/^Setup failed:\s*/i, "")
		.replace(/\s*\([A-Z][A-Z0-9_]+\)\s*$/, "")
		.trim();
	if (
		code !== undefined ||
		cleaned === "" ||
		/\b(?:request[_ -]?id|rpc|stack|stderr|stdout|exit status|internal server error|panic)\b/i.test(cleaned) ||
		/\bfatal:/i.test(cleaned) ||
		/\b[A-Z][A-Z0-9_]{3,}\b/.test(cleaned)
	) {
		return fallback;
	}
	return cleaned;
}

export async function validateImportFolder(path: string, importKind: "project" | "workspace", hostId?: string): Promise<ImportValidationResult> {
	const { data, error } = await (hostId ? clientForSessionHost(hostId) : apiClient).POST("/api/v1/imports/validate", { body: { importKind, path } });
	if (error || !data) throw new Error(apiErrorMessage(error, "Could not validate this folder."));
	return data;
}

export function importValidationMessage(result: ImportValidationResult): string {
	if (result.blockingErrors.length === 0) return "This folder cannot be imported yet.";
	return result.blockingErrors.map(importBlockingErrorLabel).join(" ");
}

export function importBlockingErrorLabel(code: string): string {
	switch (code) {
		case "INVALID_PATH":
			return `Choose a folder ${PRODUCT_NAME} can read.`;
		case "PATH_NOT_DIRECTORY":
			return "Choose a folder, not a file.";
		case "BARE_REPOSITORY":
			return "Choose a normal working checkout instead of a bare Git repository.";
		case "UNSUPPORTED_GIT_METADATA":
			return "Repair the Git metadata or choose a different folder.";
		case "CHILD_REPO_SCAN_FAILED":
			return `${PRODUCT_NAME} could not inspect the repositories under this folder.`;
		case "IMPORT_PATH_UNSAFE":
			return `Choose a specific project folder outside ${PRODUCT_NAME}'s own state directories.`;
		case "DETACHED_HEAD":
			return "This repository is checked out in detached HEAD state. Check out a named branch before importing it.";
		case "WORKSPACE_CHILD_REPO_REQUIRED":
			return "This workspace needs at least one direct child Git repository.";
		default:
			return "Choose a different folder or repair the repository before continuing.";
	}
}

export function gitActionLabel(action: string): string {
	return GIT_ACTION_LABELS[action] ?? "Git setup";
}

export function orderedProjectActions(actions: string[]): string[] {
	const rank = (action: string) => {
		const index = GIT_PREPARATION_ACTIONS.indexOf(action as (typeof GIT_PREPARATION_ACTIONS)[number]);
		return index < 0 ? GIT_PREPARATION_ACTIONS.length : index;
	};
	return [...actions].sort((left, right) => rank(left) - rank(right));
}

export function importNeedsRemoteSetup(actions: string[]): boolean {
	return actions.includes("create_remote_repository") || actions.includes("set_remote");
}

export function defaultProjectGitHubRepository(repoPath: string): ProjectGitHubRepository {
	return {
		owner: "",
		name: projectNameFromPath(repoPath) || "repository",
		private: true,
	};
}

export function projectNameFromPath(repoPath: string): string {
	return repoPath.split(/[\\/]/).filter(Boolean).pop()?.replace(/\.git$/i, "").trim() ?? "";
}

export function githubRepositoryRemoteUrl(repository: ProjectGitHubRepository): string {
	const owner = repository.owner.trim();
	const name = normalizeGitHubRepositoryName(repository.name);
	if (!owner || !name) return "";
	return `https://github.com/${owner}/${name}.git`;
}

export function normalizeGitHubRepositoryName(value: string): string {
	return value.trim().replace(/\s+/g, "-").replace(/\.git$/i, "");
}

export function projectRequestedActionEvents(repoPath: string, actions: string[]): GitPreparationEvent[] {
	const ordered = orderedProjectActions(actions);
	return ordered.map((action, index) => ({
		repoPath,
		action: action as GitPreparationEvent["action"],
		state: index === 0 ? "running" : "pending",
	}));
}

export function mergePreparationEvents(current: GitPreparationEvent[], incoming: GitPreparationEvent[]): GitPreparationEvent[] {
	const latest = new Map<string, GitPreparationEvent>();
	for (const event of [...current, ...incoming]) latest.set(`${event.repoPath}:${event.action}`, event);
	return [...latest.values()];
}

export function isValidProjectRemote(value: string): boolean {
	if (!value || /\s/.test(value) || value.startsWith("-")) return false;
	if (/^[^/@:\s]+@[^/\s]+:.+\/.+$/.test(value)) return true;
	try {
		const parsed = new URL(value);
		return ["file:", "git:", "http:", "https:", "ssh:"].includes(parsed.protocol) &&
			parsed.pathname.split("/").filter(Boolean).length >= 1;
	} catch {
		return false;
	}
}

export function persistSuggestedProjectRemoteUrl(remoteUrl: string) {
	if (typeof window === "undefined") return;
	window.localStorage.setItem(LAST_IMPORT_REMOTE_URL_KEY, remoteUrl.trim());
}

export function projectPreparationFailureMessage(event: GitPreparationEvent): string {
	return `${displayImportPath(event.repoPath)} failed while running ${gitActionLabel(event.action)}. Review the step below, then retry or go back.`;
}

export function shouldScanCreateFailure(message: string): boolean {
	if (/daemon|server|conflict|already exists|not ready|start|orchestrator|permission denied/i.test(message))
		return false;
	if (/\b(?:PATH|ID)_ALREADY_REGISTERED\b/i.test(message) || /already registered/i.test(message)) return false;
	return /workspace|repo|repository|git|path|folder|worktree|bare|branch|commit|remote/i.test(message);
}
