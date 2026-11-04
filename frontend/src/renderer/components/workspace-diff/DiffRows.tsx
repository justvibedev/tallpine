/** Hunk bands, row content and split-row computation for the diff view. */
import { memo } from "react";
import type { TFunction } from "i18next";
import { cn } from "../../lib/utils";
import type { DiffRow } from "../../lib/diff-parser";
import type { DiffRun } from "../../lib/diff-highlight";
import { FileAnnotationModel, fileAnnotationKey } from "./diff-annotation-types";
import { diffRowTone, diffMarkerGlyph } from "./DiffView";
import { LineFeedbackButton, rowAnnotationTargets, lineAnnotationTarget } from "./LineFeedback";
import { renderDiffRuns } from "./DiffChrome";
import { FileAnnotationComposer } from "./FileAnnotationComposer";

export const HunkBand = memo(function HunkBand({ row }: { row: DiffRow }) {
	return (
		<div className="flex select-none items-baseline gap-2 bg-surface-faint px-2.5 py-0.75 text-passive">
			<span className="shrink-0 text-passive/70">{row.text}</span>
			{row.section ? <span className="min-w-0 truncate text-passive/90">{row.section}</span> : null}
		</div>
	);
});

export type DiffRowContentProps = {
	annotation: FileAnnotationModel;
	index: number;
	path: string;
	previousPath?: string;
	row: DiffRow;
	runs: DiffRun[];
	t: TFunction;
	wrap: boolean;
};

// One unified-view diff row, shared between the plain (non-virtualized) and
// virtualized render paths so they can't drift apart from each other.
export function DiffRowContentInner({ annotation, index, path, previousPath, row, runs, t, wrap }: DiffRowContentProps) {
	if (row.kind === "hunk") return <HunkBand row={row} />;
	return (
		<div>
			<div
				className={cn("group/line relative flex", diffRowTone[row.kind])}
				data-diff-row=""
				data-kind={row.kind}
				data-new-no={row.newNo ?? ""}
				data-old-no={row.oldNo ?? ""}
				data-row-index={index}
			>
				<LineFeedbackButton
					active={rowAnnotationTargets(annotation.targets, path, index).length > 0}
					onClick={() => annotation.begin(lineAnnotationTarget(path, previousPath, row, index))}
					t={t}
					target={lineAnnotationTarget(path, previousPath, row, index)}
				/>
				<span className="w-9 shrink-0 select-none border-r border-border/50 bg-terminal px-1.5 text-right text-passive/70 tabular-nums">
					{row.newNo ?? row.oldNo ?? ""}
				</span>
				<span
					className={cn(
						"w-4 shrink-0 select-none text-center",
						row.kind === "add" && "text-success",
						row.kind === "del" && "text-error",
					)}
				>
					{diffMarkerGlyph[row.kind]}
				</span>
				<span className={cn("pr-3", wrap ? "whitespace-pre-wrap break-all" : "whitespace-pre")}>
					{renderDiffRuns(runs, row.kind === "add")}
				</span>
			</div>
			{rowAnnotationTargets(annotation.targets, path, index).map((target) => (
				<FileAnnotationComposer annotation={annotation} key={fileAnnotationKey(target)} target={target} />
			))}
		</div>
	);
}

// `annotation` is a fresh object on every SessionFilesView render (its draft
// text, send status, etc. all live there), so a plain memo would never skip
// a re-render — every row would see a "changed" prop on every keystroke
// anywhere in the panel, on top of every scroll tick. Only a row that HAS or
// HAD an open annotation box actually needs to re-render when
// `annotation` changes; every other row only cares about `row`/`index`/
// `path`/`previousPath`/`wrap`, which are stable across scroll-driven
// re-renders. This is what actually lets scrolling skip re-running the
// i18next calls, cn() calls, and nested Button for rows that are already
// mounted and unchanged.
export const DiffRowContent = memo(DiffRowContentInner, (prev, next) => {
	if (
		prev.row !== next.row ||
		prev.index !== next.index ||
		prev.path !== next.path ||
		prev.previousPath !== next.previousPath ||
		prev.runs !== next.runs ||
		prev.wrap !== next.wrap
	) {
		return false;
	}
	const prevActive = rowAnnotationTargets(prev.annotation.targets, prev.path, prev.index).length > 0;
	const nextActive = rowAnnotationTargets(next.annotation.targets, next.path, next.index).length > 0;
	return !prevActive && !nextActive;
});

export type SplitRow =
	| { kind: "hunk"; row: DiffRow }
	| { kind: "pair"; left: DiffRow | null; leftIndex: number | null; right: DiffRow | null; rightIndex: number | null };

// toSplitRows aligns the unified rows into left (old) / right (new) pairs: each
// run of deletions lines up index-for-index with the additions that follow it,
// context appears on both sides, and hunk headers span the full width. Each
// side also carries its original index into the flat `rows` array (leftIndex /
// rightIndex) so SplitSide can stamp the same data-row-index the unified view
// uses, without SplitSide re-deriving it via `rows.indexOf`.
export function toSplitRows(rows: DiffRow[]): SplitRow[] {
	const out: SplitRow[] = [];
	let dels: Array<{ row: DiffRow; index: number }> = [];
	let adds: Array<{ row: DiffRow; index: number }> = [];
	const flush = () => {
		const count = Math.max(dels.length, adds.length);
		for (let i = 0; i < count; i += 1) {
			out.push({
				kind: "pair",
				left: dels[i]?.row ?? null,
				leftIndex: dels[i]?.index ?? null,
				right: adds[i]?.row ?? null,
				rightIndex: adds[i]?.index ?? null,
			});
		}
		dels = [];
		adds = [];
	};
	rows.forEach((row, index) => {
		if (row.kind === "del") {
			dels.push({ row, index });
			return;
		}
		if (row.kind === "add") {
			adds.push({ row, index });
			return;
		}
		flush();
		if (row.kind === "hunk") out.push({ kind: "hunk", row });
		else out.push({ kind: "pair", left: row, leftIndex: index, right: row, rightIndex: index });
	});
	flush();
	return out;
}
