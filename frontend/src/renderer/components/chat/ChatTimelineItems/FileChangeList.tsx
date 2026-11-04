import { PRODUCT_NAME } from "../../../../shared/brand";
import { useState } from "react";
import { ChevronRight } from "lucide-react";
import { cn } from "../../../lib/utils";
import { type FileChangeFile } from "../../../types/conversation";
import { ToolDiffCode } from "./McpToolRow";
import { diffStatusMark, fileChangeVerb, FileLocationLabel } from "./TurnChangedFiles";

/**
 * The files one edit touched, and what it did to them.
 *
 * Styled like the explore summary's nested lines — "Edited FAQ.tsx +1 −1" — so an
 * expanded edit reads the same as the turn-level changed-files list. Hovering the
 * basename shows the full location; a file that carries a patch can still open it.
 */
export function FileChangeList({ files }: { files: FileChangeFile[] }) {
	if (!files.length) return null;
	return (
		<ul className="flex flex-col">
			{files.map((file) => (
				<FileChangeRow key={`${file.oldPath ?? ""}→${file.path}`} file={file} />
			))}
		</ul>
	);
}

function FileChangeRow({ file }: { file: FileChangeFile }) {
	const [open, setOpen] = useState(false);
	const status = diffStatusMark[file.status ?? "modified"] ?? diffStatusMark.modified;
	const hasPatch = Boolean(file.patch);
	const accessibleName = `${status.label} ${file.path}`;

	const line = (
		<>
			<span className="sr-only">{status.label}</span>
			<span className="shrink-0 text-xs text-muted-foreground">
				{fileChangeVerb(file.status ?? "modified")}
			</span>
			<FileLocationLabel path={file.path} oldPath={file.oldPath} />
			<span className="shrink-0 font-mono text-micro tabular-nums text-success">
				+{file.additions}
			</span>
			<span className="shrink-0 font-mono text-micro tabular-nums text-destructive">
				&minus;{file.deletions}
			</span>
		</>
	);

	// A file with no patch is not a button: nothing opens, and a control that does
	// nothing when pressed is worse than plain text.
	if (!hasPatch) {
		return (
			<li className="flex items-center gap-1.5 py-0.5 pr-1" aria-label={accessibleName}>
				{line}
			</li>
		);
	}

	return (
		<li className="flex flex-col">
			<button
				type="button"
				onClick={() => setOpen((prev) => !prev)}
				aria-expanded={open}
				aria-label={accessibleName}
				className="flex items-center gap-1.5 rounded-sm py-0.5 pr-1 text-left"
			>
				{line}
				<ChevronRight
					aria-hidden="true"
					className={cn(
						"size-3 shrink-0 text-muted-foreground/40 transition-transform",
						open && "rotate-90",
					)}
				/>
			</button>
			{open ? <Patch patch={file.patch!} truncated={file.patchTruncated} /> : null}
		</li>
	);
}

/**
 * One file's unified diff.
 *
 * Highlighted by the same engine the prose fences use — `diff` is one of the shipped
 * grammars — rather than by a second highlighter that would double the payload and
 * eventually disagree with the first.
 *
 * An added file's "patch" is its whole contents with no hunk header, which the diff
 * grammar renders as plain text. That is the right outcome: there is no diff to
 * colour, only a new file to read.
 */
export function fileChangePatch(file: FileChangeFile | undefined): string | undefined {
	if (!file) return undefined;
	if (file.patch) return file.patch;
	if (file.oldText === undefined && file.newText === undefined) return undefined;
	const oldLines = file.oldText?.split("\n") ?? [];
	const newLines = file.newText?.split("\n") ?? [];
	const header = `--- ${file.path}\n+++ ${file.path}`;
	return [
		header,
		...oldLines.map((line) => `-${line}`),
		...newLines.map((line) => `+${line}`),
	].join("\n");
}

export function Patch({ patch, truncated }: { patch: string; truncated?: boolean }) {
	return (
		// `chat-code` is what the token colours are scoped to, so a patch without it
		// tokenizes correctly and renders in one flat colour.
			<div className="mb-1 mt-0.5 overflow-hidden bg-background">
			<ToolDiffCode text={patch} />
			{truncated ? (
				<p className="border-t border-border px-2.5 py-1.5 text-xs leading-relaxed text-warning">
					This patch is longer than {PRODUCT_NAME} stores, so it stops early. The whole change is in the
					worktree and in the turn&rsquo;s diff.
				</p>
			) : null}
		</div>
	);
}

/* -------------------------------------------------------------------------- */
/* reasoning                                                                   */
/* -------------------------------------------------------------------------- */
