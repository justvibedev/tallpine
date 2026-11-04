/** Props of the workspace review pane. */
import { type ReviewTarget } from "../../../hooks/workspace-files/workspace-files-types";
import { type FileOpenOptions } from "../../FileContentPane";
import { type FileAnnotationModel } from "../../workspace-diff/diff-annotation-types";

export type WorkspaceReviewPaneProps = {
	annotation: FileAnnotationModel;
	/** What to review: the Files header's branch and changes. */
	target: ReviewTarget;
	/** Reads a failed target again. */
	onRetry?: () => void;
	filter: string;
	/** Offered when there is nothing to review; omitted where the file tree can't show the change. */
	onBrowseAll?: () => void;
	/** Opens the file at the selected review source, including historical targets. */
	onOpenFile?: (path: string, options?: FileOpenOptions) => void;
	/**
	 * False only when the host has no center navigation callback. A maximized
	 * host closes its overlay before handing the file to the center.
	 */
	canOpenInCenter?: boolean;
	/** Ways out of commits that can't be combined. */
	onIncludeCommitsBetween?: () => void;
	onShowAllCommits?: () => void;
	sessionId: string;
	hostId?: string;
	split: boolean;
	/** From the Files toolbar: collapse (or expand) every file; a new key asks again. */
	collapseRequest?: { expand: boolean; key: number };
	/** Tells the toolbar whether every file is collapsed, so it offers the other action. */
	onAllCollapsedChange?: (allCollapsed: boolean) => void;
	/** From the changed-files rail: bring this file's diff into view; a new key asks again. */
	scrollRequest?: { path: string; key: number };
};
