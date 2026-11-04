import { PRODUCT_NAME } from "../../../../shared/brand";
import { useMemo, useState } from "react";
import { File as FileIcon, Loader2 } from "lucide-react";
import { cn } from "../../../lib/utils";
import { Tooltip, TooltipContent, TooltipTrigger } from "../../ui/tooltip";
import { type DiffStatus, type ConversationItem, type TurnDiff } from "../../../types/conversation";
import { resolveTurnFilePath, turnFileOpenPath, turnPathHints } from "../../../lib/turn-file-open-path";
import { shortenPaths } from "./format";
import { Spinner } from "../../ui/spinner";

/** Single letter per change kind, which is how a diff list is normally read. */
export const diffStatusMark: Record<DiffStatus, { mark: string; tone: string; label: string }> = {
	added: { mark: "A", tone: "text-success", label: "added" },
	modified: { mark: "M", tone: "text-accent", label: "modified" },
	deleted: { mark: "D", tone: "text-destructive", label: "deleted" },
	renamed: { mark: "R", tone: "text-muted-foreground", label: "renamed" },
};

/** Match the daemon's file_change summary verbs (Created/Deleted/Renamed/Edited). */
export function fileChangeVerb(status: DiffStatus): string {
	switch (status) {
		case "added":
			return "Created";
		case "deleted":
			return "Deleted";
		case "renamed":
			return "Renamed";
		default:
			return "Edited";
	}
}

/**
 * What a turn changed on disk.
 *
 * A bordered summary card at the end of the turn (kept near rollback), not the
 * compact explore line used for mid-turn activity. Always shows the changed
 * files; Review opens the Files rail, and clicking a row focuses that path.
 *
 * Rendered only when the daemon reported a diff. An agent that cannot report one
 * gets no empty panel implying it changed nothing.
 */
export function TurnChangedFiles({
	diff,
	live,
	onReview,
	onOpenFile,
	items,
}: {
	diff: TurnDiff;
	live?: boolean;
	/** Opens the session Files inspector for the full workspace diff. */
	onReview?: () => void;
	/** Opens the Files inspector focused on this path. */
	onOpenFile?: (path: string, line?: number) => void;
	/**
	 * Timeline items from the same turn. Turn diffs often carry repo-relative
	 * basenames (`random_words.txt`); file_change rows and command cwds often
	 * carry the absolute worktree path the Edited tooltip already shows.
	 */
	items?: ConversationItem[];
}) {
	const [expanded, setExpanded] = useState(false);
	const pathHints = useMemo(() => turnPathHints(items), [items]);
	if (diff.files.length === 0) return null;

	const previewLimit = 4;
	const hidden = Math.max(0, diff.files.length - previewLimit);
	const visible = expanded ? diff.files : diff.files.slice(0, previewLimit);

	return (
		<div className="overflow-hidden rounded-lg bg-surface">
			<div className="flex items-center gap-2 px-3 py-2">
				<span className="shrink-0 text-xs text-muted-foreground">
					{diff.files.length === 1 ? "1 File Changed" : `${diff.files.length} Files Changed`}
				</span>
				{live ? (
					<Spinner icon={Loader2}
						aria-label="still changing"
						
					 className="size-3 shrink-0 text-muted-foreground/60" />
				) : null}
				<span className="flex-1" />
				{onReview ? (
					<button
						type="button"
						onClick={onReview}
						className="shrink-0 text-xs text-muted-foreground transition-colors hover:text-foreground"
					>
						Review
					</button>
				) : null}
			</div>

			<ul className="flex flex-col px-1.5 pb-1.5">
				{visible.map((file) => {
					const status = diffStatusMark[file.status] ?? diffStatusMark.modified;
					const rowClass =
						"flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left transition-colors hover:bg-interactive-hover";
					const tooltipPath = resolveTurnFilePath(file.path, pathHints);
					const tooltipOldPath = file.oldPath
						? resolveTurnFilePath(file.oldPath, pathHints)
						: undefined;
					const openPath = turnFileOpenPath(file.path, pathHints);
					const location = fileLocationLabel(tooltipPath, tooltipOldPath);

					const body = (
						<>
							<span className="sr-only">{status.label}</span>
							<FileIcon aria-hidden="true" className="size-3.5 shrink-0 text-muted-foreground" />
							<span
								className="min-w-0 flex-1 truncate text-xs text-foreground/80"
								title=""
							>
								{fileBasename(file.path)}
							</span>
							{file.additions > 0 ? (
								<span className="shrink-0 font-mono text-caption tabular-nums text-success">
									+{file.additions}
								</span>
							) : null}
							{file.deletions > 0 ? (
								<span className="shrink-0 font-mono text-caption tabular-nums text-destructive">
									&minus;{file.deletions}
								</span>
							) : null}
							{file.additions === 0 && file.deletions === 0 ? (
								<span className="shrink-0 font-mono text-caption tabular-nums text-muted-foreground/50">
									0
								</span>
							) : null}
						</>
					);

					return (
						<li key={`${file.status}-${file.oldPath ?? ""}-${file.path}`}>
							{onOpenFile ? (
								<Tooltip>
									<TooltipTrigger asChild>
										<button
											type="button"
											onClick={() => onOpenFile(tooltipPath)}
											aria-label={`Open ${openPath} in Files`}
											className={rowClass}
										>
											{body}
										</button>
									</TooltipTrigger>
									<TooltipContent side="top" className="max-w-[min(28rem,90vw)] font-mono text-caption font-normal">
										{location}
									</TooltipContent>
								</Tooltip>
							) : (
								<div className={rowClass}>
									<span className="sr-only">{status.label}</span>
									<FileIcon aria-hidden="true" className="size-3.5 shrink-0 text-muted-foreground" />
									<FileLocationLabel
										path={file.path}
										oldPath={file.oldPath}
										locationPath={tooltipPath}
										locationOldPath={tooltipOldPath}
										className="min-w-0 flex-1 truncate text-xs text-foreground/80"
									/>
									{file.additions > 0 ? (
										<span className="shrink-0 font-mono text-caption tabular-nums text-success">
											+{file.additions}
										</span>
									) : null}
									{file.deletions > 0 ? (
										<span className="shrink-0 font-mono text-caption tabular-nums text-destructive">
											&minus;{file.deletions}
										</span>
									) : null}
									{file.additions === 0 && file.deletions === 0 ? (
										<span className="shrink-0 font-mono text-caption tabular-nums text-muted-foreground/50">
											0
										</span>
									) : null}
								</div>
							)}
						</li>
					);
				})}
			</ul>

			{hidden > 0 ? (
				<button
					type="button"
					onClick={() => setExpanded((prev) => !prev)}
					aria-expanded={expanded}
					className="flex w-full items-center gap-1.5 px-3 pb-2 text-left text-xs text-muted-foreground transition-colors hover:text-foreground"
				>
					{expanded ? "Show less" : `Show ${hidden} more`}
				</button>
			) : null}

			{diff.truncated ? (
				<p className="px-3 pb-2 text-xs leading-relaxed text-warning">
					This turn changed more files than {PRODUCT_NAME} lists here.
					{onReview ? " Use Review for the whole change." : " Open the Files tab for the whole change."}
				</p>
			) : null}
		</div>
	);
}

/**
 * Basename only — color distinguishes it from "Edited", no hover fill. Hovering
 * shows the home-shortened worktree path in a monospace tooltip.
 */
export function FileLocationLabel({
	path,
	oldPath,
	locationPath,
	locationOldPath,
	className,
}: {
	path: string;
	oldPath?: string;
	/** Absolute/worktree path for the tooltip when `path` is only a basename. */
	locationPath?: string;
	locationOldPath?: string;
	className?: string;
}) {
	const location = fileLocationLabel(locationPath ?? path, locationOldPath ?? oldPath);

	return (
		<Tooltip>
			<TooltipTrigger asChild>
				{/* `title=""` blocks Chromium's native ellipsis tooltip so only the
				    path tooltip below appears — otherwise hover shows the basename. */}
				<span
					className={cn(
						"min-w-0 truncate text-xs text-foreground/65 outline-none",
						className,
					)}
					title=""
				>
					{fileBasename(path)}
				</span>
			</TooltipTrigger>
			<TooltipContent side="top" className="max-w-[min(28rem,90vw)] font-mono text-caption font-normal">
				{location}
			</TooltipContent>
		</Tooltip>
	);
}

function fileLocationLabel(path: string, oldPath?: string): string {
	return oldPath ? `${shortenPaths(oldPath)} → ${shortenPaths(path)}` : shortenPaths(path);
}

/** Basename only — the row is too narrow for a full path; the tooltip carries that. */
export function fileBasename(path: string): string {
	const slash = Math.max(path.lastIndexOf("/"), path.lastIndexOf("\\"));
	return slash >= 0 ? path.slice(slash + 1) : path;
}

/* -------------------------------------------------------------------------- */
/* turn boundary                                                               */
/* -------------------------------------------------------------------------- */
