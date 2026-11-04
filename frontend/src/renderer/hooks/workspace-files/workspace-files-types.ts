/** Types for workspace file, diff and review queries. */
import type { components } from "../../../api/schema";

export type WorkspaceCompareMode = "base" | "head_fallback";
export type WorkspaceFileSummary = Omit<components["schemas"]["WorkspaceFileSummary"], "editable" | "fileFingerprint"> & {
	editable?: boolean;
	previousPath?: string;
	fileFingerprint?: string;
};
export type WorkspaceFileSections = components["schemas"]["WorkspaceFileSections"];
export type WorkspaceCommitSummary = components["schemas"]["WorkspaceCommitSummary"];
export type WorkspaceViewInfo = components["schemas"]["WorkspaceViewResponse"];
export type WorkspaceHistoryResponse = Omit<components["schemas"]["WorkspaceHistoryResponse"], "view"> & {
	/** Absent from an older daemon. */
	view?: WorkspaceViewInfo;
};
export type WorkspaceSummary = components["schemas"]["WorkspaceSummary"];
export type WorkspaceBranches = components["schemas"]["WorkspaceBranchesResponse"];
export type WorkspaceBranch = components["schemas"]["WorkspaceBranchResponse"];
export type WorkspaceCommitConflict = components["schemas"]["WorkspaceCommitConflictResponse"];
export type WorkspaceCommitSelection = Omit<components["schemas"]["WorkspaceCommitSelectionResponse"], "files"> & {
	files: WorkspaceFileSummary[];
};
export type WorkspaceFilesResponse = Omit<components["schemas"]["ListWorkspaceFilesResponse"], "files" | "sections" | "workspaceVersion" | "degraded" | "degradedCode"> & {
	compareMode?: WorkspaceCompareMode;
	files: WorkspaceFileSummary[];
	sections: {
		/** Absent from an older daemon. */
		uncommitted?: WorkspaceFileSummary[];
		committed: WorkspaceFileSummary[];
		staged: WorkspaceFileSummary[];
		unstaged: WorkspaceFileSummary[];
		untracked: WorkspaceFileSummary[];
	};
	workspaceVersion?: string;
	degraded?: boolean;
	degradedCode?: string;
	stale?: boolean;
	refreshing?: boolean;
};
export type WorkspaceFileDetail = Omit<components["schemas"]["WorkspaceFileResponse"], "editable" | "fileFingerprint" | "workspaceVersion"> & {
	/** HTTP validator for streamed artifacts, so an unchanged refetch keeps playing. */
	mediaVersion?: string;
	editable?: boolean;
	previousPath?: string;
	compareMode?: WorkspaceCompareMode;
	fileFingerprint?: string;
	workspaceVersion?: string;
};
export type WorkspaceDiffScope = components["schemas"]["WorkspaceDiffRequest"]["scope"];
export type WorkspaceDiffsResponse = components["schemas"]["WorkspaceDiffsResponse"];
export type WorkspaceFileRevision = components["schemas"]["WorkspaceFileRevisionResponse"];
export type WorkspaceFileSearchResponse = components["schemas"]["WorkspaceFileSearchResponse"];
export type FilesSource =
	| { kind: "workspace" }
 | { kind: "snapshot"; view?: WorkspaceViewParams; commitShas?: readonly string[]; label: string }
	| { kind: "pull_request"; number: number; url: string; label: string; snapshot?: string }
	/**
	 * `readOnly` hides the edit action.
	 */
	| { kind: "artifact"; rawUrl?: string; readOnly?: boolean };

/** The branch the Files review reads. Only the checked-out branch reads the worktree; the others read revisions. */
export type FilesBranchTarget =
	| { kind: "checked_out" }
	/** `tipSha` is the tip the branch list showed when it was picked. */
	| { kind: "branch"; name: string; tipSha?: string }
	| { kind: "pull_request"; number: number; url: string; branch: string; tipSha?: string };
/**
 * The changes the Files review shows. `combined` is the "Changes" scope an
 * older daemon has instead of `uncommitted`. `commits` lists ticked SHAs newest
 * first; none is All commits.
 */
export type FilesChanges =
	| { kind: "uncommitted" | "staged" | "unstaged" | "combined" }
	| { kind: "commits"; shas: readonly string[] };
/** `changes: null` follows the default until the user picks. */
export type FilesSelection = { branch: FilesBranchTarget; changes: FilesChanges | null };
/** Selects another branch or a PR on a read; none reads the worktree. The expected SHAs fence it to what its history listed. */
export type WorkspaceViewParams = {
	branch?: string;
	prNumber?: number;
	sourceUrl?: string;
	expectedTipSha?: string;
	expectedBaseSha?: string;
};
/** What the review pane reads for the picked branch and changes. */
export type ReviewTarget = {
	/** Viewed marks are stored under it, and the pane resets when it changes. */
	key: string;
	branch: FilesBranchTarget;
	changes: FilesChanges;
	scope: WorkspaceDiffScope;
	/** Undefined reads the whole range (or a working scope). */
	commitShas?: readonly string[];
	/** Undefined reads the checked-out branch. */
	view?: WorkspaceViewParams;
	/** The manifest's version, which fences checked-out reads. */
	workspaceVersion?: string;
	/** "" for the worktree's scopes and its whole range; see workspaceSelectionKey. */
	selectionKey: string;
	/** Undefined until known. */
	files?: WorkspaceFileSummary[];
	/** Set when exactly one commit is picked. */
	sha?: string;
	subject?: string;
	/** Historical targets carry their source when opening a file. */
	openable: boolean;
	source?: FilesSource;
	state: "ready" | "pending" | "error" | "not_combinable";
	conflict?: WorkspaceCommitConflict & { subject?: string };
	error?: Error;
};
