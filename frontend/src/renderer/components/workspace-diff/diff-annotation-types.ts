/** Annotation types and helpers for the workspace diff view. */
import type { FileAnnotationTarget } from "../../../shared/file-annotations";
import { type WorkspaceFileSummary } from "../../hooks/workspace-files/workspace-files-types";

export type WorkspaceFileStatus = WorkspaceFileSummary["status"];

export type ActiveFileAnnotationTarget = FileAnnotationTarget & {
	rowIndex?: number;
	/** Keeps one shared annotation model from rendering the composer in two panes. */
	surface?: "focused" | "review";
};
export type FileAnnotationStatus = "idle" | "sending" | "sent" | "error";
export type FileAnnotationModel = {
	/** Every open comment box, oldest first. They can sit on several lines and files at once. */
	targets: ActiveFileAnnotationTarget[];
	/** State of the send in progress or just finished, whichever comments it covers. */
	status: FileAnnotationStatus;
	/** The same, for one box: idle unless the send covers that comment. */
	statusFor: (target: ActiveFileAnnotationTarget) => FileAnnotationStatus;
	error: string;
	/** Opens a box on `target`, or closes the one already open there. */
	begin: (target: ActiveFileAnnotationTarget) => void;
	draftFor: (target: ActiveFileAnnotationTarget) => string;
	setDraft: (target: ActiveFileAnnotationTarget, draft: string) => void;
	/** Closes the box on `target`, or every box when no target is given. */
	cancel: (target?: ActiveFileAnnotationTarget) => void;
	/** Sends the comment on `target` (`text` is that box's local draft), or every written comment when no target is given. */
	submit: (target?: ActiveFileAnnotationTarget, text?: string) => Promise<void>;
};

/** Closes every open box that `match` picks out, leaving comments elsewhere alone. */
export function cancelFileAnnotations(annotation: FileAnnotationModel, match: (target: ActiveFileAnnotationTarget) => boolean): void {
	for (const target of annotation.targets.filter(match)) annotation.cancel(target);
}

export function fileAnnotationKey(target: ActiveFileAnnotationTarget): string {
	return [target.surface ?? "", target.scope ?? "", target.side, target.line ?? "", target.path, target.quote ?? ""].join("\u0000");
}

// Split (old | new) view only means something when both sides have content to
// compare. Added files have nothing on the old side; deleted files have
// nothing on the new side — splitting them just wastes half the pane on an
// empty column, so those always render unified regardless of the toggle.
export function canSplitCompare(status: WorkspaceFileStatus): boolean {
	return status === "modified" || status === "renamed";
}
